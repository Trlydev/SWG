package game

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectLoader(t *testing.T) {
	g := t.TempDir()
	if l := DetectLoader(g); l.BepInEx || l.REPPG {
		t.Fatalf("empty game: %+v", l)
	}
	os.MkdirAll(filepath.Join(g, "BepInEx", "core"), 0o755)
	os.MkdirAll(filepath.Join(g, "BepInEx", "plugins", "RE_PPG"), 0o755)
	os.MkdirAll(filepath.Join(g, "RE_PPG"), 0o755)
	os.WriteFile(filepath.Join(g, "BepInEx", "plugins", "RE_PPG", "RE_PPG.Runtime.dll"), []byte("MZ"), 0o644)
	os.WriteFile(filepath.Join(g, "BepInEx", "plugins", "Other.dll"), []byte("MZ"), 0o644)
	os.WriteFile(filepath.Join(g, "BepInEx", "LogOutput.log"), []byte("[Message:   BepInEx] BepInEx 5.4.23.5 - People Playground\n[Info   :   BepInEx] Loading [RE_PPG 0.2.16]\n"), 0o644)
	l := DetectLoader(g)
	if !l.BepInEx || !l.REPPG || l.BepInExVersion != "5.4.23.5" || l.REPPGVersion != "0.2.16" || len(l.Plugins) != 2 {
		t.Fatalf("%+v", l)
	}
}

// People Playground 1.28 runs natively on Linux, where RE_PPG (Windows
// only) never loads.
func TestDetectNativeLinux(t *testing.T) {
	g := t.TempDir()
	os.WriteFile(filepath.Join(g, "People Playground.x86_64"), []byte("\x7fELF"), 0o755)
	if !DetectLoader(g).NativeLinux {
		t.Error("Linux player not detected")
	}
	os.WriteFile(filepath.Join(g, "UnityPlayer.dll"), []byte("MZ"), 0o644)
	if DetectLoader(g).NativeLinux {
		t.Error("Windows version (Proton) taken for native Linux")
	}
	g = t.TempDir()
	os.WriteFile(filepath.Join(g, "UnityPlayer.so"), []byte("\x7fELF"), 0o644)
	if !DetectLoader(g).NativeLinux {
		t.Error("UnityPlayer.so not taken for native Linux")
	}
}
