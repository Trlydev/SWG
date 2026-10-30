package scan

import (
	"encoding/binary"
	"errors"
)

// Hollowing, for the deep check: a copy of a game DLL whose engine calls
// (internal calls into Unity's native code, and P/Invokes into native
// libraries such as Steam's) do nothing and return an empty value. Outside
// the game those calls have no native code behind them: the first one a
// mod reaches (often in ModAPI's static constructor) would throw, and
// whatever the mod does after it would never run. Every other method keeps
// its real code.

// Hollow returns a copy of the .NET DLL b with its native methods given
// small IL bodies, and how many it changed.
func Hollow(b []byte) (out []byte, changed int, err error) {
	defer func() {
		if recover() != nil {
			out, changed, err = nil, 0, errors.New("malformed .NET DLL")
		}
	}()
	b = append([]byte(nil), b...)
	l, err := peLayoutOf(b)
	if err != nil {
		return nil, 0, err
	}
	md, _, _, err := metadataBlob(b)
	if err != nil {
		return nil, 0, err
	}
	_, m, err := parseMetadata(md) // its tables are slices of b: writes go to b
	if err != nil {
		return nil, 0, err
	}
	last := l.secs[len(l.secs)-1]
	for _, s := range l.secs {
		if s.raw > last.raw {
			last = s
		}
	}
	// The bodies go at the end of the last section, which grows.
	start := alignUp(maxU(last.vsize, last.rawSize), 4)
	var bodies []byte
	type patch struct{ row, rva int }
	var patches []patch
	for r := 1; r <= m.rows[tMethodDef]; r++ {
		rva, impl, flags := m.cell(tMethodDef, r, 0), m.cell(tMethodDef, r, 1), m.cell(tMethodDef, r, 2)
		native := impl&0x1000 != 0 || flags&0x2000 != 0 // InternalCall, PinvokeImpl
		if rva != 0 || !native || flags&0x0400 != 0 {   // abstract
			continue
		}
		il := m.stubIL(m.blobAt(m.cell(tMethodDef, r, 4)))
		for len(bodies)%4 != 0 {
			bodies = append(bodies, 0)
		}
		patches = append(patches, patch{r, int(last.va) + int(start) + len(bodies)})
		bodies = append(bodies, byte(len(il)<<2|2)) // tiny header
		bodies = append(bodies, il...)
		m.setCell(tMethodDef, r, 1, impl&^(0x1000|0x0003|0x0080)) // IL, managed; not PreserveSig
		m.setCell(tMethodDef, r, 2, flags&^0x2000)
	}
	if len(patches) == 0 {
		return b, 0, nil
	}
	for _, p := range patches {
		m.setCell(tMethodDef, p.row, 0, p.rva)
	}
	le := binary.LittleEndian
	// Drop anything after the last section (a signature): the file grows there.
	end := int(last.raw + last.rawSize)
	if end > len(b) {
		return nil, 0, errors.New("last section past the end of the file")
	}
	b = b[:end]
	if l.dirs+4*8+8 <= len(b) {
		le.PutUint64(b[l.dirs+4*8:], 0) // certificate table
	}
	vsize := start + uint32(len(bodies))
	rawSize := alignUp(vsize, l.fileAlign)
	b = append(b, make([]byte, int(last.raw+rawSize)-len(b))...)
	copy(b[last.raw+start:], bodies)
	le.PutUint32(b[last.hdr+8:], vsize)
	le.PutUint32(b[last.hdr+16:], rawSize)
	le.PutUint32(b[l.opt+56:], alignUp(last.va+vsize, l.secAlign)) // SizeOfImage
	return b, len(patches), nil
}

