package cfclear

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Nexus Mods gives a free account a file only from the mod's page: the
// person opens it and clicks "Slow download" (Nexus's terms and its API both
// draw the line there; premium accounts download through the API). With
// ppgmods handling nxm:// links, the page opens as a "mod manager download"
// and the click hands ppgmods the file's one-time nxm:// link.
//
// NexusWindow opens that page in a window of the person's browser on a
// profile of ppgmods' own, kept for Nexus: they sign in to Nexus there once
// and click Slow download, and the link reaches ppgmods through the nxm://
// handler like from any browser. The window is a plain browser: ppgmods
// doesn't drive or watch it (a driven browser fails Cloudflare's check on
// Nexus, and ppgmods doesn't hide that it drives one), it only closes it
// once the file was handed over.

// NexusPage is the start of every page NexusWindow opens.
const NexusPage = "https://www.nexusmods.com/peopleplayground/mods/"

// ErrWindowClosed: the person closed the window.
var ErrWindowClosed = errors.New("the Nexus Mods window was closed")

// nexusProfile is ppgmods' Nexus profile for exe (kept between windows, so
// the sign-in is), next to its background-browser profile.
func nexusProfile(exe string) (string, error) {
	d, err := ProfileDir(exe)
	if err != nil {
		return "", err
	}
	return d + "-nexus", nil
}

// quietPrefs keep a Firefox-based window from first-run pages, telemetry and
// update checks, and keep its downloads in the profile.
var quietPrefs = map[string]any{
	"browser.shell.checkDefaultBrowser":                     false,
	"browser.aboutwelcome.enabled":                          false,
	"browser.startup.homepage_override.mstone":              "ignore",
	"startup.homepage_welcome_url":                          "about:blank",
	"datareporting.policy.dataSubmissionEnabled":            false,
	"datareporting.healthreport.uploadEnabled":              false,
	"toolkit.telemetry.enabled":                             false,
	"app.update.auto":                                       false,
	"app.update.enabled":                                    false,
	"browser.download.folderList":                           2,
	"browser.download.useDownloadDir":                       true,
	"browser.download.always_ask_before_handling_new_types": false,
}

// Test hooks: how the browser is found and the window process started.
var (
	findPlain  = FindBrowser
	startPlain = func(cmd *exec.Cmd) error { return cmd.Start() }
)

// NexusWindow opens a Nexus Mods page in a window and waits: it returns
// ErrWindowClosed when the person closes it, or nil once cancel is closed
// (the file was handed over), closing the window.
func NexusWindow(page string, cancel <-chan struct{}, logf func(string, ...any)) error {
	if !strings.HasPrefix(page, NexusPage) {
		return fmt.Errorf("not a People Playground page on Nexus Mods: %s", page)
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	exe, err := findPlain()
	if err != nil {
		return err
	}
	profile, err := nexusProfile(exe)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return err
	}
	var args []string
	if isFirefox(exe) {
		downloads := filepath.Join(profile, "downloads")
		os.MkdirAll(downloads, 0o700)
		var js strings.Builder
		for k, v := range quietPrefs {
			b, _ := json.Marshal(v)
			fmt.Fprintf(&js, "user_pref(%q, %s);\n", k, b)
		}
		b, _ := json.Marshal(downloads)
		fmt.Fprintf(&js, "user_pref(%q, %s);\n", "browser.download.dir", b)
		if err := os.WriteFile(filepath.Join(profile, "user.js"), []byte(js.String()), 0o600); err != nil {
			return err
		}
		args = []string{"--profile", profile, "--no-remote", "--new-instance", page}
	} else {
		args = []string{"--user-data-dir=" + profile, "--no-first-run", "--no-default-browser-check", "--window-size=900,760", "--app=" + page}
	}
	cmd := exec.Command(exe, args...)
	dieWithParent(cmd)
	if err := startPlain(cmd); err != nil {
		return fmt.Errorf("couldn't open a browser window: %w", err)
	}
	tieToParent(cmd.Process)
	logf(`  Nexus Mods: click "Slow download" in the window that opened (sign in to Nexus there the first time; if the browser asks, let nexusmods.com open ppgmods)`)
	exited := make(chan error, 1)
	started := time.Now()
	go func() { exited <- cmd.Wait() }()
	select {
	case <-cancel:
		cmd.Process.Kill()
		<-exited
		return nil
	case err := <-exited:
		if time.Since(started) < 3*time.Second {
			// Gone at once: it handed the page to a browser already running
			// (nothing for ppgmods to close), or it failed to start.
			if err != nil {
				return fmt.Errorf("the browser stopped at once: %w", err)
			}
			<-cancel
			return nil
		}
		return ErrWindowClosed
	}
}
