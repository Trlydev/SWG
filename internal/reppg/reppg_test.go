package reppg

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RE_PPG's real update.json for v0.2.31 verifies with the key ppgmods carries;
// a changed byte doesn't.
func TestRealManifest(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "update-v0.2.31.json"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseSigned(b)
	if err != nil || m.Version != "0.2.31" || len(m.Files) == 0 {
		t.Fatalf("%+v %v", m, err)
	}
	var env map[string]string
	json.Unmarshal(b, &env)
	p, _ := base64.StdEncoding.DecodeString(env["Payload"])
	p = bytes.Replace(p, []byte(`"0.2.31"`), []byte(`"9.9.99"`), 1)
	env["Payload"] = base64.StdEncoding.EncodeToString(p)
	bad, _ := json.Marshal(env)
	if _, err := ParseSigned(bad); err == nil {
		t.Error("a changed manifest passed")
	}
}

type fakeRelease struct {
	key         *rsa.PrivateKey
	files       map[string][]byte // what the manifest lists
	extra       map[string][]byte // in the zip, not listed
	tamper      string            // listed file whose zip content differs
	bep         []byte
	signWith    *rsa.PrivateKey
	baseVersion string // the base manifest's version, if not 1.2.3
}

func sign(t *testing.T, key *rsa.PrivateKey, v any) []byte {
	payload, _ := json.Marshal(v)
	h := sha256.Sum256(payload)
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]string{"Payload": base64.StdEncoding.EncodeToString(payload), "Signature": base64.StdEncoding.EncodeToString(sig)})
	return b
}

