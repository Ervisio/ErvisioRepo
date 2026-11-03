package graphql

import "fmt"

// The AST of an executable document (no type system definitions).

type document struct {
	ops   []*operation
	frags map[string]*fragmentDef
}

type operation struct {
	kind string // query or mutation
	name string
	vars []varDef
	sel  []selection
}

type varDef struct {
	name   string
	list   bool // the type is a list
	def    *value
	hasDef bool
}

// selection is a *field, a *fragmentSpread or an *inlineFragment.
type selection any

type field struct {
	alias, name string
	args        []argument
	dirs        []directive
	sel         []selection
	line, col   int
}

func (f *field) key() string {
	if f.alias != "" {
		return f.alias
	}
	return f.name
}

type fragmentSpread struct {
	name string
	dirs []directive
}

type inlineFragment struct {
	on   string // empty: no type condition
	dirs []directive
	sel  []selection
}

type fragmentDef struct {
	name, on string
	sel      []selection
}

type argument struct {
	name string
	val  value
}

type directive struct {
	name string
	args []argument
}

type valueKind int

const (
	valVar valueKind = iota
	valInt
	valFloat
	valString
	valBool
	valNull
	valEnum
	valList
	valObject
)

type value struct {
	kind valueKind
	s    string // variable name, number as written, string, enum, "true"/"false"
	list []value
	obj  []argument
}

// Limits of a parsed document.
const (
	maxDepth  = 40
	maxTokens = 20000
)

type parser struct {
	lx     *lexer
	tok    token
	depth  int
	tokens int
}

// parse parses an executable document.
func parse(src string) (*document, error) {
	if len(src) > maxDocument {
		return nil, &syntaxError{msg: "the query is too large", line: 1, col: 1}
	}
	p := &parser{lx: newLexer(src)}
	if err := p.advance(); err != nil {
		return nil, err
	}
	doc := &document{frags: map[string]*fragmentDef{}}
	for p.tok.kind != tokEOF {
		switch {
		case p.isPunct("{"):
			sel, err := p.selectionSet()
			if err != nil {
				return nil, err
			}
			doc.ops = append(doc.ops, &operation{kind: "query", sel: sel})
		case p.tok.kind == tokName && (p.tok.val == "query" || p.tok.val == "mutation" || p.tok.val == "subscription"):
			op, err := p.operation()
			if err != nil {
				return nil, err
			}
			doc.ops = append(doc.ops, op)
		case p.tok.kind == tokName && p.tok.val == "fragment":
			f, err := p.fragment()
			if err != nil {
				return nil, err
			}
			if _, dup := doc.frags[f.name]; dup {
				return nil, fmt.Errorf("there can be only one fragment named %q", f.name)
			}
			doc.frags[f.name] = f
		default:
			return nil, p.unexpected()
		}
	}
	if len(doc.ops) == 0 {
		return nil, &syntaxError{msg: "the document has no operation", line: 1, col: 1}
	}
	return doc, nil
}

func (p *parser) advance() error {
	p.tokens++
	if p.tokens > maxTokens {
		return &syntaxError{msg: "the query has too many tokens", line: p.tok.line, col: p.tok.col}
	}
	t, err := p.lx.next()
	if err != nil {
		return err
	}
	p.tok = t
	return nil
}

func (p *parser) isPunct(s string) bool { return p.tok.kind == tokPunct && p.tok.val == s }

func (p *parser) unexpected() error {
	what := p.tok.val
	switch p.tok.kind {
	case tokEOF:
		what = "end of file"
	case tokString:
		what = fmt.Sprintf("%q", p.tok.val)
	}
	return &syntaxError{msg: fmt.Sprintf("unexpected %s", what), line: p.tok.line, col: p.tok.col}
}

func (p *parser) expect(s string) error {
	if !p.isPunct(s) {
		return &syntaxError{msg: fmt.Sprintf("expected %q, got %s", s, p.describe()), line: p.tok.line, col: p.tok.col}
	}
	return p.advance()
}

