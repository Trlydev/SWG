// Package steamdl downloads Steam Workshop items from Steam itself, for an
// account signed in once with a QR code (the Steam mobile app), through
// DepotDownloader (github.com/SteamRE/DepotDownloader, GPL-2.0, built on
// SteamKit).
//
// Steam serves Workshop files only to a signed-in account that owns the game:
// there is no public link. DepotDownloader speaks Steam's protocol and signs
// in with Steam's QR flow, so nobody types a password into ppgmods (or
// anywhere): they scan the code with the Steam app on their phone, once.
// Steam's session token is kept by DepotDownloader in ppgmods' own folder
// (on Windows, in DepotDownloader's per-user store) and reused for every
// download until Steam ends the session.
//
// ppgmods fetches a pinned DepotDownloader release from GitHub and checks it
// against the SHA-256 it carries for that release, so a changed download is
// refused. It runs with its own Steam login ID, so the person's Steam client
// stays signed in.
package steamdl

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Trlydev/SWG/internal/sources"
)

// Version is the DepotDownloader release ppgmods uses.
const Version = "3.4.0"

const (
	releaseURL = "https://github.com/SteamRE/DepotDownloader/releases/download/DepotDownloader_" + Version + "/"
	// AppID is People Playground.
	AppID = 1118200
	// loginID is ppgmods' own Steam connection id. DepotDownloader would
	// otherwise use the default one, which signs the Steam client out.
	loginID = "1347440433"
)

// The release files and their SHA-256 (checked against the published
// release on 2026-11-20).
var assets = map[string]struct{ name, sha256 string }{
	"linux/amd64":   {"DepotDownloader-linux-x64.zip", "a999dec66b4850fc961bd50366696d23c2d0fad7b18790e6a5647b2f19097a53"},
	"linux/arm64":   {"DepotDownloader-linux-arm64.zip", "d9fb612ccebc1db8eeea3b4045d2221ec70431381393ce908fb72f01d4f9c812"},
	"windows/amd64": {"DepotDownloader-windows-x64.zip", "41c9e9f0df54b3ad02e67a11726756e5c73283bd7c2e1b04acfa5ae4c2ed3767"},
	"windows/arm64": {"DepotDownloader-windows-arm64.zip", "1449ba47775e9974036e615bedf00d72bef747cc5b93a78048ed4e0b2c63b2b3"},
	"darwin/amd64":  {"DepotDownloader-macos-x64.zip", "3214b689564d73e9342a8a4aef693de6ad3d293801b0f300a4466f60ec75befb"},
	"darwin/arm64":  {"DepotDownloader-macos-arm64.zip", "60e80c7c496f3f9a079cd3c62036b35d088c27bc0149baf38f009eb57a52f6a5"},
}

var (
	// ErrSignIn: no Steam session (never signed in, signed out, or Steam
	// ended it): sign in with the QR code.
	ErrSignIn = errors.New("sign in to Steam first")
	// ErrUnsupported: no DepotDownloader build for this computer.
	ErrUnsupported = errors.New("Steam downloads aren't available on this computer")
)

// Tool is DepotDownloader in a folder of ppgmods'.
type Tool struct {
	Dir string // e.g. <config>/steam

	// Download fetches a URL into a file and returns its SHA-256 (a test
	// hook; sources.Download by default).
	Download func(url, path string, max int64) (string, error)
}

var runMu sync.Mutex // one Steam session at a time

func (t *Tool) exe() string {
	n := "DepotDownloader"
	if runtime.GOOS == "windows" {
		n += ".exe"
	}
	return filepath.Join(t.Dir, "depotdownloader-"+Version, n)
}

// home is DepotDownloader's home (its saved session lives under it).
func (t *Tool) home() string { return filepath.Join(t.Dir, "home") }

// Installed reports whether DepotDownloader is in place.
func (t *Tool) Installed() bool {
	st, err := os.Stat(t.exe())
	return err == nil && !st.IsDir()
}

