package scan

import (
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

// Reading a compiled method's IL without running it. The metadata tells what
// a DLL could reach; the IL tells which method reaches it and which string
// it hands a reflection call, so a name built or decoded at run time and
// then looked up can be tied to the lookup, as in C# source.

// Method is one method body, as its calls and string loads in order.
type Method struct {
	Name string // "DeclaringType.method"
	Ops  []MethodOp
	// XORChars: XOR followed by a cast to char, inside a loop (decoding
	// text). XORBytes: the same with a cast to byte (decoding data, or a
	// hash).
	XORChars, XORBytes bool
}

// MethodOp is a loaded string literal (Str) or a call target (Call). Exactly
// one field is set.
type MethodOp struct {
	Str  string
	Call string // "Type.Name"
}

// readMethods reads every method body in the file.
func (m *metadata) readMethods(b []byte, l *peLayout) []Method {
	m.methodOwner = m.methodOwners()
	var out []Method
	for r := 1; r <= m.rows[tMethodDef] && len(out) < 20000; r++ {
		rva := m.cell(tMethodDef, r, 0)
		if rva == 0 {
			continue
		}
		off, ok := l.rva2off(uint32(rva))
		if !ok {
			continue
		}
		code, ok := methodCode(b, off)
		if !ok {
			continue
		}
		ops, xc, xb := m.readIL(code)
		if len(ops) == 0 && !xc && !xb {
			continue
		}
		out = append(out, Method{Name: m.methodOwner[r] + "." + m.str(m.cell(tMethodDef, r, 3)), Ops: ops, XORChars: xc, XORBytes: xb})
	}
	return out
}

// methodOwners maps a MethodDef row to its declaring type's name, from the
// TypeDef method ranges.
func (m *metadata) methodOwners() map[int]string {
	owner := map[int]string{}
	n := m.rows[tTypeDef]
	for t := 1; t <= n; t++ {
		first := m.cell(tTypeDef, t, 5)
		last := m.rows[tMethodDef] + 1
		if t < n {
			last = m.cell(tTypeDef, t+1, 5)
		}
		name := m.typeDefName(t)
		for r := first; r >= 1 && r < last; r++ {
			owner[r] = name
		}
	}
	return owner
}

func (m *metadata) typeDefName(r int) string {
	if r < 1 || r > m.rows[tTypeDef] {
		return ""
	}
	name := m.str(m.cell(tTypeDef, r, 1))
	if ns := m.str(m.cell(tTypeDef, r, 2)); ns != "" {
		return ns + "." + name
	}
	return name
}

// methodCode returns a method body's IL bytes (tiny or fat header).
func methodCode(b []byte, off int) ([]byte, bool) {
	if off < 0 || off >= len(b) {
		return nil, false
	}
	switch h := b[off]; h & 3 {
	case 2: // tiny: size in the top 6 bits, code right after
		size := int(h >> 2)
		s := off + 1
		if s+size > len(b) {
			return nil, false
		}
		return b[s : s+size], true
	case 3: // fat: 12-byte (or larger) header, code size at +4
		if off+12 > len(b) {
			return nil, false
		}
		hsize := int(binary.LittleEndian.Uint16(b[off:])>>12) * 4
		size := int(binary.LittleEndian.Uint32(b[off+4:]))
		s := off + hsize
		if hsize < 12 || size < 0 || s+size > len(b) {
			return nil, false
		}
		return b[s : s+size], true
	}
	return nil, false
}

func (l *peLayout) rva2off(rva uint32) (int, bool) {
	for _, s := range l.secs {
		size := s.vsize
		if s.rawSize > size {
			size = s.rawSize
		}
		if rva >= s.va && rva < s.va+size {
			o := int(rva-s.va) + int(s.raw)
			return o, o >= 0
		}
	}
	return 0, false
}

// readIL walks a method's IL and keeps its ldstr strings and call targets in
// order. It decodes each opcode's length so operand bytes are never mistaken
// for opcodes. It also notes XOR followed closely by a cast to char or byte
// in a method that loops (a branch backwards).
func (m *metadata) readIL(code []byte) (ops []MethodOp, xorChars, xorBytes bool) {
	lastXOR := -10 // opcode count at the last xor
	back := false
	var xc, xb bool
	for p, n := 0, 0; p < len(code); n++ {
		op := code[p]
		if op == 0xFE { // two-byte opcode
			if p+1 >= len(code) {
				break
			}
			p += 2 + feOperand[code[p+1]]
			continue
		}
		switch {
		case op == 0x61: // xor
			lastXOR = n
		case (op == 0xD1 || op == 0x68) && n-lastXOR <= 3: // conv.u2, conv.i2: char
			xc = true
		case (op == 0xD2 || op == 0x67) && n-lastXOR <= 3: // conv.u1, conv.i1: byte
			xb = true
		case op >= 0x2B && op <= 0x37 && p+1 < len(code) && int8(code[p+1]) < 0:
			back = true
		case op >= 0x38 && op <= 0x44 && p+5 <= len(code) && int32(binary.LittleEndian.Uint32(code[p+1:])) < 0:
			back = true
		}
		switch ol := opOperand[op]; {
		case ol == opSwitch:
			if p+5 > len(code) {
				return ops, xc && back, xb && back
			}
			p += 5 + 4*int(binary.LittleEndian.Uint32(code[p+1:]))
		case op == 0x72: // ldstr
			if p+5 > len(code) {
				return ops, xc && back, xb && back
			}
			if s, ok := m.usString(int(binary.LittleEndian.Uint32(code[p+1:]) & 0xffffff)); ok {
				ops = append(ops, MethodOp{Str: s})
			}
			p += 5
		case op == 0x28 || op == 0x6F || op == 0x73: // call, callvirt, newobj
			if p+5 > len(code) {
				return ops, xc && back, xb && back
			}
			if nm := m.tokenName(binary.LittleEndian.Uint32(code[p+1:])); nm != "" {
				ops = append(ops, MethodOp{Call: nm})
			}
			p += 5
		default:
			p += 1 + int(ol)
		}
	}
	return ops, xc && back, xb && back
}

// JoinedStrings returns the method's string literals with String.Concat of
// literals joined, each with the call that takes it right after (if any).
func (mt Method) JoinedStrings() []JoinedString {
	var out []JoinedString
	var pending []string // literals since the last call
	for _, op := range mt.Ops {
		if op.Call == "" {
			pending = append(pending, op.Str)
			continue
		}
		if op.Call == "System.String.Concat" && len(pending) >= 2 {
			pending = []string{strings.Join(pending, "")}
			continue
		}
		for i, s := range pending {
			used := ""
			if i == len(pending)-1 {
				used = op.Call
			}
			out = append(out, JoinedString{s, used})
		}
		pending = pending[:0]
	}
	for _, s := range pending {
		out = append(out, JoinedString{s, ""})
	}
	return out
}

// JoinedString is text a method loads or builds, and the call that takes it.
type JoinedString struct {
	Text, UsedBy string
}

// tokenName resolves a method token (call/callvirt/newobj) to "Type.Name".
func (m *metadata) tokenName(tok uint32) string {
	row := int(tok & 0xffffff)
	switch tok >> 24 {
	case 0x0A: // MemberRef
		return m.memberRefName(row)
	case 0x06: // MethodDef
		return m.methodDefFullName(row)
	case 0x2B: // MethodSpec -> its method
		if row < 1 || row > m.rows[tMethodSpec] {
			return ""
		}
		t, x := decode(kMethodDefOrRef, m.cell(tMethodSpec, row, 0))
		switch t {
		case tMethodDef:
			return m.methodDefFullName(x)
		case tMemberRef:
			return m.memberRefName(x)
		}
	}
	return ""
}

func (m *metadata) memberRefName(r int) string {
	if r < 1 || r > m.rows[tMemberRef] {
		return ""
	}
	t, x := decode(kMemberRefParent, m.cell(tMemberRef, r, 0))
	owner := ""
	switch t {
	case tTypeRef:
		owner = m.typeRefName(x)
	case tTypeSpec:
		owner = m.typeSpecName(x)
	case tTypeDef:
		owner = m.typeDefName(x)
	}
	name := m.str(m.cell(tMemberRef, r, 1))
	if owner == "" {
		return name
	}
	return clrName(owner) + "." + name
}

func (m *metadata) methodDefFullName(r int) string {
	if r < 1 || r > m.rows[tMethodDef] {
		return ""
	}
	return clrName(m.methodOwner[r]) + "." + m.str(m.cell(tMethodDef, r, 3))
}

// usString decodes one #US entry (length-prefixed UTF-16, trailing flag).
func (m *metadata) usString(off int) (string, bool) {
	if off <= 0 || off >= len(m.us) {
		return "", false
	}
	n, k := compressed(m.us[off:])
	if k == 0 || n < 1 || off+k+n > len(m.us) {
		return "", false
	}
	raw := m.us[off+k : off+k+n-1]
	u := make([]uint16, len(raw)/2)
	for j := range u {
		u[j] = binary.LittleEndian.Uint16(raw[2*j:])
	}
	return string(utf16.Decode(u)), true
}

// opSwitch marks the one variable-length opcode.
const opSwitch = -1

// opOperand is the operand length of each single-byte IL opcode; every
// opcode not listed takes no operand (0).
var opOperand = func() [256]int8 {
	o := [256]int8{}
	one := []byte{0x0E, 0x0F, 0x10, 0x11, 0x12, 0x13, 0x1F, 0x2B, 0x2C, 0x2D, 0x2E, 0x2F,
		0x30, 0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37, 0xDE}
	four := []byte{0x20, 0x22, 0x27, 0x28, 0x29, 0x38, 0x39, 0x3A, 0x3B, 0x3C, 0x3D, 0x3E, 0x3F,
		0x40, 0x41, 0x42, 0x43, 0x44, 0x6F, 0x70, 0x71, 0x72, 0x73, 0x74, 0x75, 0x79,
		0x7B, 0x7C, 0x7D, 0x7E, 0x7F, 0x80, 0x81, 0x8C, 0x8D, 0x8F, 0xA3, 0xA4, 0xA5, 0xC2, 0xC6, 0xD0, 0xDD}
	for _, b := range one {
		o[b] = 1
	}
	for _, b := range four {
		o[b] = 4
	}
	o[0x21], o[0x23] = 8, 8 // ldc.i8, ldc.r8
	o[0x45] = opSwitch
	return o
}()

// feOperand is the operand length of each 0xFE-prefixed opcode.
var feOperand = func() [256]int {
	o := [256]int{}
	for _, b := range []byte{0x06, 0x07, 0x15, 0x16, 0x1C} { // ldftn, ldvirtftn, initobj, constrained, sizeof
		o[b] = 4
	}
	for _, b := range []byte{0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E} { // ldarg..stloc (wide)
		o[b] = 2
	}
	o[0x19] = 1 // no.
	return o
}()
