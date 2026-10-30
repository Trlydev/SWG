package scan

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hasDetail(r *Report, rule, sub string) bool {
	for _, f := range r.Findings {
		if f.Rule == rule && strings.Contains(f.Detail, sub) {
			return true
		}
	}
	return false
}

func xorBytes(b []byte, k byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[i] = b[i] ^ k
	}
	return out
}

// A DLL that carries another in a deflate-compressed resource: found, and
// what the hidden one can do is reported.
func TestHiddenDLLInResource(t *testing.T) {
	r := &Report{}
	analyzeDLL(r, "bundled.dll", filepath.Join("testdata", "dll", "bundled.dll"))
	if !hasDetail(r, "hidden-executable", "resource payload.bin") || !hasDetail(r, "hidden-executable", "deflate") {
		t.Fatalf("hidden DLL not found: %+v", r.Findings)
	}
	if !hasDetail(r, "process-spawn", "the hidden code") {
		t.Errorf("what the hidden DLL does is not reported: %+v", r.Findings)
	}
	clean := &Report{}
	analyzeDLL(clean, "clean.dll", filepath.Join("testdata", "dll", "clean.dll"))
	for _, f := range clean.Findings {
		if f.Rule == "hidden-executable" || f.Rule == "xor-hidden-string" {
			t.Errorf("clean DLL: %+v", f)
		}
	}
}

// Programs hidden in a mod's image: plain, XOR-encoded, gzip-compressed;
// strings hidden with XOR, as bytes and as UTF-16.
func TestHiddenInAsset(t *testing.T) {
	dll, err := os.ReadFile(filepath.Join("testdata", "dll", "process.dll"))
	if err != nil {
		t.Fatal(err)
	}
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7, 1, 200, 3}, 300)...)
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	w.Write(dll)
	w.Close()
	cases := []struct {
		name string
		data []byte
		rule string
		sub  string
	}{
		{"plain", append(append([]byte{}, png...), dll...), "hidden-executable", "at byte"},
		{"xor", append(append([]byte{}, png...), xorBytes(dll, 0x5a)...), "hidden-executable", "XOR-encoded (key 0x5a)"},
		{"gzip", append(append([]byte{}, png...), gz.Bytes()...), "hidden-executable", "gzip-compressed"},
		{"xor string", append(append([]byte{}, png...), xorBytes([]byte("powershell -enc"), 0x13)...), "xor-hidden-string", "powershell"},
		{"xor utf16", append(append([]byte{}, png...), xorBytes(utf16le("https://evil.example"), 0x77)...), "xor-hidden-string", "https://"},
	}
	dir := t.TempDir()
	for _, c := range cases {
		p := filepath.Join(dir, c.name+".png")
		os.WriteFile(p, c.data, 0o644)
		r := &Report{}
		inspectAsset(r, c.name+".png", p)
		if !hasDetail(r, c.rule, c.sub) {
			t.Errorf("%s: no %s %q: %+v", c.name, c.rule, c.sub, r.Findings)
		}
	}
	p := filepath.Join(dir, "clean.png")
	os.WriteFile(p, append(png, []byte("plain https:// text, cmd.exe, CMD.EXE")...), 0o644)
	r := &Report{}
	inspectAsset(r, "clean.png", p)
	if len(r.Findings) != 0 {
		t.Errorf("clean image: %+v", r.Findings)
	}
}

func TestHiddenInSource(t *testing.T) {
	dll, _ := os.ReadFile(filepath.Join("testdata", "dll", "process.dll"))
	var nums []string
	for _, b := range dll[:200] {
		nums = append(nums, "0x"+strings.ToUpper(string("0123456789abcdef"[b>>4])+string("0123456789abcdef"[b&15])))
	}
	var esc strings.Builder
	for _, c := range xorBytes([]byte("cmd.exe /c del"), 0x2a) {
		esc.WriteString(`\u00`)
		esc.WriteByte("0123456789abcdef"[c>>4])
		esc.WriteByte("0123456789abcdef"[c&15])
	}
	src := `using UnityEngine;
public class M : MonoBehaviour {
	static byte[] blob = new byte[] { ` + strings.Join(nums, ", ") + ` };
	static string s = "` + esc.String() + `";
	static string D(string x) { var o = ""; for (int i = 0; i < x.Length; i++) o += (char)(x[i] ^ 42); return o; }
	void Start() { System.Diagnostics.Process.Start(D(s)); }
}`
	r := Source("m.cs", src)
	for _, w := range []struct{ rule, sub string }{
		{"hidden-executable", "byte array"},
		{"xor-hidden-string", "cmd.exe"},
		{"xor-decoding", "starts programs"},
	} {
		if !hasDetail(r, w.rule, w.sub) {
			t.Errorf("no %s %q: %+v", w.rule, w.sub, r.Findings)
		}
	}

	clean := Source("c.cs", `using UnityEngine;
public class C : MonoBehaviour {
	int Hash(int a, int b) { int h = a; h ^= b; return h ^ (h >> 3); }
	void Start() { var a = new int[] {1,2,3}; var last = a[^1]; Debug.Log(last); }
}`)
	for _, f := range clean.Findings {
		if f.Rule == "xor-decoding" || f.Rule == "xor-hidden-string" || f.Rule == "hidden-executable" {
			t.Errorf("clean source: %+v", f)
		}
	}
}
