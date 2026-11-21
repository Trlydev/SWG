package game

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// BepInEx starts through its winhttp.dll in the game folder (Doorstop).
// Windows loads it on its own; Wine (Proton) loads its own builtin
// winhttp.dll instead unless told to prefer the game's, so under Proton
// BepInEx and RE_PPG install fine and then silently never load. Two ways
// tell Wine: Steam launch options
//
//	WINEDLLOVERRIDES="winhttp=n,b" %command%
//
// or a DLL override in the game's Proton prefix (what winecfg's Libraries
// tab writes).

// LaunchFix is the Steam launch options line that makes Proton load BepInEx.
const LaunchFix = `WINEDLLOVERRIDES="winhttp=n,b" %command%`

// WinHTTP is how People Playground's Proton setup loads winhttp.dll.
type WinHTTP struct {
	Proton        bool   `json:"proton"`                   // the game runs under Proton (it has a compatdata prefix)
	Prefix        string `json:"prefix,omitempty"`         // the Proton prefix (…/compatdata/1118200/pfx)
	LaunchOptions string `json:"launch_options,omitempty"` // Steam launch options for the game, as set
	ByLaunch      bool   `json:"by_launch,omitempty"`      // the launch options prefer the game's winhttp.dll
	ByPrefix      bool   `json:"by_prefix,omitempty"`      // the prefix's DLL overrides do
}

// OK: BepInEx's winhttp.dll loads (always, outside Proton).
func (w WinHTTP) OK() bool { return !w.Proton || w.ByLaunch || w.ByPrefix }

// ProtonPrefix is the game's Proton prefix: a Steam library keeps games in
// steamapps/common and their prefixes in steamapps/compatdata.
func ProtonPrefix(gameDir string) string {
	if gameDir == "" {
		return ""
	}
	steamapps := filepath.Dir(filepath.Dir(filepath.Clean(gameDir)))
	if !strings.EqualFold(filepath.Base(steamapps), "steamapps") {
		return ""
	}
	return filepath.Join(steamapps, "compatdata", AppID, "pfx")
}

// DetectWinHTTP looks at how gameDir's Proton setup loads winhttp.dll.
func DetectWinHTTP(gameDir string) WinHTTP {
	var w WinHTTP
	pfx := ProtonPrefix(gameDir)
	if pfx == "" {
		return w
	}
	if fi, err := os.Stat(filepath.Join(pfx, "user.reg")); err != nil || fi.IsDir() {
		return w
	}
	w.Proton, w.Prefix = true, pfx
	w.ByPrefix = prefixPrefersNative(filepath.Join(pfx, "user.reg"), "winhttp")
	w.LaunchOptions = launchOptions(SteamRoots(), AppID)
	w.ByLaunch = launchPrefersNative(w.LaunchOptions, "winhttp")
	return w
}

// launchPrefersNative reports whether launch options set WINEDLLOVERRIDES
// so dll loads native first (winhttp=n,b, winhttp=native, winhttp,version=n).
func launchPrefersNative(opts, dll string) bool {
	for _, f := range shellFields(opts) {
		v, ok := strings.CutPrefix(f, "WINEDLLOVERRIDES=")
		if ok && overridesPreferNative(v, dll) {
			return true
		}
	}
	return false
}

// overridesPreferNative reads a WINEDLLOVERRIDES value: entries split by
// ';', each "dll[,dll…]=mode" where mode lists n(ative)/b(uiltin) in order.
func overridesPreferNative(v, dll string) bool {
	ok := false
	for _, e := range strings.Split(v, ";") {
		names, mode, found := strings.Cut(e, "=")
		if !found {
			continue
		}
		for _, n := range strings.Split(names, ",") {
			n = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(n)), "*")
			n = strings.TrimSuffix(n, ".dll")
			if n == dll {
				ok = strings.HasPrefix(strings.ToLower(strings.TrimSpace(mode)), "n") // the last entry wins
			}
		}
	}
	return ok
}

// shellFields splits launch options like a shell would, enough for
// VAR="a b" and VAR='a' forms.
func shellFields(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	in := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, in = r, true
		case r == ' ' || r == '\t':
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if in {
		out = append(out, cur.String())
	}
	return out
}

