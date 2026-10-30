package scan

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Capability analysis of compiled mods. A mod's DLL can't be read like its
// C# source, but its metadata names every outside type and member it calls,
// every native function it imports, and its string literals (see ReadCLR).
// Those are checked against the same API rules as source code, so the
// report says what the DLL can do: start programs, use the network, touch
// files, load other code, call native code, or reach code by name through
// reflection. Code that isn't .NET, or whose metadata can't be read, stays
// unreadable (CRITICAL).

// reflectionCalls are the calls that reach code chosen by name at run time:
// with them, the metadata no longer lists everything a DLL can reach.
var reflectionCalls = []string{
	"System.Reflection.MethodBase.Invoke", "System.Reflection.MethodInfo.Invoke", "System.Reflection.ConstructorInfo.Invoke",
	"System.Reflection.PropertyInfo.SetValue", "System.Reflection.PropertyInfo.GetValue",
	"System.Type.InvokeMember", "System.Type.GetType", "System.Activator.CreateInstance", "System.Delegate.CreateDelegate",
	"System.Reflection.Assembly.GetType", "System.Reflection.Assembly.CreateInstance",
}

// reflectionLookups take a name and find code by it: text handed to one
// of them is what the DLL reaches.
var reflectionLookups = map[string]bool{
	"System.Type.GetType": true, "System.Reflection.Assembly.GetType": true, "System.Type.GetMethod": true,
	"System.Type.GetField": true, "System.Type.GetProperty": true, "System.Type.GetMember": true,
	"System.Type.InvokeMember": true, "System.Activator.CreateInstance": true, "System.AppDomain.CreateInstance": true,
	"System.Reflection.Assembly.Load": true, "System.Reflection.Assembly.LoadFrom": true,
	"System.Reflection.Assembly.LoadFile": true, "System.Reflection.Assembly.CreateInstance": true,
}

// clrName turns a metadata type name into the dotted form the rules use:
// nested types joined with ".", generic arity ("List`1") dropped.
func clrName(t string) string {
	t = strings.ReplaceAll(t, "/", ".")
	for {
		i := strings.IndexByte(t, '`')
		if i < 0 {
			return t
		}
		j := i + 1
		for j < len(t) && t[j] >= '0' && t[j] <= '9' {
			j++
		}
		t = t[:i] + t[j:]
	}
}

// analyzeDLL reports what a mod's DLL can do, read from its metadata.
func analyzeDLL(r *Report, rel, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	analyzeDLLBytes(r, rel, b, 0)
}

