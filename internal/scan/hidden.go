package scan

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

// Hidden payloads: code a mod carries inside other bytes, to be unpacked
// and loaded at run time where no reader of its source or metadata sees
// it. A second program or DLL inside a file (plain, gzip/zlib/deflate
// compressed, or XOR-encoded with a one-byte key), and well-known strings
// (shell names, URLs, the worm's markers) XOR-encoded so a text search
// misses them.

// hiddenFind is something found inside a blob; payload is a hidden program
// or DLL when one was recovered.
type hiddenFind struct {
	rule    string
	sev     Severity
	detail  string
	payload []byte
}

const (
	maxInflate   = 64 << 20  // most a compressed blob may unpack to
	maxXORSearch = 32 << 20  // larger blobs skip the XOR search
	inflateTotal = 256 << 20 // most all compressed blobs of one scanned file may unpack to
)

// budget bounds what one scanned file may make the search unpack, however
// many compressed blobs it nests (a few MB could otherwise expand to GBs).
type budget struct{ left int64 }

func newBudget() *budget { return &budget{left: inflateTotal} }

// dosStub is in nearly every Windows program and DLL, 0x4E bytes after its
// start.
const dosStub = "This program cannot be run in DOS mode"

// xorNeedles are strings no mod has a reason to hide. Each is 7+ bytes, so
// a chance match in random data is about one in 2^48 per position.
var xorNeedles = []struct {
	s, what string
}{
	{"cmd.exe", "a Windows shell"}, {"powershell", "PowerShell"}, {"/bin/sh", "a Unix shell"}, {"/bin/bash", "a Unix shell"},
	{"http://", "a web address"}, {"https://", "a web address"}, {"Process.Start", "starting programs"},
	{"System.Diagnostics.Process", "starting programs"}, {"System.Reflection.Assembly", "loading code"},
	{"Assembly.Load", "loading code"}, {"Steamworks", "the Steam API"}, {"SteamUGC", "Steam Workshop uploads"},
	{"discord.com", "Discord"}, {"api.ipify.org", "the worm's IP lookup"}, {"STEAM_CONFIG", "the worm's Steam theft"},
	{"BinaryFormatter", "the worm's deserialization"}, {"WhereUserPublished", "the worm's Workshop spread"},
}

// findHidden looks inside b. skipStart: b is itself a program or DLL, so
// the one at offset 0 is not hidden.
func (bu *budget) findHidden(b []byte, skipStart bool, depth int) []hiddenFind {
	var out []hiddenFind
	seen := map[int]bool{}
	add := func(f hiddenFind) {
		if len(out) < 16 {
			out = append(out, f)
		}
	}
	// A plain program or DLL inside. Every "MZ" is looked at (decoys, and
	// chance pairs in a large DLL, mustn't use up the search); only real
	// headers count towards the limit.
	for i, n := 0, 0; n < 8; {
		k := bytes.Index(b[i:], []byte("MZ"))
		if k < 0 {
			break
		}
		at := i + k
		i = at + 2
		if (at == 0 && skipStart) || !isPE(b[at:]) {
			continue
		}
		n++
		seen[at] = true
		add(hiddenFind{"hidden-executable", Critical, fmt.Sprintf("a Windows program or DLL is hidden inside, at byte %d", at), b[at : at+peSize(b[at:])]})
	}
	// Compressed: gzip anywhere (its 3-byte header is distinctive). Headers
	// that don't open are skipped without counting; the unpacking budget
	// bounds the work.
	for i, n := 0, 0; n < 8 && depth < 2 && bu.left > 0; {
		k := bytes.Index(b[i:], []byte{0x1f, 0x8b, 0x08})
		if k < 0 {
			break
		}
		at := i + k
		i = at + 3
		zr, err := gzip.NewReader(bytes.NewReader(b[at:]))
		if err != nil {
			continue
		}
		if dec := bu.read(zr); len(dec) > 0 {
			n++
			out = append(out, bu.unpacked("gzip", at, dec, depth)...)
		}
	}
	// One-byte XOR.
	if len(b) <= maxXORSearch {
		for _, x := range xorFinds(b) {
			if x.needle == dosStub {
				start := x.at - 0x4E
				if start < 0 || seen[start] {
					continue
				}
				if x.key > 0xff {
					continue // a program is bytes: a 2-byte key match is chance
				}
				dec := make([]byte, len(b)-start)
				for j := range dec {
					dec[j] = b[start+j] ^ byte(x.key)
				}
				if isPE(dec) {
					dec = dec[:peSize(dec)]
					add(hiddenFind{"hidden-executable", Critical, fmt.Sprintf("a Windows program or DLL is hidden inside, XOR-encoded (key %#02x)", x.key), dec})
				}
				continue
			}
			add(hiddenFind{"xor-hidden-string", Critical, fmt.Sprintf("hides %q (%s) with XOR encoding (key %#02x)", x.needle, x.what, x.key), nil})
		}
	}
	return out
}