func (p *parser) describe() string {
	switch p.tok.kind {
	case tokEOF:
		return "end of file"
	case tokString:
		return fmt.Sprintf("%q", p.tok.val)
	}
	return p.tok.val
}

func (p *parser) name() (string, error) {
	if p.tok.kind != tokName {
		return "", &syntaxError{msg: fmt.Sprintf("expected a name, got %s", p.describe()), line: p.tok.line, col: p.tok.col}
	}
	n := p.tok.val
	return n, p.advance()
}

func (p *parser) operation() (*operation, error) {
	op := &operation{kind: p.tok.val}
	if err := p.advance(); err != nil {
		return nil, err
	}
	if p.tok.kind == tokName {
		op.name = p.tok.val
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	if p.isPunct("(") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		for !p.isPunct(")") {
			v, err := p.varDef()
			if err != nil {
				return nil, err
			}
			op.vars = append(op.vars, v)
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	if _, err := p.directives(); err != nil {
		return nil, err
	}
	sel, err := p.selectionSet()
	if err != nil {
		return nil, err
	}
	op.sel = sel
	return op, nil
}

func (p *parser) varDef() (varDef, error) {
	var v varDef
	if err := p.expect("$"); err != nil {
		return v, err
	}
	n, err := p.name()
	if err != nil {
		return v, err
	}
	v.name = n
	if err := p.expect(":"); err != nil {
		return v, err
	}
	v.list = p.isPunct("[")
	if err := p.typeRef(0); err != nil {
		return v, err
	}
	if p.isPunct("=") {
		if err := p.advance(); err != nil {
			return v, err
		}
		d, err := p.value(true)
		if err != nil {
			return v, err
		}
		v.def, v.hasDef = &d, true
	}
	_, err = p.directives()
	return v, err
}

func (p *parser) typeRef(depth int) error {
	if depth > maxDepth {
		return &syntaxError{msg: "the type is nested too deeply", line: p.tok.line, col: p.tok.col}
	}
	if p.isPunct("[") {
		if err := p.advance(); err != nil {
			return err
		}
		if err := p.typeRef(depth + 1); err != nil {
			return err
		}
		if err := p.expect("]"); err != nil {
			return err
		}
	} else if _, err := p.name(); err != nil {
		return err
	}
	if p.isPunct("!") {
		return p.advance()
	}
	return nil
}

func (p *parser) fragment() (*fragmentDef, error) {
	if err := p.advance(); err != nil {
		return nil, err
	}
	n, err := p.name()
	if err != nil {
		return nil, err
	}
	if n == "on" {
		return nil, &syntaxError{msg: "a fragment cannot be named \"on\"", line: p.tok.line, col: p.tok.col}
	}
	if p.tok.kind != tokName || p.tok.val != "on" {
		return nil, &syntaxError{msg: "expected \"on\"", line: p.tok.line, col: p.tok.col}
	}
	if err := p.advance(); err != nil {
		return nil, err
	}
	on, err := p.name()
	if err != nil {
		return nil, err
	}
	if _, err := p.directives(); err != nil {
		return nil, err
	}
	sel, err := p.selectionSet()
	if err != nil {
		return nil, err
	}
	return &fragmentDef{name: n, on: on, sel: sel}, nil
}

func (p *parser) selectionSet() ([]selection, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxDepth {
		return nil, &syntaxError{msg: "the query is nested too deeply", line: p.tok.line, col: p.tok.col}
	}
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	var out []selection
	for !p.isPunct("}") {
		if p.tok.kind == tokEOF {
			return nil, p.unexpected()
		}
		s, err := p.selection()
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, &syntaxError{msg: "empty selection set", line: p.tok.line, col: p.tok.col}
	}
	return out, p.advance()
}

func (p *parser) selection() (selection, error) {
	if p.isPunct("...") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.tok.kind == tokName && p.tok.val != "on" {
			n := p.tok.val
			if err := p.advance(); err != nil {
				return nil, err
			}
			dirs, err := p.directives()
			if err != nil {
				return nil, err
			}
			return &fragmentSpread{name: n, dirs: dirs}, nil
		}
		f := &inlineFragment{}
		if p.tok.kind == tokName && p.tok.val == "on" {
			if err := p.advance(); err != nil {
				return nil, err
			}
			on, err := p.name()
			if err != nil {
				return nil, err
			}
			f.on = on
		}
		dirs, err := p.directives()
		if err != nil {
			return nil, err
		}
		f.dirs = dirs
		sel, err := p.selectionSet()
		if err != nil {
			return nil, err
		}
		f.sel = sel
		return f, nil
	}
	f := &field{line: p.tok.line, col: p.tok.col}
	n, err := p.name()
	if err != nil {
		return nil, err
	}
	if p.isPunct(":") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		f.alias = n
		if n, err = p.name(); err != nil {
			return nil, err
		}
	}
	f.name = n
	if f.args, err = p.arguments(false); err != nil {
		return nil, err
	}
	if f.dirs, err = p.directives(); err != nil {
		return nil, err
	}
	if p.isPunct("{") {
		if f.sel, err = p.selectionSet(); err != nil {
			return nil, err
		}
	}
	return f, nil
}

