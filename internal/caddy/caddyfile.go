// Package caddy publishes ErvisioRepo behind a Caddy reverse proxy. It is a
// Go port of the flow used by ErvisioDrive (caddy-core.ts and caddy.ts), whose
// tokenizer is adapted from Ervisio/plugin-caddy (MIT License, Copyright (c)
// 2026 Fonlogen): the site lives in its own file next to the Caddyfile, loaded
// by one import line, or between marker comments inside the Caddyfile when the
// config folder of a container is not mounted. Every change is validated and
// reloaded, and rolled back when either fails.
package caddy

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Markers written in the files this package manages.
const (
	// Marker is the first line of every block this package writes.
	Marker = "# Managed by the ErvisioRepo plugin"
	// MarkerHint is the second line: a hint for people who open the file.
	MarkerHint = "# Change it with `ervisio-repo caddy setup`: edits made here are replaced."
	// EndMarker closes a block kept inside the main Caddyfile.
	EndMarker = "# End of the ErvisioRepo block"
	// ImportComment is written above the import line.
	ImportComment = "# Added by the ErvisioRepo plugin"
	// FileName is the file the site block lives in, next to the Caddyfile.
	FileName = "ervisio-repo.caddy"
)

// Token is one word, quoted string or heredoc of a Caddyfile.
type Token struct {
	Text    string
	Line    int // 0-based start line
	EndLine int
	Quoted  bool
}

var isLetter = regexp.MustCompile(`^[A-Za-z]$`)

// Tokenize splits a Caddyfile into tokens. It fails on an unterminated string
// or heredoc.
func Tokenize(text string) ([]Token, error) {
	src := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	n := len(src)
	var out []Token
	i, line := 0, 0
	for i < n {
		c := src[i]
		switch c {
		case '\n':
			line++
			i++
			continue
		case ' ', '\t':
			i++
			continue
		case '#':
			for i < n && src[i] != '\n' {
				i++
			}
			continue
		}
		start := line
		if c == '"' || c == '`' {
			j := i + 1
			var b strings.Builder
			for j < n && src[j] != c {
				if c == '"' && src[j] == '\\' && j+1 < n {
					if src[j+1] == '\n' {
						line++
					}
					b.WriteByte(src[j+1])
					j += 2
					continue
				}
				if src[j] == '\n' {
					line++
				}
				b.WriteByte(src[j])
				j++
			}
			if j >= n {
				return nil, fmt.Errorf("unterminated string on line %d", start+1)
			}
			out = append(out, Token{b.String(), start, line, true})
			i = j + 1
			continue
		}
		if c == '<' && i+2 < n && src[i+1] == '<' && isLetter.MatchString(src[i+2:i+3]) {
			j := i + 2
			for j < n && (src[j] == '_' || src[j] >= '0' && src[j] <= '9' || isLetter.MatchString(src[j:j+1])) {
				j++
			}
			marker := src[i+2 : j]
			endRe := regexp.MustCompile(`^[ \t]*` + regexp.QuoteMeta(marker))
			k := strings.IndexByte(src[j:], '\n')
			if k >= 0 {
				k += j
			}
			closed := false
			var body []string
			resume := n
			for k >= 0 && k < n {
				line++
				e := strings.IndexByte(src[k+1:], '\n')
				if e >= 0 {
					e += k + 1
				}
				end := e
				if e < 0 {
					end = n
				}
				l := src[k+1 : end]
				if loc := endRe.FindStringIndex(l); loc != nil && (loc[1] == len(l) || l[loc[1]] == ' ' || l[loc[1]] == '\t') {
					closed = true
					resume = k + 1 + loc[1]
					break
				}
				body = append(body, l)
				if e < 0 {
					k = n
				} else {
					k = e
				}
			}
			if !closed {
				return nil, fmt.Errorf("unterminated heredoc on line %d", start+1)
			}
			out = append(out, Token{strings.Join(body, "\n"), start, line, true})
			i = resume
			continue
		}
		j := i
		for j < n && src[j] != ' ' && src[j] != '\t' && src[j] != '\n' {
			j++
		}
		out = append(out, Token{src[i:j], line, line, false})
		i = j
	}
	return out, nil
}

