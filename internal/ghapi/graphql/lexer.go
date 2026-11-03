package graphql

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// tokKind is the kind of a lexical token of a GraphQL document.
type tokKind int

const (
	tokEOF tokKind = iota
	tokPunct
	tokName
	tokInt
	tokFloat
	tokString
)

// token is one lexical token; val is the punctuator, the name, the number
// as written or the decoded string.
type token struct {
	kind      tokKind
	val       string
	line, col int
}

// maxDocument bounds the size of a query document.
const maxDocument = 256 << 10

// syntaxError is a malformed document.
type syntaxError struct {
	msg       string
	line, col int
}

func (e *syntaxError) Error() string {
	return fmt.Sprintf("Parse error on line %d, column %d: %s", e.line, e.col, e.msg)
}

// lexer splits a document into tokens.
type lexer struct {
	src       string
	pos       int
	line, col int
}

func newLexer(src string) *lexer {
	src = strings.TrimPrefix(src, "\uFEFF")
	return &lexer{src: src, line: 1, col: 1}
}

func (l *lexer) errorf(format string, a ...any) error {
	return &syntaxError{msg: fmt.Sprintf(format, a...), line: l.line, col: l.col}
}

func (l *lexer) advance(n int) {
	for i := 0; i < n && l.pos < len(l.src); i++ {
		if l.src[l.pos] == '\n' {
			l.line, l.col = l.line+1, 1
		} else {
			l.col++
		}
		l.pos++
	}
}

// skip skips whitespace, commas and comments.
func (l *lexer) skip() {
	for l.pos < len(l.src) {
		switch c := l.src[l.pos]; c {
		case ' ', '\t', '\n', '\r', ',':
			l.advance(1)
		case '#':
			for l.pos < len(l.src) && l.src[l.pos] != '\n' && l.src[l.pos] != '\r' {
				l.advance(1)
			}
		default:
			return
		}
	}
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isNameChar(c byte) bool { return isNameStart(c) || (c >= '0' && c <= '9') }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// next returns the next token.
func (l *lexer) next() (token, error) {
	l.skip()
	t := token{line: l.line, col: l.col}
	if l.pos >= len(l.src) {
		t.kind = tokEOF
		return t, nil
	}
	c := l.src[l.pos]
	switch {
	case strings.IndexByte("!$&():=@[]{}|", c) >= 0:
		t.kind, t.val = tokPunct, string(c)
		l.advance(1)
		return t, nil
	case c == '.':
		if strings.HasPrefix(l.src[l.pos:], "...") {
			t.kind, t.val = tokPunct, "..."
			l.advance(3)
			return t, nil
		}
		return t, l.errorf("unexpected character %q", c)
	case isNameStart(c):
		start := l.pos
		for l.pos < len(l.src) && isNameChar(l.src[l.pos]) {
			l.advance(1)
		}
		t.kind, t.val = tokName, l.src[start:l.pos]
		return t, nil
	case c == '-' || isDigit(c):
		return l.number(t)
	case c == '"':
		if strings.HasPrefix(l.src[l.pos:], `"""`) {
			return l.blockString(t)
		}
		return l.str(t)
	}
	r, _ := utf8.DecodeRuneInString(l.src[l.pos:])
	return t, l.errorf("unexpected character %q", r)
}

func (l *lexer) number(t token) (token, error) {
	start := l.pos
	if l.src[l.pos] == '-' {
		l.advance(1)
	}
	digits := func() int {
		n := 0
		for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
			l.advance(1)
			n++
		}
		return n
	}
	if digits() == 0 {
		return t, l.errorf("invalid number")
	}
	t.kind = tokInt
	if l.pos < len(l.src) && l.src[l.pos] == '.' {
		l.advance(1)
		if digits() == 0 {
			return t, l.errorf("invalid number")
		}
		t.kind = tokFloat
	}
	if l.pos < len(l.src) && (l.src[l.pos] == 'e' || l.src[l.pos] == 'E') {
		l.advance(1)
		if l.pos < len(l.src) && (l.src[l.pos] == '+' || l.src[l.pos] == '-') {
			l.advance(1)
		}
		if digits() == 0 {
			return t, l.errorf("invalid number")
		}
		t.kind = tokFloat
	}
	if l.pos < len(l.src) && (isNameStart(l.src[l.pos]) || l.src[l.pos] == '.') {
		return t, l.errorf("invalid number")
	}
	t.val = l.src[start:l.pos]
	return t, nil
}

func (l *lexer) str(t token) (token, error) {
	l.advance(1)
	var b strings.Builder
	for {
		if l.pos >= len(l.src) {
			return t, l.errorf("unterminated string")
		}
		c := l.src[l.pos]
		switch c {
		case '"':
			l.advance(1)
			t.kind, t.val = tokString, b.String()
			return t, nil
		case '\n', '\r':
			return t, l.errorf("unterminated string")
		case '\\':
			if l.pos+1 >= len(l.src) {
				return t, l.errorf("unterminated string")
			}
			e := l.src[l.pos+1]
			l.advance(2)
			switch e {
			case '"', '\\', '/':
				b.WriteByte(e)
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'u':
				if l.pos+4 > len(l.src) {
					return t, l.errorf("invalid unicode escape")
				}
				r, ok := hex4(l.src[l.pos : l.pos+4])
				if !ok {
					return t, l.errorf("invalid unicode escape")
				}
				l.advance(4)
				// A surrogate pair is written as two escapes.
				if r >= 0xd800 && r < 0xdc00 && strings.HasPrefix(l.src[l.pos:], `\u`) && l.pos+6 <= len(l.src) {
					if lo, ok := hex4(l.src[l.pos+2 : l.pos+6]); ok && lo >= 0xdc00 && lo < 0xe000 {
						l.advance(6)
						r = (r-0xd800)<<10 + (lo - 0xdc00) + 0x10000
					}
				}
				b.WriteRune(r)
			default:
				return t, l.errorf("invalid escape \\%c", e)
			}
		default:
			_, size := utf8.DecodeRuneInString(l.src[l.pos:])
			b.WriteString(l.src[l.pos : l.pos+size])
			l.advance(size)
		}
	}
}

func (l *lexer) blockString(t token) (token, error) {
	l.advance(3)
	var b strings.Builder
	for {
		if l.pos >= len(l.src) {
			return t, l.errorf("unterminated block string")
		}
		if strings.HasPrefix(l.src[l.pos:], `"""`) {
			l.advance(3)
			t.kind, t.val = tokString, blockValue(b.String())
			return t, nil
		}
		if strings.HasPrefix(l.src[l.pos:], `\"""`) {
			b.WriteString(`"""`)
			l.advance(4)
			continue
		}
		b.WriteByte(l.src[l.pos])
		l.advance(1)
	}
}

// blockValue applies the indentation rules of block strings.
func blockValue(raw string) string {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n"), "\n")
	indent := -1
	for _, ln := range lines[1:] {
		t := strings.TrimLeft(ln, " \t")
		if t == "" {
			continue
		}
		if n := len(ln) - len(t); indent < 0 || n < indent {
			indent = n
		}
	}
	if indent > 0 {
		for i := 1; i < len(lines); i++ {
			if len(lines[i]) >= indent {
				lines[i] = lines[i][indent:]
			} else {
				lines[i] = strings.TrimLeft(lines[i], " \t")
			}
		}
	}
	for len(lines) > 0 && strings.TrimLeft(lines[0], " \t") == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimLeft(lines[len(lines)-1], " \t") == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// hex4 decodes four hexadecimal digits.
func hex4(s string) (rune, bool) {
	var r rune
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return r, len(s) == 4
}