// launchOptions is the game's Steam launch options, from the most recently
// signed-in account's localconfig.vdf (else any account that set some).
func launchOptions(roots []string, appID string) string {
	var any string
	for _, root := range roots {
		recent := recentAccount(root)
		dirs, _ := filepath.Glob(filepath.Join(root, "userdata", "*", "config", "localconfig.vdf"))
		for _, p := range dirs {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			v, err := ParseVDF(string(b))
			if err != nil {
				continue
			}
			apps := v.Find("UserLocalConfigStore", "Software", "Valve", "Steam", "apps", appID)
			if apps == nil {
				apps = v.Find("UserLocalConfigStore", "Software", "Valve", "Steam", "Apps", appID)
			}
			opts := apps.Value("LaunchOptions")
			account := filepath.Base(filepath.Dir(filepath.Dir(p)))
			if recent != "" && account == recent {
				return opts
			}
			if any == "" {
				any = opts
			}
		}
	}
	return any
}

// recentAccount is the userdata folder (32-bit account id) of the account
// loginusers.vdf marks MostRecent.
func recentAccount(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "config", "loginusers.vdf"))
	if err != nil {
		return ""
	}
	v, err := ParseVDF(string(b))
	if err != nil {
		return ""
	}
	users := v.Find("users")
	if users == nil {
		return ""
	}
	// MostRecent marks it in older Steam versions; newer ones only keep a
	// timestamp of each sign-in.
	best, bestAt := "", int64(-1)
	for _, kv := range users.Kids {
		id, err := strconv.ParseUint(kv.Key, 10, 64)
		if kv.Node == nil || err != nil || id < 76561197960265728 {
			continue
		}
		account := strconv.FormatUint(id-76561197960265728, 10)
		if kv.Node.Value("MostRecent") == "1" {
			return account
		}
		if at, _ := strconv.ParseInt(kv.Node.Value("timestamp"), 10, 64); at > bestAt {
			best, bestAt = account, at
		}
	}
	return best
}

// SteamAccountName is the Steam account name to sign steamcmd in with:
// PPGMODS_STEAM_USER, else the account most recently signed in to Steam on
// this PC (loginusers.vdf). "" if none is known.
func SteamAccountName() string {
	if u := os.Getenv("PPGMODS_STEAM_USER"); u != "" {
		return u
	}
	for _, root := range SteamRoots() {
		b, err := os.ReadFile(filepath.Join(root, "config", "loginusers.vdf"))
		if err != nil {
			continue
		}
		v, err := ParseVDF(string(b))
		if err != nil {
			continue
		}
		users := v.Find("users")
		if users == nil {
			continue
		}
		best, bestAt := "", int64(-1)
		for _, kv := range users.Kids {
			if kv.Node == nil {
				continue
			}
			name := kv.Node.Value("AccountName")
			if kv.Node.Value("MostRecent") == "1" && name != "" {
				return name
			}
			if at, _ := strconv.ParseInt(kv.Node.Value("timestamp"), 10, 64); at > bestAt && name != "" {
				best, bestAt = name, at
			}
		}
		if best != "" {
			return best
		}
	}
	return ""
}

// The prefix registry key winecfg's Libraries tab writes.
const dllOverridesKey = `[Software\\Wine\\DllOverrides]`

// prefixPrefersNative reports whether a Wine user.reg overrides dll to
// load native first.
func prefixPrefersNative(userReg, dll string) bool {
	f, err := os.Open(userReg)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	in := false
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "[") {
			in = strings.HasPrefix(line, dllOverridesKey)
			continue
		}
		if !in {
			continue
		}
		if name, mode, ok := regValue(line); ok {
			name = strings.TrimPrefix(strings.ToLower(name), "*")
			if name == dll {
				return strings.HasPrefix(strings.ToLower(mode), "native")
			}
		}
	}
	return false
}

// regValue reads a `"name"="value"` line of a Wine .reg file.
func regValue(line string) (name, value string, ok bool) {
	k, v, found := strings.Cut(line, `"="`)
	if !found || !strings.HasPrefix(k, `"`) || !strings.HasSuffix(v, `"`) {
		return "", "", false
	}
	return k[1:], v[:len(v)-1], true
}

// ErrGameRunning: People Playground (so its wineserver) is running, and it
// would write the prefix's registry back over a change when it exits.
var ErrGameRunning = errors.New("People Playground or its Proton is still running: close the game and try again in a few seconds")

