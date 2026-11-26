package cfclear

import (
	"errors"
	"os/exec"
	"testing"
	"time"
)

// The window is a plain browser process: ppgmods closes it once the file was
// handed over, and notices when the person closes it.
func TestNexusWindow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	oldFind, oldStart := findPlain, startPlain
	defer func() { findPlain, startPlain = oldFind, oldStart }()
	findPlain = func() (string, error) { return "/usr/bin/chromium", nil }
	var started *exec.Cmd
	run := func(prog string, args ...string) {
		startPlain = func(cmd *exec.Cmd) error {
			started = cmd
			cmd.Path, cmd.Args = prog, append([]string{prog}, args...)
			return cmd.Start()
		}
	}
	page := NexusPage + "294?tab=files&file_id=1&nmm=1"

	// Handed over: closing cancel ends the window.
	run("/bin/sleep", "30")
	cancel := make(chan struct{})
	time.AfterFunc(300*time.Millisecond, func() { close(cancel) })
	if err := NexusWindow(page, cancel, nil); err != nil {
		t.Fatalf("handed over: %v", err)
	}
	if started.ProcessState == nil {
		t.Fatal("the window was left open")
	}

	// The person closes it.
	run("/bin/sleep", "3.3")
	if err := NexusWindow(page, make(chan struct{}), nil); !errors.Is(err, ErrWindowClosed) {
		t.Fatalf("closed by the person: %v", err)
	}

	// Not a Nexus page: nothing opens.
	if err := NexusWindow("https://evil.example/peopleplayground/mods/1", nil, nil); err == nil {
		t.Fatal("opened a page that isn't Nexus Mods")
	}
}
