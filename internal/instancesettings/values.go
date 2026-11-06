// Package instancesettings holds the run-time editable instance settings:
// the whitelist (config.Editable) read and written by reflection on the
// config, the overrides stored in the database by the web admin, and the
// locking rules that keep a key at the value of config.toml or EREPO_*.
package instancesettings

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"unicode"

	"github.com/Ervisio/ErvisioRepo/internal/config"
)

var (
	sizeType     = reflect.TypeFor[config.Size]()
	durationType = reflect.TypeFor[config.Duration]()
)

// Editable reports whether path ("section.key") is a run-time setting.
func Editable(path string) bool { return slices.Contains(config.Editable, path) }

// Field returns the config field of a "section.key" path, found by toml tag.
func Field(c *config.Config, path string) (reflect.Value, bool) {
	v := reflect.ValueOf(c).Elem()
	for part := range strings.SplitSeq(path, ".") {
		if v.Kind() != reflect.Struct {
			return reflect.Value{}, false
		}
		found := false
		for i := range v.NumField() {
			tag, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("toml"), ",")
			if tag == part {
				v, found = v.Field(i), true
				break
			}
		}
		if !found {
			return reflect.Value{}, false
		}
	}
	return v, true
}

// Values returns the editable settings of c in their JSON form: sizes and
// durations as canonical strings, integers as int64, lists as string arrays.
func Values(c *config.Config) map[string]any {
	out := make(map[string]any, len(config.Editable))
	for _, path := range config.Editable {
		f, ok := Field(c, path)
		if !ok {
			continue
		}
		switch {
		case f.Type() == sizeType:
			out[path] = config.Size(f.Int()).String()
		case f.Type() == durationType:
			out[path] = config.Duration(f.Int()).String()
		case f.Kind() == reflect.String:
			out[path] = f.String()
		case f.Kind() == reflect.Bool:
			out[path] = f.Bool()
		case f.Kind() == reflect.Int, f.Kind() == reflect.Int64:
			out[path] = f.Int()
		case f.Kind() == reflect.Slice:
			out[path] = append([]string{}, f.Interface().([]string)...)
		}
	}
	return out
}

// Normalize checks a JSON value against the type of the setting and returns
// it in the canonical form of Values. The message of an error is shown to
// the user.
func Normalize(path string, raw json.RawMessage) (any, error) {
	var tmpl config.Config
	f, ok := Field(&tmpl, path)
	if !ok || !Editable(path) {
		return nil, errors.New("not an editable setting")
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, errors.New("a value is required")
	}
	str := func() (string, error) {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", errors.New("must be a string")
		}
		if HasControl(s) {
			return "", errors.New("control characters are not allowed")
		}
		return strings.TrimSpace(s), nil
	}
	switch {
	case f.Type() == sizeType:
		s, err := str()
		if err != nil {
			return nil, err
		}
		n, err := config.ParseSize(s)
		if err != nil {
			return nil, err
		}
		return n.String(), nil
	case f.Type() == durationType:
		s, err := str()
		if err != nil {
			return nil, err
		}
		d, err := config.ParseDuration(s)
		if err != nil {
			return nil, err
		}
		return d.String(), nil
	case f.Kind() == reflect.String:
		return str()
	case f.Kind() == reflect.Bool:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, errors.New("must be true or false")
		}
		return b, nil
	case f.Kind() == reflect.Int, f.Kind() == reflect.Int64:
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, errors.New("must be an integer")
		}
		return n, nil
	case f.Kind() == reflect.Slice:
		var items []string
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, errors.New("must be a list of strings")
		}
		out := make([]string, 0, len(items))
		for _, it := range items {
			if HasControl(it) {
				return nil, errors.New("control characters are not allowed")
			}
			out = append(out, strings.TrimSpace(it))
		}
		return out, nil
	}
	return nil, errors.New("not an editable setting")
}

// set stores a value in the canonical form of Normalize into c.
func set(c *config.Config, path string, v any) error {
	f, ok := Field(c, path)
	if !ok || !f.CanSet() {
		return errors.New("not an editable setting")
	}
	switch x := v.(type) {
	case string:
		switch {
		case f.Type() == sizeType:
			n, err := config.ParseSize(x)
			if err != nil {
				return err
			}
			f.SetInt(int64(n))
		case f.Type() == durationType:
			d, err := config.ParseDuration(x)
			if err != nil {
				return err
			}
			f.SetInt(int64(d))
		case f.Kind() == reflect.String:
			f.SetString(x)
		default:
			return errors.New("wrong type")
		}
	case bool:
		if f.Kind() != reflect.Bool {
			return errors.New("wrong type")
		}
		f.SetBool(x)
	case int64:
		if f.Kind() != reflect.Int && f.Kind() != reflect.Int64 {
			return errors.New("wrong type")
		}
		f.SetInt(x)
	case []string:
		if f.Kind() != reflect.Slice {
			return errors.New("wrong type")
		}
		f.Set(reflect.ValueOf(slices.Clone(x)))
	default:
		return errors.New("wrong type")
	}
	return nil
}

// HasControl reports whether s holds a control character.
func HasControl(s string) bool { return strings.ContainsFunc(s, unicode.IsControl) }

// FieldError is a problem with one setting; Path is empty when the problem
// names no key (a TOML syntax error).
type FieldError struct {
	Path string `json:"path"`
	Msg  string `json:"msg"`
}

// FieldErrors flattens the errors of config.Parse or Config.Validate into
// per-key entries.
func FieldErrors(err error) []FieldError {
	var out []FieldError
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		var fe *config.FieldError
		switch j, joined := e.(interface{ Unwrap() []error }); {
		case !errors.As(e, &fe):
			out = append(out, FieldError{"", e.Error()})
		case e == error(fe):
			out = append(out, FieldError{fe.Path, fe.Msg})
		case joined:
			for _, x := range j.Unwrap() {
				walk(x)
			}
		default:
			walk(errors.Unwrap(e))
		}
	}
	walk(err)
	if len(out) == 0 && err != nil {
		out = append(out, FieldError{"", err.Error()})
	}
	return out
}