// PreferNativeWinHTTP adds the winhttp DLL override (native, then builtin)
// to the game's Proton prefix, as winecfg would, so Proton loads BepInEx
// without launch options. user.reg is replaced in one rename, with a copy
// of the old one kept next to it.
func PreferNativeWinHTTP(gameDir string, running func() bool) error {
	pfx := ProtonPrefix(gameDir)
	if pfx == "" {
		return errors.New("no Proton prefix found for the game folder")
	}
	if running != nil && running() {
		return ErrGameRunning
	}
	p := filepath.Join(pfx, "user.reg")
	b, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("reading the Proton prefix's registry: %w", err)
	}
	out, changed, err := addOverride(string(b), "winhttp", time.Now())
	if err != nil || !changed {
		return err
	}
	if err := writeNew(p+".ppgmods-backup", b); err != nil {
		return fmt.Errorf("backing up the Proton prefix's registry: %w", err)
	}
	tmp := p + ".ppgmods-tmp"
	if err := writeNew(tmp, []byte(out)); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// writeNew writes a fresh file at p: whatever was there (a stale copy, or a
// link that would redirect the write elsewhere) is removed first, and the
// file is created only if nothing took its place.
func writeNew(p string, b []byte) error {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(p)
	}
	return err
}

// addOverride sets "dll"="native,builtin" in a user.reg's DllOverrides key,
// adding the key when it's missing.
func addOverride(reg, dll string, now time.Time) (string, bool, error) {
	if !strings.HasPrefix(reg, "WINE REGISTRY Version 2") {
		return "", false, errors.New("the Proton prefix's user.reg isn't a Wine registry file")
	}
	nl := "\n"
	if strings.Contains(reg, "\r\n") {
		nl = "\r\n"
	}
	lines := strings.Split(reg, nl)
	want := `"` + dll + `"="native,builtin"`
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, dllOverridesKey) {
			start = i
			break
		}
	}
	if start < 0 {
		ft := (now.Unix() + 11644473600) * 10000000
		key := []string{fmt.Sprintf("%s %d", dllOverridesKey, now.Unix()), fmt.Sprintf("#time=%x", ft), want, ""}
		body := strings.TrimRight(reg, nl) + nl + nl + strings.Join(key, nl)
		return body, true, nil
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "[") || lines[i] == "" {
			end = i
			break
		}
	}
	for i := start + 1; i < end; i++ {
		name, mode, ok := regValue(lines[i])
		if !ok || strings.TrimPrefix(strings.ToLower(name), "*") != dll {
			continue
		}
		if strings.HasPrefix(strings.ToLower(mode), "native") {
			return reg, false, nil
		}
		lines[i] = want
		return strings.Join(lines, nl), true, nil
	}
	// Wine keeps a key's values sorted; any order loads, but keep it tidy.
	at := end
	for i := start + 1; i < end; i++ {
		if name, _, ok := regValue(lines[i]); ok && strings.ToLower(name) > dll {
			at = i
			break
		}
	}
	lines = append(lines[:at], append([]string{want}, lines[at:]...)...)
	return strings.Join(lines, nl), true, nil
}

// PrefixInUse reports whether the game, or any Wine process of its Proton
// prefix (wineserver lingers a few seconds after the game exits, then
// saves the registry), is running. It reads /proc, so it is Linux only;
// elsewhere it reports false.
func PrefixInUse(pfx string) bool {
	procs, _ := filepath.Glob("/proc/[0-9]*")
	want := []string{"WINEPREFIX=" + filepath.Clean(pfx), "STEAM_COMPAT_DATA_PATH=" + filepath.Dir(filepath.Clean(pfx))}
	for _, p := range procs {
		cmd, _ := os.ReadFile(filepath.Join(p, "cmdline"))
		args := strings.Split(string(cmd), "\x00")
		for _, arg := range args {
			// An argument that is the game (Z:\…\People Playground.exe or
			// a Unix path), not one that merely mentions it.
			if i := strings.LastIndexAny(arg, `/\`); strings.EqualFold(arg[i+1:], "People Playground.exe") {
				return true
			}
		}
		// Only Wine's own processes hold the registry: wineserver, wine,
		// and Windows programs (Wine names them C:\…\x.exe). A shell or
		// ppgmods itself started with WINEPREFIX set doesn't.
		i := strings.LastIndexAny(args[0], `/\`)
		name := strings.ToLower(args[0][i+1:])
		if !strings.HasPrefix(name, "wine") && !strings.HasSuffix(name, ".exe") {
			continue
		}
		env, err := os.ReadFile(filepath.Join(p, "environ"))
		if err != nil {
			continue
		}
		for _, kv := range strings.Split(string(env), "\x00") {
			kv = strings.TrimSuffix(kv, "/")
			for _, w := range want {
				if kv == w {
					return true
				}
			}
		}
	}
	return false
}
