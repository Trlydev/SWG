package game

import (
	"errors"
	"strings"
)

// VDF is a parsed Valve KeyValues text file (localconfig.vdf,
// loginusers.vdf): keys with either a string value or a nested block.
type VDF struct {
	Kids []VDFPair
}

// VDFPair is one key and its value (Node set for a block).
type VDFPair struct {
	Key   string
	Value string
	Node  *VDF
}

// Find follows keys (case-insensitive, as Steam treats them) down blocks.
func (v *VDF) Find(keys ...string) *VDF {
	cur := v
	for _, k := range keys {
		if cur == nil {
			return nil
		}
		var next *VDF
		for _, kv := range cur.Kids {
			if kv.Node != nil && strings.EqualFold(kv.Key, k) {
				next = kv.Node
				break
			}
		}
		cur = next
	}
	return cur
}

// Value is a string key's value in this block ("" if absent).
func (v *VDF) Value(key string) string {
	if v == nil {
		return ""
	}
	for _, kv := range v.Kids {
		if kv.Node == nil && strings.EqualFold(kv.Key, key) {
			return kv.Value
		}
	}
	return ""
}

// ParseVDF reads KeyValues text: "key" "value", "key" { … }, // comments,
// and backslash escapes in quoted strings.
func ParseVDF(s string) (*VDF, error) {
	p := &vdfParser{s: s}
	root, err := p.block(0)
	if err != nil {
		return nil, err
	}
	return root, nil
}

type vdfParser struct {
	s string
	i int
}

const maxVDFDepth = 64

func (p *vdfParser) block(depth int) (*VDF, error) {
	if depth > maxVDFDepth {
		return nil, errors.New("vdf: nested too deep")
	}
	v := &VDF{}
	for {
		tok, kind, err := p.next()
		if err != nil {
			return nil, err
		}
		switch kind {
		case 0: // end of input
			if depth > 0 {
				return nil, errors.New("vdf: unclosed block")
			}
			return v, nil
		case '}':
			if depth == 0 {
				return nil, errors.New("vdf: unexpected }")
			}
			return v, nil
		case '{':
			return nil, errors.New("vdf: block without a key")
		}
		val, vkind, err := p.next()
		if err != nil {
			return nil, err
		}
		switch vkind {
		case '"':
			v.Kids = append(v.Kids, VDFPair{Key: tok, Value: val})
		case '{':
			kid, err := p.block(depth + 1)
			if err != nil {
				return nil, err
			}
			v.Kids = append(v.Kids, VDFPair{Key: tok, Node: kid})
		default:
			return nil, errors.New("vdf: key without a value")
		}
	}
}

// next returns the next token: kind '"' (a string, quoted or bare), '{',
// '}', or 0 at the end.
func (p *vdfParser) next() (string, byte, error) {
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			p.i++
		case c == '/' && p.i+1 < len(p.s) && p.s[p.i+1] == '/':
			for p.i < len(p.s) && p.s[p.i] != '\n' {
				p.i++
			}
		case c == '{' || c == '}':
			p.i++
			return "", c, nil
		case c == '"':
			p.i++
			var b strings.Builder
			for p.i < len(p.s) {
				c := p.s[p.i]
				if c == '\\' && p.i+1 < len(p.s) {
					switch e := p.s[p.i+1]; e {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					default:
						b.WriteByte(e)
					}
					p.i += 2
					continue
				}
				if c == '"' {
					p.i++
					return b.String(), '"', nil
				}
				b.WriteByte(c)
				p.i++
			}
			return "", 0, errors.New("vdf: unterminated string")
		default: // a bare word
			start := p.i
			for p.i < len(p.s) && !strings.ContainsRune(" \t\r\n{}\"", rune(p.s[p.i])) {
				p.i++
			}
			return p.s[start:p.i], '"', nil
		}
	}
	return "", 0, nil
}
