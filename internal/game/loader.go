package game

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Loader describes how C# mods can run in an installed game. People
// Playground 1.27 removed the game's C# mod compiler (after the Workshop
// worms); RE_PPG (github.com/AlibardaWasTaken/RE_PPG) brings it back
// through BepInEx. Without it, script mods do nothing on 1.27 and later.
type Loader struct {
	BepInEx        bool     `json:"bepinex"`
	BepInExVersion string   `json:"bepinex_version,omitempty"`
	REPPG          bool     `json:"re_ppg"`
	REPPGVersion   string   `json:"re_ppg_version,omitempty"`
	REPPGDisabled  bool     `json:"re_ppg_disabled,omitempty"` // RE_PPG/disable-runtime.flag
	Plugins        []string `json:"plugins,omitempty"`         // DLLs under BepInEx/plugins (relative)
	Patchers       []string `json:"patchers,omitempty"`        // DLLs under BepInEx/patchers
	// NativeLinux: the game folder holds the native Linux version (1.28
	// and later). RE_PPG only works in the Windows version, so it never
	// loads there; Steam installs the Windows version when Proton is forced.
	NativeLinux bool `json:"native_linux,omitempty"`
}

var (
	reBepInExVer = regexp.MustCompile(`BepInEx (\d+\.\d+\.\d+(?:\.\d+)?)`)
	reREPPGVer   = regexp.MustCompile(`(?i)RE_PPG[^\r\n]{0,40}?\bv?(\d+\.\d+\.\d+)`)
)

// DetectLoader looks at a game folder.
func DetectLoader(gameDir string) Loader {
	var l Loader
	if gameDir == "" {
		return l
	}
	l.NativeLinux = isNativeLinux(gameDir)
	bep := filepath.Join(gameDir, "BepInEx")
	if fi, err := os.Stat(filepath.Join(bep, "core")); err == nil && fi.IsDir() {
		l.BepInEx = true
	}
	if fi, err := os.Stat(filepath.Join(gameDir, "RE_PPG")); err == nil && fi.IsDir() {
		l.REPPG = true
		if _, err := os.Stat(filepath.Join(gameDir, "RE_PPG", "disable-runtime.flag")); err == nil {
			l.REPPGDisabled = true
		}
	}
	l.Plugins = DLLsUnder(filepath.Join(bep, "plugins"))
	l.Patchers = DLLsUnder(filepath.Join(bep, "patchers"))
	for _, p := range l.Plugins {
		if strings.Contains(strings.ToUpper(p), "RE_PPG") {
			l.REPPG = true
		}
	}
	// Versions from BepInEx's log of the last run.
	if f, err := os.Open(filepath.Join(bep, "LogOutput.log")); err == nil {
		sc := bufio.NewScanner(io.LimitReader(f, 2<<20))
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if l.BepInExVersion == "" {
				if m := reBepInExVer.FindStringSubmatch(line); m != nil {
					l.BepInExVersion = m[1]
				}
			}
			if l.REPPGVersion == "" {
				if m := reREPPGVer.FindStringSubmatch(line); m != nil {
					l.REPPGVersion = m[1]
				}
			}
			if l.BepInExVersion != "" && l.REPPGVersion != "" {
				break
			}
		}
		f.Close()
	}
	return l
}

// isNativeLinux reports whether gameDir holds the native Linux version: a
// Linux Unity player and no Windows one, or both (Steam can leave the other
// version's files behind when it switches) with the Linux one newer.
func isNativeLinux(gameDir string) bool {
	newest := func(names ...string) (t time.Time, ok bool) {
		for _, n := range names {
			ms, _ := filepath.Glob(filepath.Join(gameDir, n))
			for _, m := range ms {
				if fi, err := os.Stat(m); err == nil {
					ok = true
					if fi.ModTime().After(t) {
						t = fi.ModTime()
					}
				}
			}
		}
		return t, ok
	}
	lin, isLin := newest("UnityPlayer.so", "*.x86_64")
	win, isWin := newest("UnityPlayer.dll", "*.exe")
	return isLin && (!isWin || lin.After(win))
}

// DLLsUnder lists .dll files below dir (relative paths, sorted).
func DLLsUnder(dir string) []string {
	var out []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".dll") {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}