// analyzeDLLBytes is analyzeDLL for a DLL's bytes; depth counts how deep
// inside other files this one was hidden.
func analyzeDLLBytes(r *Report, rel string, b []byte, depth int) {
	if len(b) > 64<<20 {
		r.add(Critical, "executable-file", rel, 0, "compiled library too large to read: its code cannot be checked")
		return
	}
	info, err := ReadCLR(b)
	// Hidden payloads inside, whatever the metadata says.
	reportHidden(r, rel, "", findHidden(b, true, depth), depth)
	if info != nil {
		for _, res := range info.Resources {
			reportHidden(r, rel, "resource "+clip(res.Name), findHiddenResource(res.Data, depth), depth)
		}
	}
	switch {
	case errors.Is(err, errNotCLR):
		r.add(Critical, "executable-file", rel, 0, "native (not .NET) library: its code cannot be checked")
		return
	case err != nil:
		r.add(Critical, "executable-file", rel, 0, "compiled library whose metadata can't be read ("+err.Error()+"): its code cannot be checked")
		return
	case !info.ILOnly:
		r.add(Critical, "native-code", rel, 0, "mixed .NET and native library: its native code cannot be checked")
	}

	type capHit struct {
		sev      Severity
		desc     string
		examples []string
	}
	caps := map[string]*capHit{}
	note := func(id string, sev Severity, desc, example string) {
		h := caps[id]
		if h == nil {
			h = &capHit{sev: sev, desc: desc}
			caps[id] = h
		}
		if sev > h.sev {
			h.sev = sev
		}
		if len(h.examples) < 3 && !containsString(h.examples, example) {
			h.examples = append(h.examples, example)
		}
	}
	// Attributes are markers the compiler adds (GeneratedCode,
	// SecurityPermission...): naming one runs nothing.
	attr := func(t string) bool { return strings.HasSuffix(t, "Attribute") }
	refs := map[string]bool{}
	namespaces := map[string]bool{}
	for _, t := range info.TypeRefs {
		if attr(t) {
			continue
		}
		refs[clrName(t)] = true
		outer, _, _ := strings.Cut(t, "/")
		if i := strings.LastIndexByte(outer, '.'); i > 0 {
			namespaces[outer[:i]] = true
		}
	}
	for _, mr := range info.MemberRefs {
		if mr.Type != "" && !attr(mr.Type) {
			refs[clrName(mr.Type)+"."+mr.Name] = true
		}
	}
	shown := func(name string) string {
		if t, ok := strings.CutSuffix(name, "..ctor"); ok {
			return "new " + t
		}
		return name
	}
	dynamic := false
	names := make([]string, 0, len(refs))
	for name := range refs {
		names = append(names, name)
	}
	sort.Strings(names)
	// Which method makes each call, from the IL.
	caller := map[string]string{}
	for _, mt := range info.Methods {
		for _, op := range mt.Ops {
			if op.Call != "" && caller[op.Call] == "" {
				caller[op.Call] = mt.Name
			}
		}
	}
	where := func(name string) string {
		if c := caller[name]; c != "" {
			return shown(name) + " (in " + c + ")"
		}
		return shown(name)
	}
	for _, name := range names {
		matchRules(name, func(p string, ru *apiRule) {
			if ru.sev > Info {
				note(ru.id, ru.sev, ru.desc, where(name))
			}
		})
		for _, rc := range reflectionCalls {
			if name == rc {
				dynamic = true
				note("reflection-invoke", High, "calls code chosen by name at run time (reflection), so its full reach can't be read from the file", name)
			}
		}
	}
	var nsList []string
	for ns := range namespaces {
		nsList = append(nsList, ns)
	}
	sort.Strings(nsList)
	for _, ns := range nsList {
		if ns == "System.Security.Permissions" {
			continue // only the enums of security attributes the compiler adds
		}
		if sev, _, ok := namespaceVerdict(ns); ok && sev >= High {
			note("namespace", High, "uses parts of .NET outside the allowlist for mods", ns)
		}
	}
	for _, pi := range info.PInvokes {
		note("native-interop", Critical, "calls native code", pi)
	}
	// String literals, and text methods build from them with String.Concat:
	// names used with reflection, shells, worm markers.
	hit := func(id string, sev Severity, _ int, detail string) { note(id, sev, detail, "") }
	texts := append([]string(nil), info.Strings...)
	literal := map[string]bool{}
	for _, s := range info.Strings {
		literal[s] = true
	}
	risky := false
	for _, id := range []string{"process-spawn", "network", "code-loader", "dynamic-code", "native-interop", "reflection-invoke"} {
		risky = risky || caps[id] != nil
	}
	for _, mt := range info.Methods {
		for _, js := range mt.JoinedStrings() {
			if !literal[js.Text] {
				literal[js.Text] = true
				texts = append(texts, js.Text) // built from pieces
			}
			if reflectionLookups[js.UsedBy] {
				if sev, ok := sensitivity(js.Text); ok {
					note("reflection-sensitive-type", sev, "looks up APIs by name through reflection", fmt.Sprintf("%q in %s", clip(js.Text), mt.Name))
				}
			}
		}
		switch {
		case mt.XORChars && risky, mt.XORBytes && risky:
			note("xor-decoding", High, "decodes text or data with XOR in a loop, in a DLL that also starts programs, uses the network, loads code or uses reflection: a common way to hide what it does", mt.Name)
		case mt.XORChars:
			note("xor-decoding", Medium, "decodes characters with XOR in a loop (sometimes used to hide text)", mt.Name)
		}
	}
	for _, s := range texts {
		low := strings.ToLower(strings.Join(strings.Fields(s), ""))
		sev, ok := sensitivity(s)
		switch {
		case ok && dynamic:
			note("reflection-sensitive-type", sev, "names APIs in strings and calls code by name, which reaches them without listing them", clip(s))
		case ok:
			note("sensitive-string", Medium, "mentions sensitive APIs in strings", clip(s))
		}
		if reShellStr.MatchString(s) {
			note("shell-string", High, "names a shell or download tool in a string", clip(s))
		}
		checkMarkers(hit, 0, low, "a string")
	}
	lits := make([]strLit, len(texts))
	for i, s := range texts {
		lits[i] = strLit{text: s}
	}
	checkEncoded(hit, lits)

	ids := make([]string, 0, len(caps))
	for id := range caps {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if caps[ids[i]].sev != caps[ids[j]].sev {
			return caps[ids[i]].sev > caps[ids[j]].sev
		}
		return ids[i] < ids[j]
	})
	var can []string
	for _, id := range ids {
		h := caps[id]
		detail := h.desc
		var ex []string
		for _, e := range h.examples {
			if e != "" {
				ex = append(ex, e)
			}
		}
		if len(ex) > 0 {
			detail += ": " + strings.Join(ex, ", ")
		}
		r.add(h.sev, id, rel, 0, "compiled code "+detail)
		if h.sev >= Medium {
			can = append(can, id)
		}
	}
	summary := "only the game, Unity and basic .NET (read from its metadata)"
	if len(can) > 0 {
		summary = "see the findings: " + strings.Join(can, ", ")
	}
	r.add(High, "compiled-code", rel, 0, fmt.Sprintf("compiled library: it can't be read like source, but its metadata lists what it uses (%d outside types, %d calls): %s", len(info.TypeRefs), len(info.MemberRefs), summary))
}

// sensitivity is how dangerous an API named in text is, if it names one.
func sensitivity(s string) (Severity, bool) {
	low := strings.ToLower(strings.Join(strings.Fields(s), ""))
	sev, ok := sensitiveExact[low]
	if !ok {
		sev, ok = sensitiveExact[lastSegment(low)]
	}
	for _, w := range sensitiveSubstr {
		if strings.Contains(low, w.s) && (!ok || w.sev > sev) {
			sev, ok = w.sev, true
		}
	}
	return sev, ok
}

func containsString(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
