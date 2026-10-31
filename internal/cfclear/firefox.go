package cfclear

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

// Firefox-based browsers (Firefox, Zen, LibreWolf, Floorp, Waterfox) don't
// speak Chrome's DevTools protocol; they are driven through WebDriver BiDi,
// the W3C protocol Firefox ships. The browser runs with a throwaway profile
// whose preferences keep it quiet (no first-run pages, no telemetry) and
// send any download into the profile, never into Downloads.

// firefoxPrefs is the throwaway profile's user.js.
var firefoxPrefs = map[string]any{
	// A driven browser reports navigator.webdriver = true, which the check
	// flags; this preference turns that report off.
	"dom.webdriver.enabled":                                 false,
	"browser.shell.checkDefaultBrowser":                     false,
	"browser.aboutwelcome.enabled":                          false,
	"browser.startup.homepage_override.mstone":              "ignore",
	"startup.homepage_welcome_url":                          "about:blank",
	"browser.startup.page":                                  0,
	"datareporting.policy.dataSubmissionEnabled":            false,
	"datareporting.healthreport.uploadEnabled":              false,
	"toolkit.telemetry.enabled":                             false,
	"app.update.auto":                                       false,
	"app.update.enabled":                                    false,
	"browser.download.folderList":                           2,
	"browser.download.useDownloadDir":                       true,
	"browser.download.always_ask_before_handling_new_types": false,
	"browser.sessionstore.resume_from_crash":                false,
	"remote.active-protocols":                               1, // WebDriver BiDi only
	// Firefox announces the BiDi port with dump(), which release builds
	// only print with this on.
	"browser.dom.window.dump.enabled": true,
}

var reBiDiListening = regexp.MustCompile(`WebDriver BiDi listening on (ws://[0-9.:\[\]a-z]+)`)

