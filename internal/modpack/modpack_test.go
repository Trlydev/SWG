package modpack

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	p := Pack{Name: "Weekend war", Entries: []Entry{
		{Ref: "gb:462076", Name: "Modern Weapons"},
		{Ref: "sky:2853017413", Name: "Big Tank", Contraption: true},
		{Ref: "ow:lite-pixel-blood", Name: "Lite Pixel Blood", Off: true},
		{Ref: "nx:12", Name: "Ünïcode ✓"},
		{Ref: "tw:abc-123", Name: "True one"},
		{Ref: "local:foo", Name: "hand-installed"}, // can't travel
	}}
	code := p.Encode()
	got, err := Decode("look at this: " + code + " !")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != p.Name || len(got.Entries) != 5 {
		t.Fatalf("got %+v", got)
	}
	for i, e := range got.Entries {
		if e != p.Entries[i] {
			t.Errorf("entry %d: %+v, want %+v", i, e, p.Entries[i])
		}
	}
}

// Twenty mods fit in a chat message.
func TestShort(t *testing.T) {
	var p Pack
	for i := 0; i < 20; i++ {
		p.Entries = append(p.Entries, Entry{Ref: fmt.Sprintf("gb:%d", 460000+i*37), Name: fmt.Sprintf("Mod number %d", i)})
	}
	if code := p.Encode(); len(code) > 400 {
		t.Errorf("20 mods take %d characters", len(code))
	}
}

func TestDecodeRefuses(t *testing.T) {
	raw := func(b []byte) string {
		var z bytes.Buffer
		w, _ := flate.NewWriter(&z, 9)
		w.Write(b)
		w.Close()
		return Prefix + base64.RawURLEncoding.EncodeToString(z.Bytes())
	}
	for name, text := range map[string]string{
		"no code":       "hello",
		"not base64":    Prefix + "!!!",
		"cut":           Pack{Name: "x", Entries: []Entry{{Ref: "gb:1", Name: "a"}}}.Encode()[:12],
		"unknown src":   raw([]byte{0, 1, 7, 1, 0}),
		"unknown flag":  raw([]byte{0, 1, 64, 1, 0}),
		"too many":      raw([]byte{0, 0xE9, 0x07}),
		"zero id":       raw([]byte{0, 1, 0, 0, 0}),
		"bad string id": raw([]byte{0, 1, 16, 3, '.', '.', '/', 0}),
		"huge":          raw(bytes.Repeat([]byte{0}, maxRaw+10)),
	} {
		if _, err := Decode(text); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestDuplicatesAndControl(t *testing.T) {
	p := Pack{Name: "a\x00b", Entries: []Entry{{Ref: "gb:5", Name: "x\ny"}, {Ref: "gb:5", Name: "again"}}}
	got, err := Decode(p.Encode())
	if err != nil || len(got.Entries) != 1 || got.Name != "ab" || got.Entries[0].Name != "xy" || strings.ContainsAny(got.Name, "\x00") {
		t.Fatalf("%+v %v", got, err)
	}
}
