package graphql

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// resolver resolves one field of an object: src is the Go value behind the
// object, a the coerced arguments. It returns a scalar (string, bool, int,
// int64, float64, time.Time, *time.Time), an obj, a []any of those, or nil.
type resolver func(rq *request, src any, a args) (any, error)

// objType is an object, interface, union or enum type of the schema. Only
// the fields listed in it are advertised by introspection; any other field
// queried on an object resolves to null, so that clients asking for
// optional GitHub features degrade instead of failing.
type objType struct {
	name   string
	kind   string // OBJECT, INTERFACE, UNION, ENUM or SCALAR
	fields map[string]resolver
	order  []string        // field names, for introspection
	is     map[string]bool // the interfaces and unions the type belongs to
	enum   []string
}

// schema is the set of types by name.
type schema struct {
	types map[string]*objType
}

// obj is an object value: the name of its type and the Go value behind it.
type obj struct {
	t string
	v any
}

// Error is a GraphQL error raised by a resolver.
type Error struct {
	Type    string // e.g. NOT_FOUND, FORBIDDEN, UNPROCESSABLE, INTERNAL
	Message string
}

func (e *Error) Error() string { return e.Message }

func errorf(typ, format string, a ...any) *Error {
	return &Error{Type: typ, Message: fmt.Sprintf(format, a...)}
}

// gqlError is one entry of the errors list of a response.
type gqlError struct {
	Type      string     `json:"type,omitempty"`
	Path      []any      `json:"path,omitempty"`
	Locations []location `json:"locations,omitempty"`
	Message   string     `json:"message"`
}

type location struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// omap is a JSON object that keeps the order of its keys.
type omap struct {
	keys []string
	vals map[string]any
}

func newOmap() *omap { return &omap{vals: map[string]any{}} }

