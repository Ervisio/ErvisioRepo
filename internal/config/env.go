package config

import (
	"encoding"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

const envPrefix = "EREPO"

var textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()

// applyEnv overrides scalar fields from EREPO_<SECTION>_<KEY> variables, the
// names being derived from the toml tags. Lists and maps (storage targets and
// policies) can only be set in the file.
func applyEnv(cfg *Config, lookup func(string) (string, bool)) []error {
	return walkEnv(reflect.ValueOf(cfg).Elem(), envPrefix, lookup)
}

func walkEnv(v reflect.Value, prefix string, lookup func(string) (string, bool)) []error {
	var errs []error
	t := v.Type()
	for i := range t.NumField() {
		tag, _, _ := strings.Cut(t.Field(i).Tag.Get("toml"), ",")
		if tag == "" || tag == "-" {
			continue
		}
		name := prefix + "_" + strings.ToUpper(tag)
		f := v.Field(i)
		if f.Kind() == reflect.Struct && !reflect.PointerTo(f.Type()).Implements(textUnmarshaler) {
			errs = append(errs, walkEnv(f, name, lookup)...)
			continue
		}
		raw, ok := lookup(name)
		if !ok {
			continue
		}
		if err := setScalar(f, raw); err != nil {
			errs = append(errs, &FieldError{Path: "env " + name, Msg: err.Error()})
		}
	}
	return errs
}

func setScalar(f reflect.Value, raw string) error {
	if u, ok := f.Addr().Interface().(encoding.TextUnmarshaler); ok {
		return u.UnmarshalText([]byte(raw))
	}
	switch f.Kind() {
	case reflect.String:
		f.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return errInvalid("boolean", raw)
		}
		f.SetBool(b)
	case reflect.Int, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, f.Type().Bits())
		if err != nil {
			return errInvalid("integer", raw)
		}
		f.SetInt(n)
	case reflect.Float64:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return errInvalid("number", raw)
		}
		f.SetFloat(n)
	default:
		// Slices and maps are not overridable from the environment.
	}
	return nil
}

func errInvalid(kind, raw string) error { return fmt.Errorf("invalid %s %q", kind, raw) }