// Install fetches the pinned DepotDownloader release for this computer and
// checks it before unpacking.
func (t *Tool) Install() error {
	a, ok := assets[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return ErrUnsupported
	}
	if t.Installed() {
		return nil
	}
	if err := os.MkdirAll(t.Dir, 0o700); err != nil {
		return err
	}
	dl := t.Download
	if dl == nil {
		dl = func(u, p string, max int64) (string, error) {
			_, sum, err := sources.Download(u, p, max)
			return sum, err
		}
	}
	tmp := filepath.Join(t.Dir, a.name+".part")
	defer os.Remove(tmp)
	sum, err := dl(releaseURL+a.name, tmp, 200<<20)
	if err != nil {
		return fmt.Errorf("downloading DepotDownloader: %w", err)
	}
	if !strings.EqualFold(sum, a.sha256) {
		return fmt.Errorf("DepotDownloader download doesn't match the release ppgmods knows (SHA-256 %s)", sum)
	}
	zr, err := zip.OpenReader(tmp)
	if err != nil {
		return err
	}
	defer zr.Close()
	want := filepath.Base(t.exe())
	for _, f := range zr.File {
		if f.Name != want && f.Name != "LICENSE" {
			continue
		}
		dst := filepath.Join(filepath.Dir(t.exe()), f.Name)
		if err := extract(f, dst); err != nil {
			return err
		}
	}
	if !t.Installed() {
		return errors.New("DepotDownloader's release has no " + want)
	}
	return nil
}

func extract(f *zip.File, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(dst+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(rc, 300<<20)); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(dst+".tmp", dst)
}

// env is DepotDownloader's environment: its own home (so its saved session
// stays in ppgmods' folder), no telemetry, no ICU needed.
func (t *Tool) env() []string {
	env := os.Environ()
	if runtime.GOOS != "windows" {
		env = append(env, "HOME="+t.home(), "XDG_DATA_HOME="+filepath.Join(t.home(), ".local", "share"))
	}
	return append(env,
		"DOTNET_BUNDLE_EXTRACT_BASE_DIR="+filepath.Join(t.Dir, "cache"),
		"DOTNET_CLI_TELEMETRY_OPTOUT=1",
		"DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1")
}

// run starts DepotDownloader with args and calls line for each line it
// prints; line returns false to stop it. It returns what the process ended
// with and its last lines.
func (t *Tool) run(ctx context.Context, args []string, line func(string) bool) (string, error) {
	runMu.Lock()
	defer runMu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, t.exe(), args...)
	cmd.Env = t.env()
	cmd.Dir = t.Dir
	cmd.Stdin = nil // never a prompt answered
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); pw.Close() }()
	var last []string
	stopped := false
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	sc.Split(scanLines)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), " \r")
		if strings.TrimSpace(l) != "" {
			if last = append(last, l); len(last) > 8 {
				last = last[1:]
			}
		}
		if !stopped && !line(sc.Text()) {
			stopped = true
			cancel()
		}
	}
	err := <-done
	tail := strings.Join(last, " | ")
	if stopped {
		return tail, context.Canceled
	}
	return tail, err
}

