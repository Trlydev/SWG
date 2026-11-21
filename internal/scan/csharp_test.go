package scan

import (
	"os"
	"path/filepath"
	"testing"
)

// Tricks the old line-based scanner could not see.
func TestEvasionsCaught(t *testing.T) {
	cases := map[string]struct{ src, rule string }{
		"alias":         {"using P = System.Diagnostics.Process;\nclass A { void B() { P.Start(\"x\"); } }", "process-spawn"},
		"split lines":   {"class A { void B() { System.\n  Diagnostics.\n  Process.Start(\"x\"); } }", "process-spawn"},
		"comment split": {"class A { object c = new System./*hi*/Net.WebClient(); }", "network"},
		"interpolation": {"class A { string s = $\"{System.Diagnostics.Process.Start(\\\"x\\\")}\"; }", "process-spawn"},
		"using static":  {"using static System.IO.File;\nclass A { void B() { Delete(\"x\"); } }", "file-delete"},
		"global":        {"class A { void B() { global::System.IO.Directory.Delete(\"x\", true); } }", "file-delete"},
		"steam alias":   {"using U = Steamworks.SteamUGC;\nclass A { void B() { U.SubmitItemUpdate(h, \"\"); } }", "steam-ugc"},
		"namespace":     {"using System.Security.Cryptography;\nclass A {}", "namespace"},
		"unity web":     {"using UnityEngine.Networking;\nclass A { void B() { UnityWebRequest.Get(\"u\"); } }", "network"},
		"extern":        {"class A { [System.Runtime.InteropServices.DllImport(\"k\")] static extern int F(); }", "native-interop"},
		"shell string":  {"class A { string s = \"powershell -c x\"; }", "shell-string"},
		"concat refl":   {"class A { void B() { var t = System.Type.GetType(\"System.Diag\" + \"nostics.Process\"); } }", "reflection-sensitive-type"},
		// ZeroOne Core's loader reaches Assembly.Load(byte[]) this way.
		"qualified refl": {"using System; class A { void B() { var d = Type.GetType(\"System.IO.Directory, mscorlib\"); var a = Type.GetType(\"System.Reflection.Assembly\"); } }", "reflection-sensitive-type"},
		"refl file io":   {"using System; class A { void B() { Type.GetType(\"System.IO.Directory\").GetMethod(\"Exists\"); } }", "reflection-file-access"},
	}
	for name, c := range cases {
		if r := Source("x.cs", c.src); !has(r, c.rule) {
			t.Errorf("%s: %s not detected; got %+v", name, c.rule, r.Findings)
		}
	}
}

// Code-looking text in strings, comments and #if branches' strings is data.
func TestNoFalsePositives(t *testing.T) {
	cases := map[string]string{
		"raw string":      "class A { string s = \"\"\"\nSystem.Diagnostics.Process.Start\n\"\"\"; }",
		"verbatim":        "class A { string s = @\"C:\\Process.Start \"\" here\"; }",
		"comment":         "// System.Net.WebClient\n/* Process.Start */ class A {}",
		"own Process":     "namespace MyMod { class Proc { void Start() {} } class B { void C() { new Proc().Start(); } } }",
		"allowed usings":  "using System; using System.Collections.Generic; using System.Linq; using UnityEngine; using TMPro;\nclass A {}",
		"using statement": "using System;\nclass A { void B() { using (var x = new Disp()) { } using var y = new Disp(); } }",
		"char literal":    "class A { char c = '\"'; string s = \"Process\"; }",
	}
	for name, src := range cases {
		if r := Source("x.cs", src); r.Max() >= High {
			t.Errorf("%s: flagged %+v", name, r.Findings)
		}
	}
}

// Ways to call any method by name that no reflection-lookup rule sees, and
// reading an object's own fields (which calls nothing).
func TestReflectionCracksAndFields(t *testing.T) {
	cases := []struct {
		name, src string
		rule      string
		sev       Severity
	}{
		{"dynamic", `class A { void B(object o) { dynamic d = o; d.Run("x"); } }`, "dynamic-dispatch", High},
		{"expression trees", `using System.Linq.Expressions; class A { void B() { Expression.Lambda(Expression.Call(typeof(UnityEngine.Application), "OpenURL", null, Expression.Constant("x"))).Compile(); } }`, "dynamic-code", High},
		{"CreateDelegate", `class A { void B(System.Reflection.MethodInfo m) { System.Delegate.CreateDelegate(typeof(System.Action), m); } }`, "reflection-invoke", High},
		{"AppDomain.CurrentDomain.Load", `class A { void B(byte[] b) { System.AppDomain.CurrentDomain.Load(b); } }`, "code-loader", Critical},
		{"fields only", `class A { void B(object o) { foreach (var f in o.GetType().GetFields()) UnityEngine.Debug.Log(f.Name + f.GetValue(o)); } }`, "reflection-fields", Info},
		{"a field by name", `class A { void B(object c) { var f = c.GetType().GetField("IsLimp"); f.SetValue(c, true); } }`, "reflection-field-by-name", Medium},
		{"a field by name, with a way to call", `class A { void B(object c) { c.GetType().GetField("IsLimp"); c.GetType().GetMethod("Die").Invoke(c, null); } }`, "reflection-by-name", High},
		{"UnityEvent internals by name", `class A { void B(UnityEngine.Events.UnityEvent e) { e.GetType().GetField("m_PersistentCalls"); } }`, "reflection-by-name", High},
		{"fields, then a method by name", `class A { void B(object o) { var f = o.GetType().GetFields(); o.GetType().GetMethod(f[0].Name).Invoke(o, null); } }`, "reflection-enumerate", High},
	}
	for _, c := range cases {
		d := t.TempDir()
		os.WriteFile(filepath.Join(d, "x.cs"), []byte(c.src), 0o644)
		r, err := Dir(d)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, f := range r.Findings {
			if f.Rule == c.rule && f.Severity == c.sev {
				found = true
			}
		}
		if !found || r.Max() < c.sev || c.sev < High && r.Max() != c.sev {
			t.Errorf("%s: want %s %s, got max %s: %+v", c.name, c.sev, c.rule, r.Max(), r.Findings)
		}
	}
}