// findHiddenResource looks inside one embedded resource for what the
// whole-file search can't see: a bare zlib or deflate stream (how DLL
// bundlers pack libraries).
func (bu *budget) findHiddenResource(data []byte, depth int) []hiddenFind {
	if depth >= 2 || bu.left <= 0 {
		return nil
	}
	if zr, err := zlib.NewReader(bytes.NewReader(data)); err == nil {
		return bu.unpacked("zlib", 0, bu.read(zr), depth)
	}
	return bu.unpacked("deflate", 0, bu.read(flate.NewReader(bytes.NewReader(data))), depth)
}

// unpacked reports what a decompressed blob holds.
func (bu *budget) unpacked(how string, at int, dec []byte, depth int) []hiddenFind {
	if len(dec) == 0 {
		return nil
	}
	var out []hiddenFind
	if isPE(dec) {
		out = append(out, hiddenFind{"hidden-executable", Critical, fmt.Sprintf("a Windows program or DLL is hidden inside, %s-compressed (at byte %d)", how, at), dec})
	}
	for _, f := range bu.findHidden(dec, isPE(dec), depth+1) {
		f.detail += " (inside " + how + "-compressed data)"
		out = append(out, f)
	}
	return out
}

// read unpacks r, within maxInflate and what is left of the budget.
func (bu *budget) read(r io.Reader) []byte {
	n := int64(maxInflate)
	if bu.left < n {
		n = bu.left
	}
	if n <= 0 {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(r, n))
	bu.left -= int64(len(b))
	return b
}

// peSize is how many bytes of b the program or DLL at its start spans: up
// to the end of its last section (all of b if the headers don't say).
func peSize(b []byte) int {
	pe := int(binary.LittleEndian.Uint32(b[0x3c:]))
	if pe+24 > len(b) {
		return len(b)
	}
	nsec := int(binary.LittleEndian.Uint16(b[pe+6:]))
	at := pe + 24 + int(binary.LittleEndian.Uint16(b[pe+20:]))
	end := 0
	for i := 0; i < nsec && at+40 <= len(b); i, at = i+1, at+40 {
		if e := int(binary.LittleEndian.Uint32(b[at+20:])) + int(binary.LittleEndian.Uint32(b[at+16:])); e > end {
			end = e
		}
	}
	if end <= 0 || end > len(b) {
		return len(b)
	}
	return end
}

// isPE reports whether b starts with a Windows program or DLL header.
func isPE(b []byte) bool {
	if len(b) < 0x40 || b[0] != 'M' || b[1] != 'Z' {
		return false
	}
	pe := int(binary.LittleEndian.Uint32(b[0x3c:]))
	return pe >= 0x40 && pe < 4096 && pe+4 <= len(b) && string(b[pe:pe+4]) == "PE\x00\x00"
}

type xorFind struct {
	needle, what string
	at           int
	key          uint16 // one byte, or two for UTF-16 text
}

