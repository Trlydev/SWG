package cfclear

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Trlydev/SWG/internal/manager"
	"github.com/Trlydev/SWG/internal/sources"
)

const skymodsURL = "https://catalogue.smods.ru/"

// attemptCooldown is how long after a failed automatic attempt the next one
// waits: a check that wants a person doesn't pass by launching more
// background browsers at it.
const attemptCooldown = 10 * time.Minute

// How Skymods pages are read when Cloudflare checks ppgmods' own requests.
// smods.ru refuses the clearance cookie from anything but the browser that
// earned it (seen: a fresh cookie, with the same User-Agent and IP, still
// answered 403 to ppgmods' HTTP client), so the page is read by that
// browser: it runs in the background with ppgmods' own profile for smods.ru,
// which keeps the clearance between runs. Everything happens in the
// background: a check that passes by itself does so in seconds, and one that
// wants a person to tick its box is simply tried again later
// (KeepSkymodsWorking); meanwhile the other sites keep working.

type attempt struct {
	At    time.Time `json:"at"`
	OK    bool      `json:"ok"`
	Error string    `json:"error,omitempty"`
	// NeedsPerson: the check wanted a person to tick its box; it is tried
	// again later by itself.
	NeedsPerson bool `json:"needs_person,omitempty"`

	Running bool `json:"running"`
}

var (
	autoMu      sync.Mutex
	autoRunning int  // browsers running for site reads and checks
	checking    bool // a Check is running
	lastAttempt = map[string]attempt{}

	// browserMu serialises browsers on ppgmods' profile: a browser locks
	// its profile while it runs.
	browserMu sync.Mutex

	autoOn atomic.Bool

	// goRejectedAt is when Cloudflare last refused a stored clearance from
	// ppgmods' HTTP client: after that, the page is read by the browser
	// straight away instead of trying the cookie first.
	goRejectedAt atomic.Int64

	run = Run // a test hook
)

func init() { autoOn.Store(true) }

// SetAuto turns the automatic background check on or off (Settings).
func SetAuto(on bool) { autoOn.Store(on) }

// Status reports how the last check for a site went.
func Status(site string) attempt {
	autoMu.Lock()
	defer autoMu.Unlock()
	a := lastAttempt[site]
	a.Running = autoRunning > 0 || checking
	return a
}

// CheckRunning reports whether a check (Check) is in flight right now.
func CheckRunning() bool {
	autoMu.Lock()
	defer autoMu.Unlock()
	return checking
}

// ProfileDir is ppgmods' own profile for the browser exe: kept between
// runs (so a check passed once stays passed), never the person's profile.
func ProfileDir(exe string) (string, error) {
	base := sandboxProfileBase(exe)
	if base == "" {
		d, err := manager.ConfigDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(d, "browser")
	}
	return filepath.Join(base, "ppgmods-"+profileName(exe)), nil
}

var reProfileName = regexp.MustCompile(`[^a-z0-9]+`)

func profileName(exe string) string {
	n := strings.TrimSuffix(strings.ToLower(filepath.Base(exe)), ".exe")
	n = strings.Trim(reProfileName.ReplaceAllString(n, "-"), "-")
	if n == "" {
		n = "browser"
	}
	return n
}

// open runs the browser on ppgmods' profile.
func open(u string, opt Options) (*Result, error) {
	if opt.Browser == "" {
		exe, err := FindBrowser()
		if err != nil {
			return nil, err
		}
		opt.Browser = exe
	}
	if opt.ProfileDir == "" {
		d, err := ProfileDir(opt.Browser)
		if err != nil {
			return nil, err
		}
		opt.ProfileDir = d
	}
	autoMu.Lock()
	autoRunning++
	autoMu.Unlock()
	defer func() {
		autoMu.Lock()
		autoRunning--
		autoMu.Unlock()
	}()
	browserMu.Lock()
	defer browserMu.Unlock()
	return run(u, opt)
}

// record notes how an attempt went and stores the clearance it earned.
func record(site string, res *Result, err error) {
	a := attempt{At: time.Now(), OK: err == nil}
	if err != nil {
		a.Error = err.Error()
		a.NeedsPerson = errors.Is(err, ErrNeedsPerson)
	} else if ck := res.Cookie("cf_clearance"); ck != "" {
		Save(&Clearance{Site: site, Cookie: ck, UserAgent: res.UserAgent, At: time.Now().UTC()})
	}
	autoMu.Lock()
	lastAttempt[site] = a
	autoMu.Unlock()
}

// recentFailure is the last attempt's error if it failed within the cooldown.
func recentFailure(site string) error {
	autoMu.Lock()
	defer autoMu.Unlock()
	if a, ok := lastAttempt[site]; ok && !a.OK && time.Since(a.At) < attemptCooldown {
		if a.NeedsPerson {
			return ErrNeedsPerson
		}
		return errors.New(a.Error)
	}
	return nil
}

// BrowserGet reads a page of site through the browser on ppgmods' profile,
// in the background: past a check that passes by itself, or with the
// clearance an earlier check left in the profile. It doesn't wait on a check
// that wants a person (ErrNeedsPerson), and after a failure it doesn't
// launch the browser again for attemptCooldown.
func BrowserGet(site, u string, logf func(string, ...any)) ([]byte, error) {
	return browserGet(site, u, logf, "")
}

