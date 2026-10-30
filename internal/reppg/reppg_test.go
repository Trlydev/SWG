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
	"runtime"
	"strings"
	"testing"
	"time"
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
	key      *rsa.PrivateKey
	files    map[string][]byte // what the manifest lists
	extra    map[string][]byte // in the zip, not listed
	tamper   string            // listed file whose zip content differs
	bep      []byte
	signWith *rsa.PrivateKey
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
	w.Write(sign(t, signer, Manifest{Product: "RE_PPG-base", Version: "1.2.3", Files: files}))
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

func bepZip() []byte {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, p := range []string{"winhttp.dll", "doorstop_config.ini", "BepInEx/core/BepInEx.dll"} {
		w, _ := zw.Create(p)
		w.Write([]byte("bep:" + p))
	}
	zw.Close()
	return b.Bytes()
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
	oldKey, oldRel, oldURL, oldSHA := publisherKey, releases, bepInExURL, bepInExSHA256
	publisherKey, releases, bepInExURL, bepInExSHA256 = &key.PublicKey, srv.URL, srv.URL+"/bepinex.zip", hex.EncodeToString(h[:])
	t.Cleanup(func() { publisherKey, releases, bepInExURL, bepInExSHA256 = oldKey, oldRel, oldURL, oldSHA })
	g := filepath.Join(t.TempDir(), "steamapps", "common", "People Playground")
	os.MkdirAll(g, 0o755)
	os.WriteFile(filepath.Join(g, "UnityPlayer.dll"), []byte("MZ"), 0o644)
	return g
}

var baseFiles = map[string][]byte{
	"BepInEx/plugins/RE_PPG/RE_PPG.Loader.dll": []byte("loader"),
	"RE_PPG/bootstrap/seed/RE_PPG.Runtime.dll": []byte("runtime"),
}

func TestInstall(t *testing.T) {
	g := setup(t, &fakeRelease{files: baseFiles, extra: map[string][]byte{"RE_PPG/unlisted.dll": []byte("not signed")}})
	res, err := Install(context.Background(), g, t.Logf)
	if err != nil || res.Version != "1.2.3" {
		t.Fatalf("%+v %v", res, err)
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
		os.MkdirAll(filepath.Join(g, "BepInEx", "core"), 0o755)
		os.WriteFile(filepath.Join(g, "BepInEx", "core", "BepInEx.dll"), []byte("existing"), 0o644)
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

// Under Proton, Wine is told to load BepInEx's winhttp.dll: the override is
// added to the prefix's user.reg (backed up first), and left alone when set.
func TestProtonOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Proton is Linux only")
	}
	g := setup(t, &fakeRelease{files: baseFiles})
	pfx := ProtonPrefix(g)
	os.MkdirAll(pfx, 0o755)
	orig := "WINE REGISTRY Version 2\n;; All keys relative to \\\\User\\\\S-1-5-21-0-0-0-1000\n\n#arch=win64\n\n[Software\\\\Wine\\\\DllOverrides] 1700000000\n#time=1da\n\"d3d11\"=\"native\"\n\n[Software\\\\Wine\\\\Fonts] 1700000000\n"
	os.WriteFile(filepath.Join(pfx, "user.reg"), []byte(orig), 0o644)
	res, err := Install(context.Background(), g, t.Logf)
	if err != nil || res.Proton != ProtonSet {
		t.Fatalf("%+v %v", res, err)
	}
	b, _ := os.ReadFile(filepath.Join(pfx, "user.reg"))
	want := "[Software\\\\Wine\\\\DllOverrides] 1700000000\n#time=1da\n\"winhttp\"=\"native,builtin\"\n\"d3d11\"=\"native\""
	if !strings.Contains(string(b), want) || !strings.Contains(string(b), "Fonts") {
		t.Errorf("user.reg:\n%s", b)
	}
	if bk, _ := os.ReadFile(filepath.Join(pfx, "user.reg.ppgmods-backup")); string(bk) != orig {
		t.Error("no backup of the original user.reg")
	}
	if res, _ := Install(context.Background(), g, nil); res.Proton != ProtonAlready {
		t.Errorf("second install: %+v", res)
	}
}

func TestWinhttpOverrideEdits(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for name, c := range map[string]struct{ in, want string }{
		"no key":          {"WINE REGISTRY Version 2\n\n[Software\\\\Wine] 1\n", "[Software\\\\Wine\\\\DllOverrides] 1800000000\n\"winhttp\"=\"native,builtin\"\n"},
		"builtin only":    {"[Software\\\\Wine\\\\DllOverrides] 1\n\"winhttp\"=\"builtin\"\n", "\"winhttp\"=\"native,builtin\"\n"},
		"windows endings": {"[Software\\\\Wine\\\\DllOverrides] 1\r\n\"x\"=\"native\"\r\n", "[Software\\\\Wine\\\\DllOverrides] 1\r\n\"winhttp\"=\"native,builtin\"\r\n"},
	} {
		out, changed := withWinhttpOverride([]byte(c.in), now)
		if !changed || !strings.Contains(string(out), c.want) {
			t.Errorf("%s: changed %v:\n%s", name, changed, out)
		}
	}
	if _, changed := withWinhttpOverride([]byte("[Software\\\\Wine\\\\DllOverrides] 1\n\"winhttp\"=\"n,b\"\n"), now); changed {
		t.Error("an existing n,b override was rewritten")
	}
}