// xorFinds searches b for the needles XOR-encoded with any one-byte key, in
// one pass per needle: XOR-ing neighbouring bytes cancels the key, so the
// encoded needle's neighbour-XORs equal the plain needle's. Text stored as
// UTF-16 (as .NET keeps string literals) is searched with 2-byte
// neighbours, which also cancels a 2-byte key. Key 0 (plain text) and
// 0x20 (only flips letter case) are not reported.
func xorFinds(b []byte) []xorFind {
	var out []xorFind
	if len(b) < 8 {
		return nil
	}
	d1 := neighbourXOR(b, 1)
	d2 := neighbourXOR(b, 2)
	needles := append([]struct{ s, what string }{{dosStub, "a hidden program"}}, xorNeedles...)
	for _, n := range needles {
		for _, enc := range []struct {
			pat  []byte
			d    []byte
			step int
		}{{[]byte(n.s), d1, 1}, {utf16le(n.s), d2, 2}} {
			nd := neighbourXOR(enc.pat, enc.step)
			from := 0
			for hits := 0; hits < 4; {
				k := bytes.Index(enc.d[from:], nd)
				if k < 0 {
					break
				}
				at := from + k
				from = at + 1
				key := uint16(b[at] ^ enc.pat[0])
				if enc.step == 2 {
					key |= uint16(b[at+1]^enc.pat[1]) << 8
				}
				if key == 0 || key == 0x20 {
					continue
				}
				out = append(out, xorFind{n.s, n.what, at, key})
				hits++
			}
		}
	}
	return out
}

func neighbourXOR(b []byte, step int) []byte {
	if len(b) <= step {
		return nil
	}
	d := make([]byte, len(b)-step)
	for i := range d {
		d[i] = b[i] ^ b[i+step]
	}
	return d
}

// reportHidden adds what findHidden found in file rel. A recovered DLL is
// analysed too, so the report says what the hidden code can do.
func reportHidden(r *Report, rel, where string, finds []hiddenFind, depth int, bu *budget) {
	for _, f := range finds {
		detail := f.detail
		if where != "" {
			detail = where + ": " + detail
		}
		r.add(f.sev, f.rule, rel, 0, detail)
		if f.payload != nil && depth < 2 {
			sub := &Report{}
			analyzeDLLWith(sub, rel, f.payload, depth+1, bu)
			for _, sf := range sub.Findings {
				if sf.Rule == "compiled-code" || sf.Severity < Medium {
					continue
				}
				sf.Detail = "the hidden code: " + strings.TrimPrefix(sf.Detail, "compiled code ")
				r.Findings = append(r.Findings, sf)
			}
		}
	}
}

// hiddenInSource looks for hidden payloads in a C# file: byte array
// literals (one by one and all joined, in case one was split) that hold a
// program or XOR-encoded strings, string literals with XOR-encoded text,
// and XOR decoding of characters or bytes. risky: the file also loads
// code, uses reflection, starts programs or uses the network.
func hiddenInSource(toks []token, hit func(string, Severity, int, string), risky bool) {
	bu := newBudget()
	var all []byte
	first := 0
	for _, arr := range byteArrays(toks) {
		if first == 0 {
			first = arr.line
		}
		if len(all) < maxXORSearch {
			all = append(all, arr.b...)
		}
		for _, f := range bu.findHidden(arr.b, false, 0) {
			hit(f.rule, f.sev, arr.line, "byte array: "+f.detail)
		}
	}
	if len(all) > 0 {
		for _, f := range bu.findHidden(all, false, 0) {
			if f.rule == "hidden-executable" {
				hit(f.rule, f.sev, first, "byte arrays, joined: "+f.detail)
			}
		}
	}
	for _, s := range joinedStrings(toks) {
		for _, x := range xorFinds(utf16le(s.text)) {
			hit("xor-hidden-string", Critical, s.line, fmt.Sprintf("string hides %q (%s) with XOR encoding (key %#02x)", x.needle, x.what, x.key))
		}
	}
	// a ^ b next to a char/byte cast or an indexer: decoding text or data.
	sites, line := 0, 0
	for i, t := range toks {
		if t.kind != tPunct || t.text != "^" || i == 0 || toks[i-1].text == "[" || toks[i-1].text == "," || toks[i-1].text == ".." {
			continue // [^1] is "from the end", not XOR
		}
		for k := i - 8; k < i+8 && k < len(toks); k++ {
			if k < 0 || toks[k].text == ";" && k > i {
				continue
			}
			if tk := toks[k]; tk.kind == tIdent && (tk.text == "char" || tk.text == "byte") || tk.text == "[" && k < i {
				sites++
				if line == 0 {
					line = t.line
				}
				break
			}
		}
	}
	switch {
	case sites > 0 && risky:
		hit("xor-decoding", High, line, fmt.Sprintf("decodes characters or bytes with XOR (%d places) in a file that also loads code, uses reflection, starts programs or uses the network: a common way to hide what it does", sites))
	case sites >= 2:
		hit("xor-decoding", Medium, line, fmt.Sprintf("decodes characters or bytes with XOR (%d places): sometimes used to hide text", sites))
	}
}