// runFirefox is Run for a Firefox-based browser.
func runFirefox(u, exe, profile string, opt Options, logf func(string, ...any)) (*Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opt.Timeout)
	defer cancel()

	downloads := filepath.Join(profile, "downloads")
	os.MkdirAll(downloads, 0o700)
	var js strings.Builder
	for k, v := range firefoxPrefs {
		b, _ := json.Marshal(v)
		fmt.Fprintf(&js, "user_pref(%q, %s);\n", k, b)
	}
	b, _ := json.Marshal(downloads)
	fmt.Fprintf(&js, "user_pref(%q, %s);\n", "browser.download.dir", b)
	if err := os.WriteFile(filepath.Join(profile, "user.js"), []byte(js.String()), 0o600); err != nil {
		return nil, err
	}

	args := []string{"--remote-debugging-port", "0", "--profile", profile, "--no-remote", "--new-instance"}
	if !opt.Headed {
		args = append(args, "--headless")
	}
	// Start on the page itself: forks such as Zen open extra tabs of their
	// own, so the first tab isn't always the one the person sees.
	args = append(args, u)
	cmd := exec.CommandContext(ctx, exe, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	os.Remove(filepath.Join(profile, bidiServerFile)) // a stale one from an earlier run
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", filepath.Base(exe), err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	// The port is announced on the browser's output (stderr or stdout,
	// depending on the build) and, in newer versions, written to a file in
	// the profile; whichever comes first.
	addr := make(chan string, 2)
	var outMu sync.Mutex
	var lastOut []string // the browser's last lines, for the error
	for _, r := range []io.Reader{stderr, stdout} {
		go func(r io.Reader) {
			sc := bufio.NewScanner(r)
			for sc.Scan() {
				if line := strings.TrimSpace(sc.Text()); line != "" {
					outMu.Lock()
					if lastOut = append(lastOut, clipLine(line)); len(lastOut) > 6 {
						lastOut = lastOut[1:]
					}
					outMu.Unlock()
				}
				if m := reBiDiListening.FindStringSubmatch(sc.Text()); m != nil {
					select {
					case addr <- m[1]:
					default:
					}
				}
			}
		}(r)
	}
	var wsURL string
	deadline := time.After(30 * time.Second)
	poll := time.NewTicker(300 * time.Millisecond)
	defer poll.Stop()
	for wsURL == "" {
		select {
		case wsURL = <-addr:
		case <-poll.C:
			wsURL = bidiServerAddr(profile)
		case <-deadline:
			outMu.Lock()
			said := strings.Join(lastOut, " | ")
			outMu.Unlock()
			if said == "" {
				said = "nothing"
			}
			return nil, fmt.Errorf("the browser didn't open its WebDriver BiDi port (is it Firefox-based?); it said: %s", said)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	c, err := dialBiDi(ctx, wsURL+"/session")
	if err != nil {
		return nil, err
	}
	defer c.close()
	if _, err := c.call(ctx, "session.new", map[string]any{"capabilities": map[string]any{}}); err != nil {
		return nil, fmt.Errorf("WebDriver BiDi session: %w", err)
	}
	tab, err := pageTab(ctx, c, u)
	if err != nil {
		return nil, err
	}

	tick := time.NewTicker(700 * time.Millisecond)
	defer tick.Stop()
	prog := newProgress(logf)
	for {
		var st pageState
		raw, err := c.call(ctx, "script.evaluate", map[string]any{
			"expression":   "JSON.stringify(" + pageStateJS + ")",
			"target":       map[string]any{"context": tab},
			"awaitPromise": false,
		})
		if err == nil {
			json.Unmarshal([]byte(evalString(raw)), &st)
		} else if ctx.Err() == nil && strings.Contains(err.Error(), "closed the connection") {
			if opt.Headed {
				return nil, errors.New("the browser window was closed before the check passed")
			}
			return nil, fmt.Errorf("the background browser stopped: %w", err)
		}
		if st.loaded() {
			res := &Result{UserAgent: st.UA, At: time.Now().UTC()}
			if raw, err := c.call(ctx, "script.evaluate", map[string]any{
				"expression":   `document.documentElement.outerHTML`,
				"target":       map[string]any{"context": tab},
				"awaitPromise": false,
			}); err == nil {
				res.HTML = evalString(raw)
			}
			if raw, err := c.call(ctx, "storage.getCookies", map[string]any{"partition": map[string]any{"type": "context", "context": tab}}); err == nil {
				var cs struct {
					Cookies []struct {
						Name   string `json:"name"`
						Domain string `json:"domain"`
						Value  struct {
							Value string `json:"value"`
						} `json:"value"`
					} `json:"cookies"`
				}
				json.Unmarshal(raw, &cs)
				for _, k := range cs.Cookies {
					res.Cookies = append(res.Cookies, Cookie{Name: k.Name, Value: k.Value.Value, Domain: k.Domain})
				}
			}
			prog.done(res)
			return res, nil
		}
		if !opt.Headed && prog.elapsed() >= opt.PersonAfter {
			return nil, ErrNeedsPerson
		}
		prog.note(st.Title)
		select {
		case <-ctx.Done():
			return nil, timeoutErr(opt)
		case <-tick.C:
		}
	}
}

// pageTab finds the tab showing u (the browser was started on it), or opens
// a new tab on u and brings it to the front.
func pageTab(ctx context.Context, c *bidiConn, u string) (string, error) {
	host := u
	if pu, err := url.Parse(u); err == nil && pu.Host != "" {
		host = pu.Host
	}
	for wait := 0; wait < 10; wait++ {
		tree, err := c.call(ctx, "browsingContext.getTree", map[string]any{})
		if err != nil {
			return "", err
		}
		var t struct {
			Contexts []struct {
				Context string `json:"context"`
				URL     string `json:"url"`
			} `json:"contexts"`
		}
		json.Unmarshal(tree, &t)
		for _, x := range t.Contexts {
			if strings.Contains(x.URL, host) {
				return x.Context, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	raw, err := c.call(ctx, "browsingContext.create", map[string]any{"type": "tab"})
	if err != nil {
		return "", fmt.Errorf("WebDriver BiDi: no tab for the page: %w", err)
	}
	var created struct {
		Context string `json:"context"`
	}
	if json.Unmarshal(raw, &created) != nil || created.Context == "" {
		return "", errors.New("WebDriver BiDi: no browser tab")
	}
	if _, err := c.call(ctx, "browsingContext.navigate", map[string]any{"context": created.Context, "url": u, "wait": "none"}); err != nil {
		return "", fmt.Errorf("the browser could not open the page: %w", err)
	}
	c.call(ctx, "browsingContext.activate", map[string]any{"context": created.Context})
	return created.Context, nil
}

func clipLine(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// bidiServerFile is where newer Firefox versions write the BiDi server's
// address in the profile.
const bidiServerFile = "WebDriverBiDiServer.json"

// bidiServerAddr reads the BiDi server's address from the profile ("" until
// the browser has written it).
func bidiServerAddr(profile string) string {
	b, err := os.ReadFile(filepath.Join(profile, bidiServerFile))
	if err != nil {
		return ""
	}
	var f struct {
		Host string `json:"ws_host"`
		Port int    `json:"ws_port"`
	}
	if json.Unmarshal(b, &f) != nil || f.Port <= 0 || f.Port > 65535 {
		return ""
	}
	if f.Host == "" {
		f.Host = "127.0.0.1"
	}
	return fmt.Sprintf("ws://%s", net.JoinHostPort(f.Host, strconv.Itoa(f.Port)))
}

// evalString is the string a script.evaluate reply carries, or "".
func evalString(raw json.RawMessage) string {
	var ev struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	json.Unmarshal(raw, &ev)
	return ev.Result.Value
}

// bidiConn is a minimal WebDriver BiDi client: commands with ids, replies
// matched by id, events ignored.
type bidiConn struct {
	conn    net.Conn
	mu      sync.Mutex
	next    int
	pending map[int]chan bidiReply
	done    chan struct{}
}

type bidiReply struct {
	Result  json.RawMessage `json:"result"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
}

func dialBiDi(ctx context.Context, u string) (*bidiConn, error) {
	conn, _, _, err := ws.Dial(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("WebDriver BiDi: %w", err)
	}
	c := &bidiConn{conn: conn, pending: map[int]chan bidiReply{}, done: make(chan struct{})}
	go c.read()
	return c, nil
}

func (c *bidiConn) read() {
	defer close(c.done)
	for {
		b, err := wsutil.ReadServerText(c.conn)
		if err != nil {
			return
		}
		var m struct {
			ID   *int   `json:"id"`
			Type string `json:"type"`
			bidiReply
		}
		if json.Unmarshal(b, &m) != nil || m.ID == nil {
			continue // an event
		}
		c.mu.Lock()
		ch := c.pending[*m.ID]
		delete(c.pending, *m.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- m.bidiReply
		}
	}
}

func (c *bidiConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.next++
	id := c.next
	ch := make(chan bidiReply, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	b, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err := wsutil.WriteClientText(c.conn, b); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		if r.Error != "" {
			return nil, fmt.Errorf("%s: %s %s", method, r.Error, r.Message)
		}
		return r.Result, nil
	case <-c.done:
		return nil, errors.New("WebDriver BiDi: the browser closed the connection")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *bidiConn) close() { c.conn.Close() }

// isFirefox reports whether exe is a Firefox-based browser.
func isFirefox(exe string) bool {
	n := strings.ToLower(filepath.Base(exe))
	for _, f := range []string{"firefox", "zen", "librewolf", "floorp", "waterfox", "mercury", "icecat"} {
		if strings.Contains(n, f) {
			return true
		}
	}
	return false
}