func browserGet(site, u string, logf func(string, ...any), exe string) ([]byte, error) {
	if !autoOn.Load() {
		return nil, errors.New("the automatic browser check is off in Settings")
	}
	if err := recentFailure(site); err != nil {
		return nil, err
	}
	autoMu.Lock()
	busy := checking
	autoMu.Unlock()
	if busy {
		// Don't queue behind a window the person may take minutes over.
		return nil, errors.New("the browser check is open right now; try again once it has passed")
	}
	res, err := open(u, Options{Logf: logf, Timeout: time.Minute, Browser: exe})
	if err != nil && !errors.Is(err, ErrNeedsPerson) && !errors.Is(err, ErrNoBrowser) {
		// The browser itself failed (it didn't start, the page didn't load):
		// one retry before the cooldown.
		res, err = open(u, Options{Logf: logf, Timeout: time.Minute, Browser: exe})
	}
	record(site, res, err)
	if err != nil {
		if errors.Is(err, ErrNeedsPerson) && logf != nil {
			logf("%v: Skymods results are missing for now; ppgmods tries again by itself in the background", err)
		}
		return nil, err
	}
	return []byte(res.HTML), nil
}

// Check tries site's check now, in the background (the automatic retries
// and the CLI). It ignores the cooldown. A check that wants a person to
// tick its box isn't shown to anyone: it is tried again later by itself.
func Check(site, u string, logf func(string, ...any)) error {
	return check(site, u, logf, "")
}

func check(site, u string, logf func(string, ...any), exe string) error {
	autoMu.Lock()
	if checking {
		autoMu.Unlock()
		return errors.New("the check is already running")
	}
	checking = true
	autoMu.Unlock()
	defer func() {
		autoMu.Lock()
		checking = false
		autoMu.Unlock()
	}()
	res, err := open(u, Options{Logf: logf, Timeout: time.Minute, Browser: exe})
	if errors.Is(err, ErrNeedsPerson) {
		err = errors.New("smods.ru wants a person to tick its box right now; ppgmods tries again by itself")
		record(site, nil, ErrNeedsPerson)
		return err
	}
	record(site, res, err)
	if err == nil {
		goRejectedAt.Store(0)
	}
	return err
}

// retryEvery is how often a failed Skymods check is tried again while
// ppgmods runs (a test hook).
var retryEvery = 15 * time.Minute

// KeepSkymodsWorking tries smods.ru's check again in the background, now
// and then, while the last one failed: Skymods comes back by itself once
// smods.ru lets the background browser through. It stops when quit closes.
func KeepSkymodsWorking(quit <-chan struct{}, logf func(string, ...any)) {
	t := time.NewTicker(retryEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
		case <-quit:
			return
		}
		st := Status(SkymodsSite)
		if !autoOn.Load() || st.OK || st.Error == "" || st.Running || CheckRunning() {
			continue
		}
		RunSkymodsCheck(logf)
	}
}

// Forget drops everything kept for site's check: the stored clearance, the
// record of the last attempt and ppgmods' browser profile (its cookies).
func Forget(site string) error {
	autoMu.Lock()
	delete(lastAttempt, site)
	autoMu.Unlock()
	goRejectedAt.Store(0)
	// Every profile ppgmods made, also for a browser used before (another
	// PPGMODS_BROWSER, or one since uninstalled): only ppgmods-* folders.
	dirs := map[string]bool{}
	if d, err := manager.ConfigDir(); err == nil {
		ms, _ := filepath.Glob(filepath.Join(d, "browser", "ppgmods-*"))
		for _, m := range ms {
			dirs[m] = true
		}
	}
	browsers := FindBrowsers()
	if exe, err := FindBrowser(); err == nil {
		browsers = append(browsers, exe)
	}
	for _, exe := range browsers {
		if d, err := ProfileDir(exe); err == nil {
			dirs[d] = true
		}
	}
	browserMu.Lock()
	for d := range dirs {
		if strings.HasPrefix(filepath.Base(d), "ppgmods-") {
			os.RemoveAll(d)
		}
	}
	browserMu.Unlock()
	return Clear(site)
}

// RunSkymodsCheck tries smods.ru's check in the background (the automatic
// retries, the CLI) and refreshes the Skymods caches when it passes.
func RunSkymodsCheck(logf func(string, ...any)) bool {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if err := Check(SkymodsSite, skymodsURL, logf); err != nil {
		logf("the Skymods browser check did not pass: %v", err)
		return false
	}
	sources.InvalidateSkyCache()
	logf("the Skymods browser check passed; Skymods works again")
	return true
}

// WireSkymods connects the browser to the Skymods client: a stored
// clearance is tried with its own requests first, and when Cloudflare
// refuses them, the page is read through the browser.
func WireSkymods(logf func(string, ...any)) {
	sources.SkyClearance = skyClearance
	sources.SkyClearanceExpired = skyClearanceRefused
	sources.SkyBrowserGet = func(u string) ([]byte, error) {
		return BrowserGet(SkymodsSite, u, logf)
	}
}

// skyClearance is the stored clearance, unless Cloudflare refused it from
// ppgmods' client within the last day.
func skyClearance() (string, string) {
	if t := goRejectedAt.Load(); t != 0 && time.Since(time.Unix(0, t)) < 24*time.Hour {
		return "", ""
	}
	c, err := Load(SkymodsSite)
	if err != nil || c == nil {
		return "", ""
	}
	return c.Cookie, c.UserAgent
}

func skyClearanceRefused() { goRejectedAt.Store(time.Now().UnixNano()) }
