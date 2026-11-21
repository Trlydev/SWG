package game

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLaunchPrefersNative(t *testing.T) {
	cases := map[string]bool{
		`WINEDLLOVERRIDES="winhttp=n,b" %command%`:                      true,
		`WINEDLLOVERRIDES='winhttp=native,builtin' %command%`:           true,
		`WINEDLLOVERRIDES=winhttp=n %command% -foo`:                     true,
		`PROTON_LOG=1 WINEDLLOVERRIDES="version,winhttp=n,b" %command%`: true,
		`WINEDLLOVERRIDES="d3d11=n;winhttp.dll=n,b" %command%`:          true,
		`WINEDLLOVERRIDES="winhttp=b,n" %command%`:                      false,
		`WINEDLLOVERRIDES="winhttp=n;winhttp=b" %command%`:              false, // the last one wins
		`WINEDLLOVERRIDES="winhttpx=n,b" %command%`:                     false,
		`%command% WINEDLLOVERRIDES`:                                    false,
		``:                                                              false,
		`gamemoderun %command%`:                                         false,
	}
	for opts, want := range cases {
		if got := launchPrefersNative(opts, "winhttp"); got != want {
			t.Errorf("%q: %v", opts, got)
		}
	}
}

func TestParseVDF(t *testing.T) {
	v, err := ParseVDF(`// comment
"Root"
{
	"apps" { "1118200" { "LaunchOptions"		"WINEDLLOVERRIDES=\"winhttp=n,b\" %command%" "path" "C:\\Games" } }
	bare value
}`)
	if err != nil {
		t.Fatal(err)
	}
	app := v.Find("root", "Apps", "1118200")
	if got := app.Value("launchoptions"); got != `WINEDLLOVERRIDES="winhttp=n,b" %command%` {
		t.Errorf("launch options: %q", got)
	}
	if got := app.Value("path"); got != `C:\Games` {
		t.Errorf("path: %q", got)
	}
	if v.Find("Root").Value("bare") != "value" {
		t.Error("bare words")
	}
	if v.Find("Root", "nope", "x") != nil || v.Find("Root", "nope").Value("x") != "" {
		t.Error("missing keys")
	}
	for _, bad := range []string{`"a" {`, `"a" "b" }`, `"a"`, `"a" "b`, `{ }`, strings.Repeat(`"a" {`, 100)} {
		if _, err := ParseVDF(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

const userReg = "WINE REGISTRY Version 2\n;; All keys relative to REGISTRY\\\\User\\\\S-1\n\n#arch=win64\n\n" +
	"[Control Panel\\\\Desktop] 1789520095\n#time=1dd4575fd9281c2\n\"Wallpaper\"=\"\"\n\n" +
	"[Software\\\\Wine\\\\DllOverrides] 1789520095\n#time=1dd4575fd9281c2\n\"atl100\"=\"native,builtin\"\n\"xaudio2_7\"=\"native,builtin\"\n\n" +
	"[Software\\\\Wine\\\\Fonts] 1789520095\n"

func writeReg(t *testing.T, dir, reg string) string {
	p := filepath.Join(dir, "user.reg")
	os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(p, []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAddOverride(t *testing.T) {
	now := time.Unix(1795213358, 0)
	out, changed, err := addOverride(userReg, "winhttp", now)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if !strings.Contains(out, "\"atl100\"=\"native,builtin\"\n\"winhttp\"=\"native,builtin\"\n\"xaudio2_7\"") {
		t.Errorf("not added in order:\n%s", out)
	}
	if strings.Count(out, "winhttp") != 1 || !strings.HasSuffix(out, "[Software\\\\Wine\\\\Fonts] 1789520095\n") {
		t.Errorf("rest of the file changed:\n%s", out)
	}
	p := writeReg(t, t.TempDir(), out)
	if !prefixPrefersNative(p, "winhttp") || prefixPrefersNative(p, "version") {
		t.Error("prefixPrefersNative")
	}
	if _, changed, _ := addOverride(out, "winhttp", now); changed {
		t.Error("changed again")
	}

	// builtin first is switched; the value isn't duplicated.
	b := strings.Replace(userReg, "\"xaudio2_7\"", "\"winhttp\"=\"builtin\"\n\"xaudio2_7\"", 1)
	out, changed, _ = addOverride(b, "winhttp", now)
	if !changed || strings.Count(out, "winhttp") != 1 || !strings.Contains(out, "\"winhttp\"=\"native,builtin\"") {
		t.Errorf("builtin not switched:\n%s", out)
	}

	// No DllOverrides key yet; CRLF kept.
	noKey := "WINE REGISTRY Version 2\r\n\r\n[Control Panel\\\\Desktop] 1\r\n\"Wallpaper\"=\"\"\r\n"
	out, changed, _ = addOverride(noKey, "winhttp", now)
	if !changed || !strings.Contains(out, "\r\n[Software\\\\Wine\\\\DllOverrides] 1795213358\r\n#time=") || strings.Contains(strings.ReplaceAll(out, "\r\n", ""), "\n") {
		t.Errorf("key not added:\n%q", out)
	}
	p = writeReg(t, t.TempDir(), out)
	if !prefixPrefersNative(p, "winhttp") {
		t.Error("added key not read back")
	}

	if _, _, err := addOverride("not a registry", "winhttp", now); err == nil {
		t.Error("changed a file that isn't a Wine registry")
	}
}

// fakeSteam makes a Steam root with the game in its library and a prefix.
func fakeSteam(t *testing.T, launch string) (root, gameDir string) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root = filepath.Join(home, ".local", "share", "Steam")
	gameDir = filepath.Join(root, "steamapps", "common", "People Playground")
	os.MkdirAll(gameDir, 0o755)
	writeReg(t, filepath.Join(root, "steamapps", "compatdata", AppID, "pfx"), userReg)
	os.MkdirAll(filepath.Join(root, "config"), 0o755)
	os.WriteFile(filepath.Join(root, "config", "loginusers.vdf"), []byte(`"users" {
		"76561199094548100" { "AccountName" "a" "timestamp" "1795211741" }
		"76561197960265729" { "AccountName" "b" "timestamp" "1695211741" }
	}`), 0o644)
	cfg := func(account, opts string) {
		d := filepath.Join(root, "userdata", account, "config")
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "localconfig.vdf"), []byte(`"UserLocalConfigStore" { "Software" { "Valve" { "Steam" { "apps" { "1118200" { "LaunchOptions" "`+strings.ReplaceAll(opts, `"`, `\"`)+`" } } } } } }`), 0o644)
	}
	cfg("1", `WINEDLLOVERRIDES="winhttp=n,b" %command%`) // another, older account
	cfg("1134282372", launch)
	return root, gameDir
}

func TestDetectWinHTTP(t *testing.T) {
	_, g := fakeSteam(t, "gamemoderun %command%")
	w := DetectWinHTTP(g)
	if !w.Proton || w.ByLaunch || w.ByPrefix || w.OK() || w.LaunchOptions != "gamemoderun %command%" {
		t.Errorf("without the override (the recent account's options count): %+v", w)
	}
	if err := PreferNativeWinHTTP(g, func() bool { return true }); err != ErrGameRunning {
		t.Errorf("game running: %v", err)
	}
	// A link planted at the backup's name doesn't redirect the write.
	outside := filepath.Join(t.TempDir(), "elsewhere")
	os.WriteFile(outside, []byte("keep"), 0o644)
	os.Symlink(outside, filepath.Join(w.Prefix, "user.reg.ppgmods-backup"))
	if err := PreferNativeWinHTTP(g, func() bool { return false }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "keep" {
		t.Error("the backup was written through a link")
	}
	if w := DetectWinHTTP(g); !w.ByPrefix || !w.OK() {
		t.Errorf("after the fix: %+v", w)
	}
	if b, _ := os.ReadFile(filepath.Join(w.Prefix, "user.reg.ppgmods-backup")); string(b) != userReg {
		t.Error("no backup of the old registry")
	}

	_, g = fakeSteam(t, `WINEDLLOVERRIDES="winhttp=n,b" %command%`)
	if w := DetectWinHTTP(g); !w.ByLaunch || !w.OK() {
		t.Errorf("launch options: %+v", w)
	}

	// Outside a Steam library (Windows, or a copied folder): not Proton.
	if w := DetectWinHTTP(t.TempDir()); w.Proton || !w.OK() {
		t.Errorf("no prefix: %+v", w)
	}
}

// A process with the prefix in its environment counts as using it.
// TestMain lets the test binary stand in for a long-running process
// (TestPrefixInUse starts copies of it named like Wine's).
func TestMain(m *testing.M) {
	if os.Getenv("PPGMODS_TEST_SLEEP") == "1" {
		time.Sleep(10 * time.Second)
		return
	}
	os.Exit(m.Run())
}

func TestPrefixInUse(t *testing.T) {
	if _, err := os.Stat("/proc/self/environ"); err != nil {
		t.Skip("no /proc")
	}
	pfx := filepath.Join(t.TempDir(), "compatdata", AppID, "pfx")
	if PrefixInUse(pfx) {
		t.Fatal("unused prefix in use")
	}
	t.Setenv("WINEPREFIX", pfx+"/")
	self, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Skip(err)
	}
	start := func(name string) {
		// A copy of this test binary, named like a Wine process, that just
		// waits (TestMain). Not sleep: some systems' coreutils is one
		// program that picks the tool by its name.
		bin := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(bin, b, 0o755); err != nil {
			t.Skip(err)
		}
		cmd := exec.Command(bin)
		cmd.Env = append(os.Environ(), "PPGMODS_TEST_SLEEP=1")
		if err := cmd.Start(); err != nil {
			t.Skip(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
		// Right after the start, /proc can still show the parent's command
		// line (before the new program replaced it): wait for the name.
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			line, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", cmd.Process.Pid))
			if strings.HasPrefix(string(line), bin+"\x00") {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("%s didn't start", name)
	}
	// This test (like a shell) has WINEPREFIX set but isn't Wine.
	start("not-wine")
	if PrefixInUse(pfx) {
		t.Error("a process that isn't Wine counted")
	}
	start("wineserver")
	if !PrefixInUse(pfx) {
		t.Error("a wineserver with WINEPREFIX set to it isn't seen")
	}
}

func TestSteamAccountName(t *testing.T) {
	fakeSteam(t, "")
	t.Setenv("PPGMODS_STEAM_USER", "")
	if got := SteamAccountName(); got != "a" { // the newest sign-in
		t.Errorf("account name %q", got)
	}
	t.Setenv("PPGMODS_STEAM_USER", "chosen")
	if got := SteamAccountName(); got != "chosen" {
		t.Errorf("override: %q", got)
	}
}

func TestSteamIDOf(t *testing.T) {
	fakeSteam(t, "")
	if got := SteamIDOf("A"); got != "76561199094548100" {
		t.Errorf("SteamID of a: %q", got)
	}
	if got := SteamIDOf("nobody"); got != "" {
		t.Errorf("unknown account: %q", got)
	}
}
