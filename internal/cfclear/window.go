package cfclear

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

// The person ticks the check's box in a plain browser window: no remote
// control (no DevTools or WebDriver BiDi port), only ppgmods' own profile.
// A browser under remote control can be recognised by the check, which then
// accepts the tick and asks again, over and over. Whatever the check sets
// stays in the profile, where the background browser reads pages with it.

// showWindow is plainWindow (a test hook).
var showWindow = plainWindow

// windowTimeout is how long the window may stay open.
const windowTimeout = 10 * time.Minute

// plainWindow opens u in exe with profile and waits until the window is
// closed (or windowTimeout).
func plainWindow(u, exe, profile string, logf func(string, ...any)) error {
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return err
	}
	var args []string
	if isFirefox(exe) {
		args = []string{"--profile", profile, "--no-remote", "--new-instance", u}
	} else {
		args = []string{"--user-data-dir=" + profile, "--no-first-run", "--no-default-browser-check", "--new-window", u}
	}
	ctx, cancel := context.WithTimeout(context.Background(), windowTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	logf(`a browser window is open on smods.ru: tick "Verify you are human", wait until the Skymods page shows, then close the window`)
	err := cmd.Wait()
	if ctx.Err() != nil {
		return errors.New("the browser window stayed open for 10 minutes; close it once the Skymods page shows")
	}
	_ = err // closing the window is the normal end, whatever exit code the browser gives
	return nil
}