// stubIL is a body that returns the empty value of the signature's return
// type: nothing, 0, or null; it throws when it can't make one (a struct).
func (m *metadata) stubIL(sig []byte) []byte {
	const ret, ldnull, throw, ldc0 = 0x2A, 0x14, 0x7A, 0x16
	i := 1
	if len(sig) == 0 {
		return []byte{ldnull, throw}
	}
	skip := func(at int) int { _, n := compressed(sig[at:]); return at + n }
	if sig[0]&0x10 != 0 {
		i = skip(i) // generic parameter count
	}
	i = skip(i) // parameter count
	for i < len(sig) && (sig[i] == 0x1f || sig[i] == 0x20) {
		i = skip(i + 1) // custom modifier
	}
	if i >= len(sig) {
		return []byte{ldnull, throw}
	}
	switch e := sig[i]; {
	case e == 0x01:
		return []byte{ret}
	case e >= 0x02 && e <= 0x09:
		return []byte{ldc0, ret}
	case e == 0x0a || e == 0x0b:
		return []byte{ldc0, 0x6A, ret} // conv.i8
	case e == 0x0c:
		return []byte{0x22, 0, 0, 0, 0, ret}
	case e == 0x0d:
		return []byte{0x23, 0, 0, 0, 0, 0, 0, 0, 0, ret}
	case e == 0x18 || e == 0x19 || e == 0x0f || e == 0x1b:
		return []byte{ldc0, 0xD3, ret} // conv.i
	case e == 0x0e || e == 0x12 || e == 0x1c || e == 0x1d || e == 0x14:
		return []byte{ldnull, ret}
	case e == 0x15 && i+1 < len(sig) && sig[i+1] == 0x12:
		return []byte{ldnull, ret}
	case e == 0x11:
		tok, _ := compressed(sig[i+1:])
		if tok&3 == 0 && m.isEnum(tok>>2) { // a TypeDef here
			return []byte{ldc0, ret}
		}
	}
	return []byte{ldnull, throw}
}

// isEnum reports whether TypeDef row r extends System.Enum.
func (m *metadata) isEnum(r int) bool {
	if r < 1 || r > m.rows[tTypeDef] {
		return false
	}
	t, row := decode(kTypeDefOrRef, m.cell(tTypeDef, r, 3))
	return t == tTypeRef && m.typeRefName(row) == "System.Enum"
}

// setCell writes column c of row r (1-based) of table t.
func (m *metadata) setCell(t, r, c, v int) {
	at := (r - 1) * m.rowSize[t]
	for i := 0; i < c; i++ {
		at += m.colSize[t][i]
	}
	switch m.colSize[t][c] {
	case 2:
		binary.LittleEndian.PutUint16(m.tables[t][at:], uint16(v))
	case 4:
		binary.LittleEndian.PutUint32(m.tables[t][at:], uint32(v))
	}
}

type peLayout struct {
	opt, dirs           int
	fileAlign, secAlign uint32
	secs                []peSectionHdr
}

type peSectionHdr struct {
	hdr                     int // where its header is in the file
	va, vsize, raw, rawSize uint32
}

func peLayoutOf(b []byte) (*peLayout, error) {
	le := binary.LittleEndian
	if len(b) < 0x40 || b[0] != 'M' || b[1] != 'Z' {
		return nil, errNotCLR
	}
	pe := int(le.Uint32(b[0x3c:]))
	if pe+24 > len(b) || string(b[pe:pe+4]) != "PE\x00\x00" {
		return nil, errNotCLR
	}
	l := &peLayout{opt: pe + 24}
	nsec := int(le.Uint16(b[pe+6:]))
	optSize := int(le.Uint16(b[pe+20:]))
	if l.opt+optSize > len(b) || nsec == 0 {
		return nil, errNotCLR
	}
	switch le.Uint16(b[l.opt:]) {
	case 0x10b:
		l.dirs = l.opt + 96
	case 0x20b:
		l.dirs = l.opt + 112
	default:
		return nil, errNotCLR
	}
	l.secAlign, l.fileAlign = le.Uint32(b[l.opt+32:]), le.Uint32(b[l.opt+36:])
	if l.fileAlign == 0 || l.secAlign == 0 {
		return nil, errNotCLR
	}
	for i, at := 0, l.opt+optSize; i < nsec; i, at = i+1, at+40 {
		if at+40 > len(b) {
			return nil, errNotCLR
		}
		l.secs = append(l.secs, peSectionHdr{at, le.Uint32(b[at+12:]), le.Uint32(b[at+8:]), le.Uint32(b[at+20:]), le.Uint32(b[at+16:])})
	}
	return l, nil
}

func alignUp(v, a uint32) uint32 { return (v + a - 1) / a * a }

func maxU(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}
