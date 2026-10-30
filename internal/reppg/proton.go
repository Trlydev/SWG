package reppg

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Trlydev/SWG/internal/game"
)

// Under Proton the game is the Windows version running in Wine, and BepInEx
// starts through winhttp.dll in the game folder. Wine loads its own
// winhttp.dll instead unless it is told to prefer the one next to the game
// (a DLL override, what protontricks or winecfg would set). ppgmods writes
// that override into the game's Wine prefix, so nothing else is needed.

// Proton results.
const (
	ProtonSet      = "set"       // the override was added
	ProtonAlready  = "already"   // it was there
	ProtonNoPrefix = "no-prefix" // the game hasn't run under Proton yet
)

// LaunchOption is the Steam launch option that does the same as the
// override, for when the prefix can't be changed.
const LaunchOption = `WINEDLLOVERRIDES="winhttp=n,b" %command%`

// ProtonPrefix is the game's Wine prefix (steamapps/compatdata/<id>/pfx).
func ProtonPrefix(gameDir string) string {
	steamapps := filepath.Dir(filepath.Dir(gameDir))
	return filepath.Join(steamapps, "compatdata", game.AppID, "pfx")
}

const dllOverrides = `[Software\\Wine\\DllOverrides]`

// setWinhttpOverride makes Wine in prefix load the game folder's
// winhttp.dll. user.reg is backed up once before the first change.
func setWinhttpOverride(prefix string) (string, error) {
	reg := filepath.Join(prefix, "user.reg")
	b, err := os.ReadFile(reg)
	if errors.Is(err, os.ErrNotExist) {
		return ProtonNoPrefix, nil
	}
	if err != nil {
		return "", err
	}
	out, changed := withWinhttpOverride(b, time.Now())
	if !changed {
		return ProtonAlready, nil
	}
	if _, err := os.Stat(reg + ".ppgmods-backup"); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(reg+".ppgmods-backup", b, 0o644); err != nil {
			return "", err
		}
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(reg); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp := reg + ".ppgmods-tmp"
	if err := os.WriteFile(tmp, out, mode); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, reg); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return ProtonSet, nil
}

// withWinhttpOverride returns user.reg with "winhttp"="native,builtin" in
// the DllOverrides key, and whether that changed anything.
func withWinhttpOverride(reg []byte, now time.Time) ([]byte, bool) {
	nl := "\n"
	if bytes.Contains(reg, []byte("\r\n")) {
		nl = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(string(reg), "\r\n", "\n"), "\n")
	const want = `"winhttp"="native,builtin"`
	for i, l := range lines {
		if !strings.HasPrefix(strings.ToLower(l), strings.ToLower(dllOverrides)) {
			continue
		}
		insert := i + 1
		for j := i + 1; j < len(lines) && !strings.HasPrefix(lines[j], "["); j++ {
			v := strings.ToLower(strings.TrimSpace(lines[j]))
			if strings.HasPrefix(v, "#") {
				insert = j + 1 // after #time=
				continue
			}
			if strings.HasPrefix(v, `"winhttp"=`) {
				if v == `"winhttp"="native,builtin"` || v == `"winhttp"="n,b"` || v == `"winhttp"="native"` {
					return reg, false
				}
				lines[j] = want
				return []byte(strings.Join(lines, nl)), true
			}
		}
		lines = append(lines[:insert], append([]string{want}, lines[insert:]...)...)
		return []byte(strings.Join(lines, nl)), true
	}
	s := strings.TrimRight(strings.Join(lines, nl), nl)
	s += nl + nl + fmt.Sprintf("%s %d", dllOverrides, now.Unix()) + nl + want + nl
	return []byte(s), true
}