type byteArray struct {
	b    []byte
	line int
}

// byteArrays reads byte[] initialisers of 16+ numbers: new byte[] { 77,
// 90, ... }, byte[] x = { 0x4D, ... }. Casts like (byte) are skipped.
func byteArrays(toks []token) []byteArray {
	var out []byteArray
	for i := 0; i+3 < len(toks); i++ {
		if toks[i].text != "byte" || toks[i+1].text != "[" {
			continue
		}
		j := i + 2
		for j < len(toks) && j < i+8 && toks[j].text != "{" && toks[j].text != ";" {
			j++
		}
		if j >= len(toks) || toks[j].text != "{" {
			continue
		}
		var b []byte
		k := j + 1
		for ; k < len(toks) && toks[k].text != "}"; k++ {
			t := toks[k]
			switch {
			case t.kind == tNumber:
				if v, ok := parseIntLit(t.text); ok && v >= 0 && v < 256 {
					b = append(b, byte(v))
				}
			case t.text == "," || t.text == "(" || t.text == ")" || t.text == "byte":
			default:
				k = len(toks) // not a plain list of numbers
			}
		}
		if len(b) >= 16 {
			out = append(out, byteArray{b, toks[i].line})
		}
		i = j
	}
	return out
}

// parseIntLit reads a C# integer literal: 123, 0x7B, 0b1111011, with _
// separators and u/l suffixes.
func parseIntLit(s string) (int64, bool) {
	s = strings.ToLower(strings.ReplaceAll(s, "_", ""))
	s = strings.TrimRight(s, "ul")
	base := int64(10)
	switch {
	case strings.HasPrefix(s, "0x"):
		base, s = 16, s[2:]
	case strings.HasPrefix(s, "0b"):
		base, s = 2, s[2:]
	}
	if s == "" || len(s) > 16 {
		return 0, false
	}
	var v int64
	for _, c := range s {
		d := int64(-1)
		switch {
		case c >= '0' && c <= '9':
			d = int64(c - '0')
		case c >= 'a' && c <= 'f':
			d = int64(c-'a') + 10
		}
		if d < 0 || d >= base {
			return 0, false
		}
		v = v*base + d
	}
	return v, true
}

// inspectAsset looks inside a file that should be plain data (an image, a
// sound, mod.json) for a hidden program or XOR-hidden strings.
func inspectAsset(r *Report, rel, path string) {
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	if fi.Size() > maxXORSearch {
		r.add(Medium, "too-large-to-inspect", rel, 0, fmt.Sprintf("%d MB: too large to look inside for hidden code", fi.Size()>>20))
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	bu := newBudget()
	reportHidden(r, rel, "", bu.findHidden(b, isNativeBinary(b), 0), 0, bu)
}