// scanLines splits on \n and also on prompts that end without one.
func scanLines(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i], nil
	}
	for _, p := range prompts {
		if i := bytes.Index(data, []byte(p)); i >= 0 {
			return i + len(p), data[:i+len(p)], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// prompts DepotDownloader asks without a newline; any of them means there
// is no usable session (ppgmods never answers one).
var prompts = []string{"Enter account password", "Please enter your 2 factor auth code", "Please enter the authentication code"}

func isPrompt(l string) bool {
	for _, p := range prompts {
		if strings.Contains(l, p) {
			return true
		}
	}
	return strings.Contains(l, "Access token was rejected")
}

// SignIn signs in with Steam's QR flow: onQR gets each code to show (Steam
// replaces it every so often) as rows of '1' (dark) and '0' (light). It
// returns the signed-in account once Steam has accepted the scan and the
// session is saved. Cancel ctx to stop waiting.
func (t *Tool) SignIn(ctx context.Context, onQR func(rows []string)) (string, error) {
	if !t.Installed() {
		if err := t.Install(); err != nil {
			return "", err
		}
	}
	tmp := filepath.Join(t.Dir, "signin")
	defer os.RemoveAll(tmp)
	// -manifest-only: once signed in, it only writes the game's file list
	// (a small step that proves the account can download).
	args := []string{"-app", fmt.Sprint(AppID), "-manifest-only", "-qr", "-remember-password", "-loginid", loginID, "-dir", tmp}
	var qr []string
	inQR := false
	tail, err := t.run(ctx, args, func(l string) bool {
		if row, ok := qrRow(l); ok {
			if !inQR {
				qr, inQR = nil, true
			}
			qr = append(qr, row)
			return true
		}
		if inQR {
			inQR = false
			if rows := trimQR(qr); len(rows) >= 21 {
				onQR(rows)
			}
		}
		return true
	})
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		return "", context.Canceled
	}
	acct := t.Account()
	if acct == "" {
		return "", fmt.Errorf("Steam sign-in didn't finish (%s)", tail)
	}
	os.WriteFile(filepath.Join(t.Dir, "account"), []byte(acct), 0o600)
	return acct, nil
}

// qrRow reads one line of DepotDownloader's text QR code: each module is
// two characters, "██" dark or "  " light.
func qrRow(l string) (string, bool) {
	l = strings.TrimRight(l, "\r")
	if !strings.Contains(l, "█") || strings.Trim(l, "█ ") != "" {
		return "", false
	}
	r := []rune(l)
	if len(r)%2 == 1 {
		r = append(r, ' ')
	}
	var b strings.Builder
	for i := 0; i < len(r); i += 2 {
		if r[i] == '█' {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	return b.String(), true
}

// trimQR evens out the rows and drops the light quiet-zone columns at both
// sides (its blank lines above and below aren't read as rows), leaving the
// code itself; whoever draws it adds the margin.
func trimQR(rows []string) []string {
	w := 0
	for _, r := range rows {
		w = max(w, len(r))
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r + strings.Repeat("0", w-len(r))
	}
	left, right := w, 0
	for _, r := range out {
		if i := strings.IndexByte(r, '1'); i >= 0 {
			left = min(left, i)
			right = max(right, strings.LastIndexByte(r, '1')+1)
		}
	}
	if right <= left {
		return nil
	}
	for i := range out {
		out[i] = out[i][left:right]
	}
	return out
}

// Account is the account whose Steam session is saved ("" if none):
// ppgmods' note of it, confirmed by DepotDownloader's session file.
func (t *Tool) Account() string {
	tokens := t.savedAccounts()
	if b, err := os.ReadFile(filepath.Join(t.Dir, "account")); err == nil {
		a := strings.TrimSpace(string(b))
		for _, x := range tokens {
			if strings.EqualFold(x, a) {
				return a
			}
		}
	}
	if len(tokens) == 1 {
		return tokens[0]
	}
	return ""
}

// savedAccounts reads the accounts DepotDownloader holds a session for, from
// its account.config (deflate-compressed protobuf; field 4 maps account name
// to refresh token).
func (t *Tool) savedAccounts() []string {
	var out []string
	for _, p := range t.configFiles() {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(flate.NewReader(bytes.NewReader(b)), 1<<20))
		if err != nil && len(raw) == 0 {
			continue
		}
		out = append(out, loginTokenAccounts(raw)...)
	}
	return out
}

func (t *Tool) configFiles() []string {
	var roots []string
	if runtime.GOOS == "windows" {
		roots = []string{filepath.Join(os.Getenv("LocalAppData"), "IsolatedStorage")}
	} else {
		roots = []string{filepath.Join(t.home(), ".local", "share", "IsolatedStorage")}
	}
	var out []string
	for _, r := range roots {
		filepath.WalkDir(r, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && d.Name() == "account.config" {
				out = append(out, p)
			}
			return nil
		})
	}
	return out
}

// loginTokenAccounts reads the keys of field 4 (LoginTokens) of the store.
func loginTokenAccounts(b []byte) []string {
	var out []string
	for len(b) > 0 {
		key, n := uvarint(b)
		if n <= 0 {
			break
		}
		b = b[n:]
		field, wire := key>>3, key&7
		switch wire {
		case 0:
			_, n := uvarint(b)
			if n <= 0 {
				return out
			}
			b = b[n:]
		case 2:
			l, n := uvarint(b)
			if n <= 0 || uint64(len(b)-n) < l {
				return out
			}
			msg := b[n : n+int(l)]
			b = b[n+int(l):]
			if field == 4 {
				if k, v := mapEntry(msg); k != "" && v != "" {
					out = append(out, k)
				}
			}
		case 5:
			if len(b) < 4 {
				return out
			}
			b = b[4:]
		case 1:
			if len(b) < 8 {
				return out
			}
			b = b[8:]
		default:
			return out
		}
	}
	return out
}

// mapEntry reads a protobuf map entry's string key (1) and value (2).
func mapEntry(b []byte) (k, v string) {
	for len(b) > 0 {
		key, n := uvarint(b)
		if n <= 0 || key&7 != 2 {
			return
		}
		b = b[n:]
		l, n := uvarint(b)
		if n <= 0 || uint64(len(b)-n) < l {
			return
		}
		s := string(b[n : n+int(l)])
		b = b[n+int(l):]
		switch key >> 3 {
		case 1:
			k = s
		case 2:
			v = s
		}
	}
	return
}

func uvarint(b []byte) (uint64, int) {
	var x uint64
	for i, c := range b {
		if i == 10 {
			return 0, -1
		}
		x |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return x, i + 1
		}
	}
	return 0, 0
}

