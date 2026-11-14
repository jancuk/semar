package redact

import (
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// A view is a decoded form of a source text. Byte i of text was produced by the
// source unit that starts at at[i]. Units are contiguous and in order, so a unit
// ends where the next one starts. A nil at means the view is the source itself.
type view struct {
	text   string
	at     []int
	srcLen int
}

func sourceView(s string) view { return view{text: s, srcLen: len(s)} }

func (v view) source(i int) int {
	switch {
	case v.at == nil:
		return i
	case i == len(v.text):
		return v.srcLen
	default:
		return v.at[i]
	}
}

// sourceSpan maps text[i:j] to the whole source units that produced it.
func (v view) sourceSpan(i, j int) (int, int) {
	last := v.source(j - 1)
	end := v.srcLen
	for k := j; k < len(v.text); k++ {
		if s := v.source(k); s > last {
			end = s
			break
		}
	}
	return v.source(i), end
}

// percentDecoded decodes each %XX, and each + to a space when plus is set. A %
// that does not start an escape stays as it is, so one stray % does not hide
// an encoded secret elsewhere in the text.
func (v view) percentDecoded(plus bool) (view, bool) {
	t := v.text
	if !strings.Contains(t, "%") && !(plus && strings.Contains(t, "+")) {
		return v, false
	}
	b := make([]byte, 0, len(t))
	at := make([]int, 0, len(t))
	changed := false
	for i := 0; i < len(t); i++ {
		at = append(at, v.source(i))
		switch {
		case t[i] == '%' && i+2 < len(t) && isHex(t[i+1]) && isHex(t[i+2]):
			b = append(b, unhex(t[i+1])<<4|unhex(t[i+2]))
			i += 2
			changed = true
		case plus && t[i] == '+':
			b = append(b, ' ')
			changed = true
		default:
			b = append(b, t[i])
		}
	}
	return view{string(b), at, v.srcLen}, changed
}

// jsonUnescaped decodes the inside of a valid JSON string. Unlike
// encoding/json it keeps an invalid UTF-8 byte as it is, so a secret that holds
// one is still found.
func jsonUnescaped(s string) view {
	if !strings.Contains(s, `\`) {
		return sourceView(s)
	}
	b := make([]byte, 0, len(s))
	at := make([]int, 0, len(s))
	for i := 0; i < len(s); {
		start := i
		switch {
		case s[i] != '\\' || i+1 == len(s):
			b = append(b, s[i])
			i++
		case s[i+1] == 'u':
			r, n := unicodeEscape(s[i:])
			b = utf8.AppendRune(b, r)
			i += n
		default:
			b = append(b, simpleEscape(s[i+1]))
			i += 2
		}
		for len(at) < len(b) {
			at = append(at, start)
		}
	}
	return view{string(b), at, len(s)}
}

// unicodeEscape reads \uXXXX, or a surrogate pair \uXXXX\uXXXX, at the start
// of s and returns the rune and the bytes read. A lone surrogate becomes
// U+FFFD, as in encoding/json.
func unicodeEscape(s string) (rune, int) {
	hi, ok := hex4(s)
	if !ok {
		return utf8.RuneError, 2
	}
	if !utf16.IsSurrogate(hi) {
		return hi, 6
	}
	if lo, ok := hex4(s[6:]); ok {
		if r := utf16.DecodeRune(hi, lo); r != utf8.RuneError {
			return r, 12
		}
	}
	return utf8.RuneError, 6
}

func hex4(s string) (rune, bool) {
	if len(s) < 6 || s[0] != '\\' || s[1] != 'u' {
		return 0, false
	}
	n, err := strconv.ParseUint(s[2:6], 16, 32)
	return rune(n), err == nil
}

func simpleEscape(c byte) byte {
	switch c {
	case 'b':
		return '\b'
	case 'f':
		return '\f'
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	default:
		return c
	}
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c <= '9':
		return c - '0'
	case c <= 'F':
		return c - 'A' + 10
	default:
		return c - 'a' + 10
	}
}
