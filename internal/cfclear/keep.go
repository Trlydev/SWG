package cfclear

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Starting the browser takes most of a read (headless Zen: about 0.85 s a
// read with a fresh browser, 0.2 s in a running one), and every Skymods
// page read used to start one and close it again. The window app keeps one
// running in the background instead (KeepBrowser), on ppgmods' profile:
// later reads only open the page in its tab. It is closed when it has been
// idle for keepIdle, when ppgmods quits (CloseBrowser), and before anything
// needs the profile for a visible window. A short-lived process (the command
// line) doesn't keep one, since nothing would close it after it exits.

// pollEvery is how often a read looks at the page. It used to be 700 ms,
// which was most of a read's time: a page that loads in 100 ms still
// waited for the next look (measured with Zen: 0.8 s a read).
const pollEvery = 150 * time.Millisecond

// keepIdle is how long a kept browser may sit unused (a test hook).
var keepIdle = 2 * time.Minute

var (
	keepOn atomic.Bool

	keptMu  sync.Mutex
	kept    browserSession
	keptKey string // browser executable and profile it runs with
	keptGen int    // which kept browser the idle timer belongs to
	keptIdl *time.Timer
)

// KeepBrowser turns keeping a background browser between reads on or off
// (the window app turns it on for as long as it runs).
func KeepBrowser(on bool) {
	keepOn.Store(on)
	if !on {
		CloseBrowser()
	}
}

// CloseBrowser closes the kept browser, if one is running.
func CloseBrowser() {
	keptMu.Lock()
	defer keptMu.Unlock()
	closeKeptLocked()
}

func closeKeptLocked() {
	if kept != nil {
		kept.close()
		kept, keptKey = nil, ""
	}
	if keptIdl != nil {
		keptIdl.Stop()
		keptIdl = nil
	}
	keptGen++
}

// readKept reads u with the kept browser for key, starting one if none runs
// (or the running one is for another browser or profile). A browser that
// failed in any way other than a check wanting a person is closed, so the
// next read starts afresh; a read in a kept browser that stopped is tried
// once more in a new one.
func readKept(u, key string, opt Options, logf func(string, ...any), start func() (browserSession, error)) (*Result, error) {
	keptMu.Lock()
	defer keptMu.Unlock()
	if kept != nil && keptKey != key {
		closeKeptLocked()
	}
	if kept != nil {
		res, err := kept.read(u, opt, logf)
		if err == nil || errors.Is(err, ErrNeedsPerson) {
			armIdleLocked()
			return res, err
		}
		closeKeptLocked() // it stopped or hung: a new one below
	}
	s, err := start()
	if err != nil {
		return nil, err
	}
	res, err := s.read(u, opt, logf)
	if err != nil && !errors.Is(err, ErrNeedsPerson) {
		s.close()
		return nil, err
	}
	kept, keptKey = s, key
	armIdleLocked()
	return res, err
}

// armIdleLocked (re)starts the idle timer of the kept browser.
func armIdleLocked() {
	if keptIdl != nil {
		keptIdl.Stop()
	}
	keptGen++
	gen := keptGen
	keptIdl = time.AfterFunc(keepIdle, func() {
		keptMu.Lock()
		defer keptMu.Unlock()
		if gen == keptGen { // not used since
			closeKeptLocked()
		}
	})
}