// SignOut forgets the Steam session: ppgmods' note, and DepotDownloader's
// saved session where it lives in ppgmods' folder (on Windows it stays in
// DepotDownloader's own store, unused).
func (t *Tool) SignOut() error {
	os.Remove(filepath.Join(t.Dir, "account"))
	if runtime.GOOS != "windows" {
		return os.RemoveAll(t.home())
	}
	return nil
}

var reDigits = regexp.MustCompile(`^\d{1,20}$`)

// DownloadItem downloads Workshop item ws for the signed-in account into
// dest (made empty first) and returns dest. ErrSignIn means there is no
// usable session.
func (t *Tool) DownloadItem(ctx context.Context, ws, dest string) (string, error) {
	if !reDigits.MatchString(ws) {
		return "", fmt.Errorf("not a Workshop item id: %q", ws)
	}
	acct := t.Account()
	if acct == "" {
		return "", ErrSignIn
	}
	if !t.Installed() {
		if err := t.Install(); err != nil {
			return "", err
		}
	}
	os.RemoveAll(dest)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	args := []string{"-app", fmt.Sprint(AppID), "-pubfile", ws, "-username", acct, "-remember-password", "-loginid", loginID, "-dir", dest}
	needSignIn := false
	tail, err := t.run(ctx, args, func(l string) bool {
		if isPrompt(l) {
			needSignIn = true
			return false
		}
		return true
	})
	if needSignIn {
		return "", ErrSignIn
	}
	if err != nil {
		if ctx.Err() != nil {
			return "", errors.New("the Steam download took too long")
		}
		return "", fmt.Errorf("Steam download failed: %v (%s)", err, tail)
	}
	os.RemoveAll(filepath.Join(dest, ".DepotDownloader")) // its own bookkeeping
	ents, _ := os.ReadDir(dest)
	if len(ents) == 0 {
		return "", fmt.Errorf("Steam download produced no files (%s)", tail)
	}
	return dest, nil
}
