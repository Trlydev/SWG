package scan

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// Names split across constants, string.Concat or hex are put back
// together before they are checked.
func TestFollowsSplitAndHexStrings(t *testing.T) {
	src := `using System; using System.Reflection;
public class M {
	const string A = "System.Diag";
	static readonly string B = A + "nostics";
	static void Run() {
		var t = Type.GetType(B + ".Process");
		var url = "68747470733a2f2f6576696c2e6578616d706c652f782e646c6c";
		t.GetMethod("Start").Invoke(null, null);
	}
}`
	r := Source("m.cs", src)
	for _, w := range []struct{ rule, sub string }{
		{"reflection-sensitive-type", "System.Diagnostics.Process"},
		{"encoded-string", "https://evil.example/x.dll"},
	} {
		if !hasDetail(r, w.rule, w.sub) {
			t.Errorf("no %s %q: %+v", w.rule, w.sub, r.Findings)
		}
	}
	r = Source("c.cs", `public class C { void F() { var u = System.Type.GetType(string.Concat("System.Net.", "WebClient")); } }`)
	if !hasDetail(r, "reflection-sensitive-type", "System.Net.WebClient") {
		t.Errorf("string.Concat not joined: %+v", r.Findings)
	}
	// A variable changed after it is set is not followed.
	r = Source("v.cs", `public class V { void F() { var s = "System.Diag"; s += "x"; var t = System.Type.GetType(s + "nostics.Process"); } }`)
	if hasDetail(r, "reflection-sensitive-type", "System.Diagnostics.Process") {
		t.Errorf("followed a changed variable: %+v", r.Findings)
	}
}

// A constant's name followed by "." (A.Length) once made the scanner loop
// forever.
func TestConstantMemberAccessTerminates(t *testing.T) {
	done := make(chan bool)
	go func() {
		Source("l.cs", `public class L { const string A = "abc"; int F() { return A.Length + A.IndexOf("b"); } }`)
		done <- true
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not finish")
	}
}

// Reading the code of compiled DLLs: which method makes a risky call, text
// glued together at run time and handed to a reflection lookup, and XOR
// decoding loops.
func TestDLLCode(t *testing.T) {
	r := &Report{}
	analyzeDLL(r, "process.dll", filepath.Join("testdata", "dll", "process.dll"))
	if !hasDetail(r, "process-spawn", "(in Evil.Go)") {
		t.Errorf("no calling method: %+v", r.Findings)
	}
	r = &Report{}
	analyzeDLL(r, "split.dll", filepath.Join("testdata", "dll", "split.dll"))
	if !hasDetail(r, "reflection-sensitive-type", "System.Diagnostics.Process") {
		t.Errorf("glued-together name not followed: %+v", r.Findings)
	}
	r = &Report{}
	analyzeDLL(r, "xordec.dll", filepath.Join("testdata", "dll", "xordec.dll"))
	if !hasDetail(r, "xor-decoding", "XorDec.D") {
		t.Errorf("XOR loop not found: %+v", r.Findings)
	}
	for _, f := range r.Findings {
		if f.Rule == "xor-decoding" && f.Severity != High {
			t.Errorf("XOR next to Process.Start should be HIGH: %+v", f)
		}
	}
	r = &Report{}
	analyzeDLL(r, "clean.dll", filepath.Join("testdata", "dll", "clean.dll"))
	for _, f := range r.Findings {
		if f.Rule == "xor-decoding" || f.Rule == "reflection-sensitive-type" {
			t.Errorf("clean DLL: %+v", f)
		}
	}
}

// Findings of the scanner's security review: decoys don't use up the
// search, joining text and folding constants stay bounded, and a cut-off
// escape doesn't crash the lexer.
func TestScannerReview(t *testing.T) {
	dll, _ := os.ReadFile(filepath.Join("testdata", "dll", "process.dll"))
	decoys := append(bytes.Repeat([]byte("MZ"), 20), dll...)
	if f := newBudget().findHidden(decoys, false, 0); len(f) == 0 {
		t.Error("20 fake MZ headers hid the real DLL")
	}
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	w.Write(dll)
	w.Close()
	fakeGz := append(bytes.Repeat([]byte{0x1f, 0x8b, 0x08, 0x00}, 20), gz.Bytes()...)
	found := false
	for _, f := range newBudget().findHidden(fakeGz, false, 0) {
		found = found || strings.Contains(f.detail, "gzip")
	}
	if !found {
		t.Error("20 fake gzip headers hid the gzipped DLL")
	}

	done := make(chan bool)
	go func() {
		// String.Concat in a loop of 2000 over a long string.
		mt := Method{Ops: []MethodOp{{Str: strings.Repeat("x", 20000)}}}
		for i := 0; i < 2000; i++ {
			mt.Ops = append(mt.Ops, MethodOp{Str: "y"}, MethodOp{Call: "System.String.Concat"})
		}
		mt.JoinedStrings()
		// Constants that double 40 times.
		var src strings.Builder
		src.WriteString(`class C { void F() { var a0 = "xxxxxxxx";`)
		for i := 1; i <= 40; i++ {
			fmt.Fprintf(&src, " var a%d = a%d + a%d;", i, i-1, i-1)
		}
		src.WriteString(" } }")
		Source("c.cs", src.String())
		// An escape cut off by the end of the file.
		Source("e.cs", `"\u`)
		Source("e2.cs", `x = "\U00`)
		done <- true
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("scanning took too long (unbounded joining or folding)")
	}
}
