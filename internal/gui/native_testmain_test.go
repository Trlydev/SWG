//go:build cgo && !nofyne

package gui

import (
	"os"
	"sync"
	"testing"
)

// laterQueue holds what the window would redraw after a layout pass; tests
// don't need those redraws, and running them on another goroutine (as Fyne's
// test driver does with fyne.Do) races with the test's own drawing.
var laterQueue struct {
	sync.Mutex
	fs []func()
}

func TestMain(m *testing.M) {
	uiLater = func(f func()) {
		laterQueue.Lock()
		laterQueue.fs = append(laterQueue.fs, f)
		laterQueue.Unlock()
	}
	os.Exit(m.Run())
}
