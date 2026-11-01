// Package modpack reads and writes ppgmods modpacks: a list of mods and
// contraptions as one short line of text, small enough to paste into a
// chat, saved as a .ppgm file.
//
// A code is "PPG1." and then, base64url-encoded and deflate-compressed, a
// tiny binary list. Each entry says where the item comes from (GameBanana,
// the Steam Workshop mirrors, True Workshop, Nexus Mods, the Open Workshop)
// and its ID there, whether it's a contraption, whether it's turned off,
// and its name (to show the list before anything is downloaded). The code
// carries no files, URLs or code: like a game disc that only holds the key,
// it names what to fetch, and ppgmods fetches it from the site as always,
// through the same scan and safety checks as any install.
package modpack

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Prefix starts every modpack code.
const Prefix = "PPG1."

// Ext is the modpack file extension.
const Ext = ".ppgm"

// Limits that keep a pasted code from costing anything to read.
const (
	MaxEntries = 1000
	maxRaw     = 256 << 10
	maxName    = 48
	maxID      = 64
)

// sources, in code order; the index is stored in the code.
var sources = []string{"gb", "sky", "tw", "nx", "ow"}

const (
	flagOff         = 8
	flagStringID    = 16
	flagContraption = 32
)

var reID = regexp.MustCompile(`^[0-9a-z][0-9a-z-]{0,63}$`)

// Entry is one item of a pack.
type Entry struct {
	Ref         string // gb:123, sky:123, tw:…, nx:123, ow:slug
	Name        string
	Contraption bool
	Off         bool
}

// Pack is a named list of items.
type Pack struct {
	Name    string
	Entries []Entry
}

// Encode returns the pack as a modpack code. Entries whose ref isn't one
// of the sources above are left out.
func (p Pack) Encode() string {
	var raw bytes.Buffer
	putString(&raw, clip(p.Name, 80))
	var body bytes.Buffer
	n := 0
	for _, e := range p.Entries {
		src, id, ok := split(e.Ref)
		if !ok || n == MaxEntries {
			continue
		}
		n++
		flags := byte(src)
		if e.Off {
			flags |= flagOff
		}
		if e.Contraption {
			flags |= flagContraption
		}
		num, err := strconv.ParseUint(id, 10, 64)
		numeric := err == nil && num > 0 && strconv.FormatUint(num, 10) == id
		if !numeric {
			flags |= flagStringID
		}
		body.WriteByte(flags)
		if numeric {
			putUvarint(&body, num)
		} else {
			putString(&body, id)
		}
		putString(&body, clip(e.Name, maxName))
	}
	putUvarint(&raw, uint64(n))
	raw.Write(body.Bytes())
	var z bytes.Buffer
	w, _ := flate.NewWriter(&z, flate.BestCompression)
	w.Write(raw.Bytes())
	w.Close()
	return Prefix + base64.RawURLEncoding.EncodeToString(z.Bytes())
}

func split(ref string) (src int, id string, ok bool) {
	s, id, found := strings.Cut(ref, ":")
	if !found || !reID.MatchString(id) {
		return 0, "", false
	}
	for i, v := range sources {
		if v == s {
			return i, id, true
		}
	}
	return 0, "", false
}

// Find reports whether text holds a modpack code.
func Find(text string) bool { return strings.Contains(text, Prefix) }

var errDamaged = errors.New("the modpack code is damaged or cut short")

// Decode reads a modpack code (or a .ppgm file's text). Anything around the
// code (spaces, line breaks, a chat message) is ignored.
func Decode(text string) (Pack, error) {
	i := strings.Index(text, Prefix)
	if i < 0 {
		return Pack{}, errors.New("this isn't a ppgmods modpack (no PPG1. code in it)")
	}
	code := text[i+len(Prefix):]
	if j := strings.IndexFunc(code, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}); j >= 0 {
		code = code[:j]
	}
	z, err := base64.RawURLEncoding.DecodeString(code)
	if err != nil {
		return Pack{}, errDamaged
	}
	raw, err := io.ReadAll(io.LimitReader(flate.NewReader(bytes.NewReader(z)), maxRaw+1))
	if err != nil || len(raw) > maxRaw {
		return Pack{}, errDamaged
	}
	r := bytes.NewReader(raw)
	var p Pack
	if p.Name, err = getString(r, 80); err != nil {
		return Pack{}, err
	}
	n, err := binary.ReadUvarint(r)
	if err != nil || n > MaxEntries {
		return Pack{}, errDamaged
	}
	seen := map[string]bool{}
	for k := uint64(0); k < n; k++ {
		flags, err := r.ReadByte()
		if err != nil {
			return Pack{}, errDamaged
		}
		src := int(flags & 7)
		if src >= len(sources) || flags&^(7|flagOff|flagStringID|flagContraption) != 0 {
			return Pack{}, errors.New("the modpack names something this ppgmods doesn't know (made by a newer version? update ppgmods)")
		}
		var id string
		if flags&flagStringID != 0 {
			if id, err = getString(r, maxID); err != nil {
				return Pack{}, err
			}
		} else {
			num, err := binary.ReadUvarint(r)
			if err != nil || num == 0 {
				return Pack{}, errDamaged
			}
			id = strconv.FormatUint(num, 10)
		}
		if !reID.MatchString(id) {
			return Pack{}, errDamaged
		}
		e := Entry{Ref: sources[src] + ":" + id, Off: flags&flagOff != 0, Contraption: flags&flagContraption != 0}
		if e.Name, err = getString(r, 4*maxName); err != nil {
			return Pack{}, err
		}
		if !seen[e.Ref] {
			seen[e.Ref] = true
			p.Entries = append(p.Entries, e)
		}
	}
	return p, nil
}

func putUvarint(b *bytes.Buffer, v uint64) {
	var tmp [binary.MaxVarintLen64]byte
	b.Write(tmp[:binary.PutUvarint(tmp[:], v)])
}

func putString(b *bytes.Buffer, s string) {
	putUvarint(b, uint64(len(s)))
	b.WriteString(s)
}

func getString(r *bytes.Reader, max int) (string, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil || n > uint64(max) || int(n) > r.Len() {
		return "", errDamaged
	}
	b := make([]byte, n)
	r.Read(b)
	s := strings.ToValidUTF8(string(b), "")
	return strings.Map(func(c rune) rune {
		if c < 0x20 || c == 0x7f {
			return -1
		}
		return c
	}, s), nil
}

// clip shortens s to at most n bytes without splitting a character.
func clip(s string, n int) string {
	for len(s) > n {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}
