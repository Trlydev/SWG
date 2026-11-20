package cfclear

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeSession struct {
	mu     sync.Mutex
	reads  int
	closed bool
	err    error
}

func (f *fakeSession) read(u string, _ Options, _ func(string, ...any)) (*Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.err != nil {
		return nil, f.err
	}
	return &Result{HTML: u}, nil
}

func (f *fakeSession) close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
}

func (f *fakeSession) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// The browser started for the first read serves the next ones; another
// browser or profile, a failure, the idle time and CloseBrowser close it.
func TestKeptBrowser(t *testing.T) {
	defer CloseBrowser()
	var started []*fakeSession
	start := func() (browserSession, error) {
		s := &fakeSession{}
		started = append(started, s)
		return s, nil
	}
	read := func(key string) {
		t.Helper()
		if res, err := readKept("https://x/"+key, key, Options{}, nil, start); err != nil || res.HTML != "https://x/"+key {
			t.Fatalf("read: %v %v", res, err)
		}
	}
	read("a")
	read("a")
	read("a")
	if len(started) != 1 || started[0].reads != 3 {
		t.Fatalf("started %d browsers; first read %d pages", len(started), started[0].reads)
	}

	read("b") // another browser or profile
	if len(started) != 2 || !started[0].isClosed() {
		t.Fatal("the browser for another profile didn't replace the kept one")
	}

	started[1].err = errors.New("the background browser stopped")
	read("b") // retried in a new one
	if len(started) != 3 || !started[1].isClosed() {
		t.Fatal("a failed browser wasn't replaced")
	}

	// A check that wants a person keeps the browser (it's fine, the site
	// just wants a tick).
	started[2].err = ErrNeedsPerson
	if _, err := readKept("https://x/b", "b", Options{}, nil, start); !errors.Is(err, ErrNeedsPerson) {
		t.Fatal(err)
	}
	if started[2].isClosed() || len(started) != 3 {
		t.Fatal("closed over a check that wants a person")
	}
	started[2].err = nil

	CloseBrowser()
	if !started[2].isClosed() {
		t.Fatal("CloseBrowser left the browser running")
	}
}

func TestKeptBrowserIdle(t *testing.T) {
	defer CloseBrowser()
	old := keepIdle
	keepIdle = 50 * time.Millisecond
	defer func() { keepIdle = old }()
	s := &fakeSession{}
	if _, err := readKept("https://x/", "k", Options{}, nil, func() (browserSession, error) { return s, nil }); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if s.isClosed() {
		t.Fatal("closed before the idle time")
	}
	time.Sleep(150 * time.Millisecond)
	if !s.isClosed() {
		t.Fatal("still running after the idle time")
	}
}

// A window needs the profile: the kept browser is closed first. And the
// command line (keeping off) never keeps one.
func TestKeepBrowserOff(t *testing.T) {
	s := &fakeSession{}
	keepOn.Store(true)
	readKept("https://x/", "k", Options{}, nil, func() (browserSession, error) { return s, nil })
	KeepBrowser(false)
	if !s.isClosed() || keepOn.Load() {
		t.Fatal("turning keeping off left the browser running")
	}
}