func (fr *fakeRelease) serve(t *testing.T) *httptest.Server {
	signer := fr.key
	if fr.signWith != nil {
		signer = fr.signWith
	}
	var files []File
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	files0 := fr.files
	if fr.baseVersion != "" { // the package's own seed/update.json, with this version
		fr.files = map[string][]byte{}
		for k, v := range files0 {
			fr.files[k] = v
		}
		fr.files["RE_PPG/bootstrap/seed/update.json"] = sign(t, signer, Manifest{Product: "RE_PPG", Version: fr.baseVersion})
	}
	for p, c := range fr.files {
		h := sha256.Sum256(c)
		files = append(files, File{Path: p, Size: int64(len(c)), Sha256: hex.EncodeToString(h[:])})
		if p == fr.tamper {
			c = append([]byte("x"), c[1:]...)
		}
		w, _ := zw.Create(strings.ReplaceAll(p, "/", `\`)) // RE_PPG's zips use backslashes
		w.Write(c)
	}
	for p, c := range fr.extra {
		w, _ := zw.Create(p)
		w.Write(c)
	}
	w, _ := zw.Create("base-manifest.json")
	w.Write(sign(t, signer, Manifest{Product: "RE_PPG-base", Files: files})) // like RE_PPG's: no version
	zw.Close()
	update := sign(t, signer, Manifest{Product: "RE_PPG", Version: "1.2.3"})
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest/download/update.json":
			w.Write(update)
		case "/download/v1.2.3/RE_PPG-base.zip":
			w.Write(zb.Bytes())
		case "/bepinex.zip":
			w.Write(fr.bep)
		default:
			http.NotFound(w, r)
		}
	}))
}

// BepInEx files of the fake release, and RE_PPG's Harmony files matching them.
var bepFiles = append([]string(nil), bepInExFiles...)

// fakeBepInEx puts a complete (fake) BepInEx in g, with someone's files.
func fakeBepInEx(g string, harmony string) {
	for _, f := range bepFiles {
		p := filepath.Join(g, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("bep:"+f), 0o644)
	}
	if harmony != "" {
		os.WriteFile(filepath.Join(g, "BepInEx", "core", "0Harmony.dll"), []byte(harmony), 0o644)
	}
}

func bepZip() []byte {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, p := range bepFiles {
		w, _ := zw.Create(p)
		w.Write([]byte("bep:" + p))
	}
	zw.Close()
	return b.Bytes()
}

func hexSHA(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// withHarmony adds RE_PPG's Harmony files to a fake base package.
func withHarmony(files map[string][]byte) map[string][]byte {
	out := map[string][]byte{}
	for k, v := range files {
		out[k] = v
	}
	patched := []byte("patched harmony")
	compat, _ := json.Marshal(map[string]any{
		"OriginalSha256": hexSHA([]byte("bep:BepInEx/core/0Harmony.dll")),
		"PatchedSha256":  hexSHA(patched),
		"Dependencies":   map[string]string{"MonoMod.Utils.dll": hexSHA([]byte("bep:BepInEx/core/MonoMod.Utils.dll"))},
	})
	out["RE_PPG/bootstrap/harmony/compatibility.json"] = append([]byte("\xef\xbb\xbf"), compat...) // RE_PPG's has a BOM
	out["RE_PPG/bootstrap/harmony/0Harmony.dll"] = patched
	out["RE_PPG/bootstrap/legacy-harmony/0Harmony.dll"] = []byte("legacy harmony")
	return out
}

func setup(t *testing.T, fr *fakeRelease) string {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	fr.key = key
	if fr.bep == nil {
		fr.bep = bepZip()
	}
	srv := fr.serve(t)
	t.Cleanup(srv.Close)
	h := sha256.Sum256(fr.bep)
	oldKey, oldRel, oldURL, oldSHA, oldLegacy := publisherKey, releases, bepInExURL, bepInExSHA256, legacyHarmonySHA256
	publisherKey, releases, bepInExURL, bepInExSHA256 = &key.PublicKey, srv.URL, srv.URL+"/bepinex.zip", hex.EncodeToString(h[:])
	legacyHarmonySHA256 = hexSHA([]byte("legacy harmony"))
	t.Cleanup(func() {
		publisherKey, releases, bepInExURL, bepInExSHA256, legacyHarmonySHA256 = oldKey, oldRel, oldURL, oldSHA, oldLegacy
	})
	g := t.TempDir()
	os.WriteFile(filepath.Join(g, "UnityPlayer.dll"), []byte("MZ"), 0o644)
	return g
}

var baseFiles = withHarmony(map[string][]byte{
	"BepInEx/plugins/RE_PPG/RE_PPG.Loader.dll": []byte("loader"),
	"RE_PPG/bootstrap/seed/RE_PPG.Runtime.dll": []byte("runtime"),
})

func TestInstall(t *testing.T) {
	g := setup(t, &fakeRelease{files: baseFiles, extra: map[string][]byte{"RE_PPG/unlisted.dll": []byte("not signed")}})
	v, err := Install(context.Background(), g, t.Logf)
	if err != nil || v != "1.2.3" {
		t.Fatalf("%q %v", v, err)
	}
	for p, c := range baseFiles {
		if b, _ := os.ReadFile(filepath.Join(g, filepath.FromSlash(p))); !bytes.Equal(b, c) {
			t.Errorf("%s: %q", p, b)
		}
	}
	if _, err := os.Stat(filepath.Join(g, "BepInEx", "core", "BepInEx.dll")); err != nil {
		t.Error("BepInEx not installed")
	}
	if _, err := os.Stat(filepath.Join(g, "RE_PPG", "unlisted.dll")); err == nil {
		t.Error("a file the signed list doesn't name was written")
	}
	// Harmony, as RE_PPG's installer sets it up.
	if b, _ := os.ReadFile(filepath.Join(g, "BepInEx", "core", "0Harmony.dll")); string(b) != "patched harmony" {
		t.Errorf("BepInEx's Harmony not replaced: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(g, "People Playground_Data", "Managed", "0Harmony.dll")); string(b) != "legacy harmony" {
		t.Errorf("no Harmony for older mods: %q", b)
	}
	// Installing again keeps working (BepInEx's Harmony is RE_PPG's now).
	if _, err := Install(context.Background(), g, nil); err != nil {
		t.Errorf("second install: %v", err)
	}
}

// RE_PPG's Harmony goes only over the BepInEx Harmony it was made for, and a
// Managed/0Harmony.dll that isn't Harmony is left alone (and stops it).
func TestHarmonyRefuses(t *testing.T) {
	g := setup(t, &fakeRelease{files: baseFiles})
	fakeBepInEx(g, "another Harmony")
	if _, err := Install(context.Background(), g, nil); err == nil || !strings.Contains(err.Error(), "5.4.23.5") {
		t.Errorf("other BepInEx Harmony: %v", err)
	}
	// Refused before anything was written (no half-installed RE_PPG).
	if _, err := os.Stat(filepath.Join(g, "BepInEx", "plugins", "RE_PPG", "RE_PPG.Loader.dll")); err == nil {
		t.Error("RE_PPG files written although the Harmony check failed")
	}
	if b, _ := os.ReadFile(filepath.Join(g, "BepInEx", "core", "0Harmony.dll")); string(b) != "another Harmony" {
		t.Error("replaced a Harmony RE_PPG wasn't made for")
	}

	g = setup(t, &fakeRelease{files: baseFiles})
	managed := filepath.Join(g, "People Playground_Data", "Managed")
	os.MkdirAll(managed, 0o755)
	os.WriteFile(filepath.Join(managed, "0Harmony.dll"), []byte("not a DLL"), 0o644)
	if _, err := Install(context.Background(), g, nil); err == nil || !strings.Contains(err.Error(), "isn't Harmony") {
		t.Errorf("Managed/0Harmony.dll that isn't Harmony: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(managed, "0Harmony.dll")); string(b) != "not a DLL" {
		t.Error("the game's Managed/0Harmony.dll was replaced")
	}
}

// Nothing is written when one file doesn't match, the signature is someone
// else's, BepInEx's download changed, or the game is the native Linux one.
func TestInstallRefuses(t *testing.T) {
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	cases := map[string]*fakeRelease{
		"tampered file":   {files: baseFiles, tamper: "RE_PPG/bootstrap/seed/RE_PPG.Runtime.dll"},
		"other signer":    {files: baseFiles, signWith: other},
		"escaping path":   {files: map[string][]byte{"../evil.dll": []byte("x")}},
		"outside RE_PPG/": {files: map[string][]byte{"winhttp.dll": []byte("x")}},
	}
	for name, fr := range cases {
		g := setup(t, fr)
		fakeBepInEx(g, "")
		if _, err := Install(context.Background(), g, nil); err == nil {
			t.Errorf("%s: installed", name)
		}
		if _, err := os.Stat(filepath.Join(g, "BepInEx", "plugins", "RE_PPG", "RE_PPG.Loader.dll")); err == nil {
			t.Errorf("%s: files were written", name)
		}
	}

	fr := &fakeRelease{files: baseFiles}
	g := setup(t, fr)
	bepInExSHA256 = strings.Repeat("0", 64)
	if _, err := Install(context.Background(), g, nil); err == nil || !strings.Contains(err.Error(), "BepInEx") {
		t.Errorf("changed BepInEx download: %v", err)
	}

	g = setup(t, &fakeRelease{files: baseFiles})
	os.Remove(filepath.Join(g, "UnityPlayer.dll"))
	os.WriteFile(filepath.Join(g, "UnityPlayer.so"), []byte("\x7fELF"), 0o644)
	if _, err := Install(context.Background(), g, nil); err == nil || !strings.Contains(err.Error(), "Proton") {
		t.Errorf("native Linux: %v", err)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"0.2.31", "0.2.30", true}, {"0.2.31", "0.2.31", false}, {"0.2.9", "0.2.10", false}, {"0.3", "0.2.99", true}, {"0.2.31", "", true}, {"", "", false}} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// Findings of the security review: a half-installed BepInEx is repaired,
// Harmony files must come from the signed list, the signed version must be
// the latest, written files are readable, and symlinks can't lead out.
func TestInstallReview(t *testing.T) {
	// A BepInEx file missing (an earlier extraction stopped halfway).
	g := setup(t, &fakeRelease{files: baseFiles})
	fakeBepInEx(g, "")
	os.Remove(filepath.Join(g, "winhttp.dll"))
	if _, err := Install(context.Background(), g, nil); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if _, err := os.Stat(filepath.Join(g, "winhttp.dll")); err != nil {
		t.Error("missing BepInEx file not put back")
	}
	if fi, err := os.Stat(filepath.Join(g, "BepInEx", "plugins", "RE_PPG", "RE_PPG.Loader.dll")); err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("written file mode: %v %v", fi, err)
	}

	// Harmony files planted in the folder but not in the signed list: unused.
	noHarmony := map[string][]byte{"BepInEx/plugins/RE_PPG/RE_PPG.Loader.dll": []byte("loader")}
	g = setup(t, &fakeRelease{files: noHarmony})
	fakeBepInEx(g, "")
	boot := filepath.Join(g, "RE_PPG", "bootstrap", "harmony")
	os.MkdirAll(boot, 0o755)
	os.WriteFile(filepath.Join(boot, "0Harmony.dll"), []byte("planted"), 0o644)
	compat, _ := json.Marshal(map[string]any{"OriginalSha256": hexSHA([]byte("bep:BepInEx/core/0Harmony.dll")), "PatchedSha256": hexSHA([]byte("planted"))})
	os.WriteFile(filepath.Join(boot, "compatibility.json"), compat, 0o644)
	if _, err := Install(context.Background(), g, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(g, "BepInEx", "core", "0Harmony.dll")); string(b) == "planted" {
		t.Error("used a Harmony the signed list doesn't vouch for")
	}

	// A symlinked folder that points outside the game folder.
	g = setup(t, &fakeRelease{files: baseFiles})
	fakeBepInEx(g, "")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(g, "RE_PPG")); err == nil {
		if _, err := Install(context.Background(), g, nil); err == nil {
			t.Error("installed through a symlink leading out of the game folder")
		}
		if ents, _ := os.ReadDir(outside); len(ents) > 0 {
			t.Errorf("wrote outside the game folder: %v", ents)
		}
	}
}

func TestInstallVersionMismatch(t *testing.T) {
	fr := &fakeRelease{files: baseFiles, baseVersion: "1.2.2"}
	g := setup(t, fr)
	fakeBepInEx(g, "")
	if _, err := Install(context.Background(), g, nil); err == nil || !strings.Contains(err.Error(), "not the latest") {
		t.Errorf("older signed base zip: %v", err)
	}
}