// Entry kinds.
const (
	KindGlobal  = "global"
	KindSnippet = "snippet"
	KindSite    = "site"
	KindImport  = "import"
	KindOther   = "other"
)

// Entry is one top-level directive or block of a Caddyfile.
type Entry struct {
	Kind string
	// Head holds the tokens before the opening brace (or the directive line).
	Head []string
	// Start and End are the 0-based first and last lines.
	Start, End int
}

// Outline is the top-level structure of a Caddyfile.
type Outline struct {
	Entries []Entry
	Sites   []Entry
	// Imports are the arguments of top-level import lines that name files or
	// globs (snippet imports left out).
	Imports  []string
	Snippets []string
	// Braceless is set when the file has top-level directives that are not
	// imports: a single site written without braces. Adding a site would change
	// its meaning, so such a file is never edited.
	Braceless bool
}

var placeholderEnd = regexp.MustCompile(`\{[^}]*\}$`)

// Scan reads the top-level structure of a Caddyfile, or fails when it cannot
// be read safely. Nothing is ever written to a file that fails here.
func Scan(text string) (*Outline, error) {
	tokens, err := Tokenize(text)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	depth := 0
	var head []Token
	blockStart := -1
	var blockHead []string
	texts := func(ts []Token) []string {
		r := make([]string, len(ts))
		for i, t := range ts {
			r[i] = t.Text
		}
		return r
	}
	flush := func() {
		if len(head) == 0 {
			return
		}
		kind := KindOther
		if head[0].Text == "import" && !head[0].Quoted {
			kind = KindImport
		}
		entries = append(entries, Entry{kind, texts(head), head[0].Line, head[len(head)-1].EndLine})
		head = nil
	}
	for _, t := range tokens {
		open := !t.Quoted && t.Text == "{"
		cl := !t.Quoted && t.Text == "}"
		if !t.Quoted && !open && !cl && depth == 0 &&
			(strings.HasSuffix(t.Text, "{") || strings.HasSuffix(t.Text, "}")) && !placeholderEnd.MatchString(t.Text) {
			return nil, fmt.Errorf("line %d: %q needs a space before the brace", t.Line+1, t.Text)
		}
		if depth == 0 {
			if open {
				if len(head) > 0 {
					blockStart = head[0].Line
				} else {
					blockStart = t.Line
				}
				blockHead = texts(head)
				head = nil
				depth = 1
				continue
			}
			if cl {
				return nil, fmt.Errorf("line %d: \"}\" without a matching \"{\"", t.Line+1)
			}
			if len(head) > 0 && t.Line != head[len(head)-1].EndLine {
				flush()
			}
			head = append(head, t)
			continue
		}
		if open {
			depth++
		} else if cl {
			depth--
			if depth == 0 {
				kind := KindSite
				name := ""
				if len(blockHead) > 0 {
					name = blockHead[0]
				}
				if len(blockHead) == 0 {
					kind = KindGlobal
				} else if len(name) > 2 && name[0] == '(' && name[len(name)-1] == ')' {
					kind = KindSnippet
				}
				entries = append(entries, Entry{kind, blockHead, blockStart, t.Line})
				blockHead = nil
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("a block opened on line %d is never closed", blockStart+1)
	}
	flush()
	o := &Outline{Entries: entries}
	for _, e := range entries {
		switch e.Kind {
		case KindSnippet:
			o.Snippets = append(o.Snippets, e.Head[0][1:len(e.Head[0])-1])
		case KindSite:
			o.Sites = append(o.Sites, e)
		case KindOther:
			o.Braceless = true
		}
	}
	for _, e := range entries {
		if e.Kind != KindImport || len(e.Head) < 2 || contains(o.Snippets, e.Head[1]) {
			continue
		}
		o.Imports = append(o.Imports, e.Head[1])
	}
	return o, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

var schemePrefix = regexp.MustCompile(`^[a-z][a-z0-9+.-]*://`)
var pathSuffix = regexp.MustCompile(`/.*$`)
var portSuffix = regexp.MustCompile(`:\d+$`)

// HostsOf returns the host names of a site header: "a.com, b.com" gives
// ["a.com", "b.com"], with schemes, ports and paths removed.
func HostsOf(head []string) []string {
	var out []string
	for _, a := range strings.FieldsFunc(strings.Join(head, " "), func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	}) {
		a = strings.ToLower(strings.TrimSpace(a))
		a = schemePrefix.ReplaceAllString(a, "")
		a = pathSuffix.ReplaceAllString(a, "")
		a = portSuffix.ReplaceAllString(a, "")
		if a != "" {
			out = append(out, a)
		}
	}
	return out
}

// SiteHosts lists every host name the sites of an outline answer to.
func SiteHosts(o *Outline) []string {
	var hosts []string
	for _, s := range o.Sites {
		hosts = append(hosts, HostsOf(s.Head)...)
	}
	if o.Braceless {
		for _, e := range o.Entries {
			if e.Kind == KindGlobal {
				continue
			}
			if e.Kind == KindOther {
				hosts = append(HostsOf(e.Head), hosts...)
			}
			break
		}
	}
	return hosts
}

// EditBlocker says why a Caddyfile must not be edited, or "" when it can be.
func EditBlocker(text string) string {
	o, err := Scan(text)
	if err != nil {
		return err.Error()
	}
	if o.Braceless {
		return "the Caddyfile defines its site without braces, so another site cannot be added safely"
	}
	return ""
}

// HostTaken reports whether host is already an address of hosts, exactly or
// through a wildcard of the same level.
func HostTaken(hosts []string, host string) bool {
	h := strings.ToLower(host)
	parent := h[strings.IndexByte(h, '.')+1:]
	for _, x := range hosts {
		x = strings.ToLower(x)
		if x == h || x == "*."+parent {
			return true
		}
	}
	return false
}

func dirOf(p string) string {
	i := strings.LastIndexByte(p, '/')
	switch {
	case i < 0:
		return "."
	case i == 0:
		return "/"
	}
	return p[:i]
}

func globToRe(glob string) *regexp.Regexp {
	esc := regexp.QuoteMeta(glob)
	esc = strings.ReplaceAll(esc, `\*`, `[^/]*`)
	esc = strings.ReplaceAll(esc, `\?`, `[^/]`)
	return regexp.MustCompile("^" + esc + "$")
}

// ImportsCovering returns the import arguments of o (relative to mainPath's
// folder) that already load file.
func ImportsCovering(o *Outline, mainPath, file string) []string {
	dir := dirOf(mainPath)
	var out []string
	for _, imp := range o.Imports {
		abs := imp
		if !strings.HasPrefix(imp, "/") {
			abs = dir + "/" + imp
		}
		abs = strings.ReplaceAll(abs, "/./", "/")
		if globToRe(abs).MatchString(file) {
			out = append(out, imp)
		}
	}
	return out
}

// importArg is the argument written: relative when file sits next to mainPath.
func importArg(mainPath, file string) string {
	if dirOf(file) == dirOf(mainPath) {
		return file[strings.LastIndexByte(file, '/')+1:]
	}
	return file
}

// EnsureImport appends "import <file>" to the main Caddyfile, once. The text is
// unchanged when an import (a glob included) already loads the file.
func EnsureImport(main, mainPath, file string) (string, error) {
	if b := EditBlocker(main); b != "" {
		return "", errors.New(b)
	}
	o, err := Scan(main)
	if err != nil {
		return "", err
	}
	if len(ImportsCovering(o, mainPath, file)) > 0 {
		return main, nil
	}
	base := strings.TrimRight(main, " \t\r\n")
	add := ImportComment + "\nimport " + importArg(mainPath, file) + "\n"
	if base == "" {
		return add, nil
	}
	return base + "\n\n" + add, nil
}

// RemoveImport removes the import lines that load exactly file (not globs),
// the comment written above them and the blank line before it.
func RemoveImport(main, mainPath, file string) (string, error) {
	o, err := Scan(main)
	if err != nil {
		return "", err
	}
	arg := importArg(mainPath, file)
	want := map[string]bool{file: true, arg: true, "./" + arg: true}
	lines := strings.Split(main, "\n")
	drop := map[int]bool{}
	for _, e := range o.Entries {
		if e.Kind != KindImport || len(e.Head) != 2 || !want[e.Head[1]] || e.Start != e.End {
			continue
		}
		drop[e.Start] = true
		if k := e.Start - 1; k >= 0 && strings.TrimSpace(lines[k]) == ImportComment {
			drop[k] = true
			if k > 0 && strings.TrimSpace(lines[k-1]) == "" {
				drop[k-1] = true
			}
		}
	}
	if len(drop) == 0 {
		return main, nil
	}
	kept := make([]string, 0, len(lines))
	for i, l := range lines {
		if !drop[i] {
			kept = append(kept, l)
		}
	}
	text := strings.Join(kept, "\n")
	if strings.HasSuffix(main, "\n") && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text, nil
}

func normalizeNL(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// findInline returns the line range of the inline block (Marker to EndMarker).
// ok is false when there is none; it fails when the markers do not pair up.
func findInline(main string) (start, end int, ok bool, err error) {
	var starts, ends []int
	for i, l := range strings.Split(normalizeNL(main), "\n") {
		switch strings.TrimSpace(l) {
		case Marker:
			starts = append(starts, i)
		case EndMarker:
			ends = append(ends, i)
		}
	}
	if len(starts) == 0 && len(ends) == 0 {
		return 0, 0, false, nil
	}
	if len(starts) != 1 || len(ends) != 1 || ends[0] < starts[0] {
		return 0, 0, false, errors.New("the ErvisioRepo markers in the Caddyfile do not pair up")
	}
	return starts[0], ends[0], true, nil
}

// ReadInline returns the inline block's text, or "" when there is none.
func ReadInline(main string) (string, error) {
	s, e, ok, err := findInline(main)
	if err != nil || !ok {
		return "", err
	}
	return strings.Join(strings.Split(normalizeNL(main), "\n")[s:e+1], "\n") + "\n", nil
}

// UpsertInline puts block (from GenerateBlock with inline) in place of the
// inline block, or at the end of the Caddyfile.
func UpsertInline(main, block string) (string, error) {
	if b := EditBlocker(main); b != "" {
		return "", errors.New(b)
	}
	s, e, ok, err := findInline(main)
	if err != nil {
		return "", err
	}
	lines := strings.Split(main, "\n")
	if ok {
		before := strings.Join(lines[:s], "\n")
		after := strings.Join(lines[e+1:], "\n")
		if before != "" {
			before += "\n"
		}
		return before + block + after, nil
	}
	base := strings.TrimRight(main, " \t\r\n")
	if base == "" {
		return block, nil
	}
	return base + "\n\n" + block, nil
}

// RemoveInline removes the inline block and the blank line before it.
func RemoveInline(main string) (string, error) {
	s, e, ok, err := findInline(main)
	if err != nil || !ok {
		return main, err
	}
	lines := strings.Split(main, "\n")
	if s > 0 && strings.TrimSpace(lines[s-1]) == "" {
		s--
	}
	text := strings.Join(append(append([]string{}, lines[:s]...), lines[e+1:]...), "\n")
	if strings.HasSuffix(main, "\n") && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text, nil
}