func (m *omap) set(k string, v any) {
	if _, ok := m.vals[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.vals[k] = v
}

// MarshalJSON writes the object with its keys in insertion order.
func (m *omap) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range m.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, fmt.Errorf("marshal key: %w", err)
		}
		b.Write(kb)
		b.WriteByte(':')
		vb, err := json.Marshal(m.vals[k])
		if err != nil {
			return nil, fmt.Errorf("marshal %s: %w", k, err)
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// maxFields bounds the fields resolved by one request.
const maxFields = 200000

// executor runs one operation of a document.
type executor struct {
	rq     *request
	sch    *schema
	doc    *document
	vars   map[string]any
	errs   []gqlError
	fields int
}

// errTooComplex aborts a request that resolves too many fields.
var errTooComplex = errors.New("the query resolves too many fields; use smaller pages")

// pickOperation returns the operation to run.
func pickOperation(doc *document, name string) (*operation, error) {
	if name != "" {
		for _, op := range doc.ops {
			if op.name == name {
				return op, nil
			}
		}
		return nil, fmt.Errorf("unknown operation named %q", name)
	}
	if len(doc.ops) != 1 {
		return nil, errors.New("an operation name is required when the document has several operations")
	}
	return doc.ops[0], nil
}

// coerceVariables applies the defaults of the operation to the variables
// of the request and wraps single values given for list variables.
func coerceVariables(op *operation, in map[string]any) map[string]any {
	out := map[string]any{}
	for _, d := range op.vars {
		v, ok := in[d.name]
		if !ok {
			if !d.hasDef {
				continue
			}
			v = constValue(*d.def)
		}
		if _, isList := v.([]any); d.list && v != nil && !isList {
			v = []any{v}
		}
		out[d.name] = v
	}
	return out
}

// constValue converts a literal to Go (variables are not allowed).
func constValue(v value) any {
	e := &executor{vars: map[string]any{}}
	return e.value(v)
}

// value converts an argument value to Go: int64, float64, string (strings
// and enums), bool, nil, []any or map[string]any.
func (e *executor) value(v value) any {
	switch v.kind {
	case valVar:
		return e.vars[v.s]
	case valInt:
		if n, err := strconv.ParseInt(v.s, 10, 64); err == nil {
			return n
		}
		f, _ := strconv.ParseFloat(v.s, 64)
		return f
	case valFloat:
		f, _ := strconv.ParseFloat(v.s, 64)
		return f
	case valString, valEnum:
		return v.s
	case valBool:
		return v.s == "true"
	case valList:
		out := make([]any, len(v.list))
		for i, x := range v.list {
			out[i] = e.value(x)
		}
		return out
	case valObject:
		out := map[string]any{}
		for _, a := range v.obj {
			out[a.name] = e.value(a.val)
		}
		return out
	}
	return nil
}

func (e *executor) args(in []argument) args {
	a := args{}
	for _, x := range in {
		a[x.name] = e.value(x.val)
	}
	return a
}

// skipped evaluates @skip and @include.
func (e *executor) skipped(dirs []directive) bool {
	for _, d := range dirs {
		a := e.args(d.args)
		cond, _ := a["if"].(bool)
		switch d.name {
		case "skip":
			if cond {
				return true
			}
		case "include":
			if !cond {
				return true
			}
		}
	}
	return false
}

// applies reports whether a fragment with type condition cond applies to
// objects of type t.
func (e *executor) applies(cond string, t *objType) bool {
	return cond == "" || cond == t.name || t.is[cond]
}

type fieldGroup struct {
	key    string
	fields []*field
}

// collect groups the fields of sels that apply to t by response key.
func (e *executor) collect(t *objType, sels []selection, visited map[string]bool, groups []*fieldGroup, idx map[string]*fieldGroup) []*fieldGroup {
	for _, s := range sels {
		switch s := s.(type) {
		case *field:
			if e.skipped(s.dirs) {
				continue
			}
			k := s.key()
			if g, ok := idx[k]; ok {
				g.fields = append(g.fields, s)
				continue
			}
			g := &fieldGroup{key: k, fields: []*field{s}}
			idx[k] = g
			groups = append(groups, g)
		case *fragmentSpread:
			if e.skipped(s.dirs) || visited[s.name] {
				continue
			}
			visited[s.name] = true
			f, ok := e.doc.frags[s.name]
			if !ok || !e.applies(f.on, t) {
				continue
			}
			groups = e.collect(t, f.sel, visited, groups, idx)
		case *inlineFragment:
			if e.skipped(s.dirs) || !e.applies(s.on, t) {
				continue
			}
			groups = e.collect(t, s.sel, visited, groups, idx)
		}
	}
	return groups
}

func appendPath(path []any, k any) []any {
	out := make([]any, len(path), len(path)+1)
	copy(out, path)
	return append(out, k)
}

// object resolves the selections of o.
func (e *executor) object(o obj, sels []selection, path []any) (*omap, error) {
	t, ok := e.sch.types[o.t]
	if !ok {
		return nil, fmt.Errorf("graphql: unknown type %s", o.t)
	}
	out := newOmap()
	for _, g := range e.collect(t, sels, map[string]bool{}, nil, map[string]*fieldGroup{}) {
		e.fields++
		if e.fields > maxFields {
			return nil, errTooComplex
		}
		f := g.fields[0]
		p := appendPath(path, g.key)
		if f.name == "__typename" {
			out.set(g.key, t.name)
			continue
		}
		r, ok := t.fields[f.name]
		if !ok {
			out.set(g.key, nil)
			continue
		}
		v, err := r(e.rq, o.v, e.args(f.args))
		if err != nil {
			if errors.Is(err, errTooComplex) {
				return nil, err
			}
			e.fieldError(err, f, p)
			out.set(g.key, nil)
			continue
		}
		var sub []selection
		for _, x := range g.fields {
			sub = append(sub, x.sel...)
		}
		c, err := e.complete(v, sub, p)
		if err != nil {
			return nil, err
		}
		out.set(g.key, c)
	}
	return out, nil
}

// complete converts a resolved value to its JSON form.
func (e *executor) complete(v any, sel []selection, path []any) (any, error) {
	switch v := v.(type) {
	case nil:
		return nil, nil
	case obj:
		if len(sel) == 0 {
			return nil, nil
		}
		m, err := e.object(v, sel, path)
		if err != nil {
			return nil, err
		}
		return m, nil
	case *obj:
		if v == nil {
			return nil, nil
		}
		return e.complete(*v, sel, path)
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			c, err := e.complete(x, sel, appendPath(path, i))
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	case time.Time:
		if v.IsZero() {
			return nil, nil
		}
		return v.UTC().Format(time.RFC3339), nil
	case *time.Time:
		if v == nil || v.IsZero() {
			return nil, nil
		}
		return v.UTC().Format(time.RFC3339), nil
	case int:
		return int64(v), nil
	case string, bool, int64, float64:
		return v, nil
	}
	return nil, fmt.Errorf("graphql: unsupported value %T", v)
}

func (e *executor) fieldError(err error, f *field, path []any) {
	var ge *Error
	if !errors.As(err, &ge) {
		ge = e.rq.classify(err)
	}
	e.errs = append(e.errs, gqlError{Type: ge.Type, Path: path, Message: ge.Message,
		Locations: []location{{Line: f.line, Column: f.col}}})
}

// run executes op and returns the data (nil on a request error).
func (e *executor) run(op *operation) (*omap, error) {
	root := obj{t: "Query"}
	switch op.kind {
	case "mutation":
		root.t = "Mutation"
	case "subscription":
		return nil, errors.New("subscriptions are not supported")
	}
	return e.object(root, op.sel, nil)
}
