package sources

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Steam serves a Workshop item's files (SteamPipe content) only to a
// signed-in account that owns the game; there is no public file link. Valve's
// own command-line client, steamcmd, downloads them for an account it is
// signed in to. The person signs it in once, themselves, in a terminal
// (`steamcmd +login <account>`, with their password and Steam Guard); it
// keeps a login token, and ppgmods then runs it with password prompts off.
// ppgmods never sees, asks for or stores the password.

// ErrSteamCmdLogin means steamcmd has no saved login for the account.
var ErrSteamCmdLogin = errors.New("steamcmd isn't signed in")

// ErrNoSteamCmd means steamcmd isn't installed (or not where ppgmods looks).
var ErrNoSteamCmd = errors.New("steamcmd isn't installed")

// SteamCmdPath finds steamcmd: PPGMODS_STEAMCMD, then PATH, then where its
// installers put it.
func SteamCmdPath() string {
	if p := os.Getenv("PPGMODS_STEAMCMD"); p != "" {
		return p
	}
	for _, n := range []string{"steamcmd", "steamcmd.sh"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	home, _ := os.UserHomeDir()
	var cands []string
	if runtime.GOOS == "windows" {
		cands = []string{
			filepath.Join(os.Getenv("LocalAppData"), "steamcmd", "steamcmd.exe"),
			`C:\steamcmd\steamcmd.exe`,
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Steam", "steamcmd.exe"),
		}
	} else {
		cands = []string{
			filepath.Join(home, ".steam", "steamcmd", "steamcmd.sh"),
			filepath.Join(home, "Steam", "steamcmd.sh"),
			filepath.Join(home, "steamcmd", "steamcmd.sh"),
			"/usr/games/steamcmd",
		}
	}
	for _, p := range cands {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

var (
	steamCmdMu sync.Mutex // one steamcmd at a time (it locks its own files)

	reSteamCmdOK   = regexp.MustCompile(`Success\. Downloaded item (\d+) to "([^"]+)"`)
	reSteamCmdFail = regexp.MustCompile(`ERROR! Download item (\d+) failed \(([^)]*)\)`)
	reSteamUser    = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,64}$`)
)

// SteamCmdDownload downloads Workshop item ws of appID with steamcmd,
// signed in as user, into dest (steamcmd's install folder: the item lands in
// dest/steamapps/workshop/content/<app>/<ws>, never in the Steam client's
// own folders), and returns the item's folder.
func SteamCmdDownload(ctx context.Context, exe, user, dest string, appID int, ws string) (string, error) {
	if exe == "" {
		return "", ErrNoSteamCmd
	}
	if !reSteamUser.MatchString(user) {
		return "", errors.New("no Steam account name to sign steamcmd in with")
	}
	if !reDigits.MatchString(ws) {
		return "", fmt.Errorf("not a Workshop item id: %q", ws)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	steamCmdMu.Lock()
	defer steamCmdMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe,
		"+@ShutdownOnFailedCommand", "1", "+@NoPromptForPassword", "1",
		"+force_install_dir", dest,
		"+login", user,
		"+workshop_download_item", fmt.Sprint(appID), ws,
		"+quit")
	cmd.Stdin = nil // never a prompt
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	text := out.String()
	switch {
	case strings.Contains(text, "Cached credentials not found") || strings.Contains(text, "No cached credentials"):
		return "", ErrSteamCmdLogin
	case reSteamCmdOK.MatchString(text):
		m := reSteamCmdOK.FindStringSubmatch(text)
		dir := m[2]
		want := filepath.Join(dest, "steamapps", "workshop", "content", fmt.Sprint(appID), ws)
		if filepath.Clean(dir) != filepath.Clean(want) {
			// Where it said it put it, but only inside dest.
			if rel, err := filepath.Rel(dest, dir); err != nil || strings.HasPrefix(rel, "..") {
				return "", fmt.Errorf("steamcmd put the item outside ppgmods' folder (%s)", dir)
			}
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return "", fmt.Errorf("steamcmd said it downloaded the item, but %s isn't there", dir)
		}
		return dir, nil
	case reSteamCmdFail.MatchString(text):
		return "", fmt.Errorf("steamcmd couldn't download it (%s)", reSteamCmdFail.FindStringSubmatch(text)[2])
	case ctx.Err() != nil:
		return "", errors.New("steamcmd took too long")
	case err != nil:
		return "", fmt.Errorf("steamcmd: %v (%s)", err, lastLine(text))
	}
	return "", fmt.Errorf("steamcmd didn't report a download (%s)", lastLine(text))
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(s, "\x1b[0m", "")), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			if len(l) > 160 {
				l = l[:160]
			}
			return l
		}
	}
	return "no output"
}