func (p *parser) arguments(constant bool) ([]argument, error) {
	if !p.isPunct("(") {
		return nil, nil
	}
	if err := p.advance(); err != nil {
		return nil, err
	}
	var out []argument
	for !p.isPunct(")") {
		n, err := p.name()
		if err != nil {
			return nil, err
		}
		if err := p.expect(":"); err != nil {
			return nil, err
		}
		v, err := p.value(constant)
		if err != nil {
			return nil, err
		}
		out = append(out, argument{name: n, val: v})
	}
	if len(out) == 0 {
		return nil, &syntaxError{msg: "empty argument list", line: p.tok.line, col: p.tok.col}
	}
	return out, p.advance()
}

func (p *parser) directives() ([]directive, error) {
	var out []directive
	for p.isPunct("@") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		n, err := p.name()
		if err != nil {
			return nil, err
		}
		args, err := p.arguments(false)
		if err != nil {
			return nil, err
		}
		out = append(out, directive{name: n, args: args})
	}
	return out, nil
}

func (p *parser) value(constant bool) (value, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxDepth {
		return value{}, &syntaxError{msg: "the value is nested too deeply", line: p.tok.line, col: p.tok.col}
	}
	t := p.tok
	switch {
	case p.isPunct("$"):
		if constant {
			return value{}, p.unexpected()
		}
		if err := p.advance(); err != nil {
			return value{}, err
		}
		n, err := p.name()
		return value{kind: valVar, s: n}, err
	case t.kind == tokInt:
		return value{kind: valInt, s: t.val}, p.advance()
	case t.kind == tokFloat:
		return value{kind: valFloat, s: t.val}, p.advance()
	case t.kind == tokString:
		return value{kind: valString, s: t.val}, p.advance()
	case t.kind == tokName:
		v := value{kind: valEnum, s: t.val}
		switch t.val {
		case "true", "false":
			v.kind = valBool
		case "null":
			v.kind = valNull
		}
		return v, p.advance()
	case p.isPunct("["):
		if err := p.advance(); err != nil {
			return value{}, err
		}
		v := value{kind: valList, list: []value{}}
		for !p.isPunct("]") {
			e, err := p.value(constant)
			if err != nil {
				return value{}, err
			}
			v.list = append(v.list, e)
		}
		return v, p.advance()
	case p.isPunct("{"):
		if err := p.advance(); err != nil {
			return value{}, err
		}
		v := value{kind: valObject}
		for !p.isPunct("}") {
			n, err := p.name()
			if err != nil {
				return value{}, err
			}
			if err := p.expect(":"); err != nil {
				return value{}, err
			}
			e, err := p.value(constant)
			if err != nil {
				return value{}, err
			}
			v.obj = append(v.obj, argument{name: n, val: e})
		}
		return v, p.advance()
	}
	return value{}, p.unexpected()
}
