package instancesettings

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/Ervisio/ErvisioRepo/internal/config"
)

// Sources of the value of a setting.
const (
	SourceDefault = "default"
	SourceFile    = "file"
	SourceDB      = "db"
	SourceEnv     = "env"
)

// FromEnv reports whether an EREPO_ variable sets path. Lists cannot be set
// from the environment.
func FromEnv(path string, lookupEnv func(string) (string, bool)) bool {
	if lookupEnv == nil {
		return false
	}
	var tmpl config.Config
	if f, ok := Field(&tmpl, path); !ok || f.Kind() == reflect.Slice {
		return false
	}
	_, ok := lookupEnv(config.EnvName(path))
	return ok
}

// Locked reports whether path ignores the overrides of the web admin: it is
// listed in [settings] locked of base or set by an EREPO_ variable.
func Locked(base *config.Config, path string, lookupEnv func(string) (string, bool)) bool {
	return slices.Contains(base.Settings.Locked, path) || FromEnv(path, lookupEnv)
}

// Skipped is a stored override that Overlay did not apply.
type Skipped struct {
	Key    string
	Reason string
}

// Overlay returns a copy of base with the stored overrides applied, in key
// order. Overrides of locked or unknown keys, and those that would make the
// configuration invalid, are skipped and returned. base is not modified.
func Overlay(base *config.Config, overrides map[string]json.RawMessage, lookupEnv func(string) (string, bool)) (*config.Config, []Skipped) {
	out := *base
	var skipped []Skipped
	for _, k := range slices.Sorted(maps.Keys(overrides)) {
		if !Editable(k) {
			skipped = append(skipped, Skipped{k, "not an editable setting"})
			continue
		}
		if Locked(base, k, lookupEnv) {
			skipped = append(skipped, Skipped{k, "locked"})
			continue
		}
		v, err := Normalize(k, overrides[k])
		if err != nil {
			skipped = append(skipped, Skipped{k, err.Error()})
			continue
		}
		next := out
		if err := set(&next, k, v); err != nil {
			skipped = append(skipped, Skipped{k, err.Error()})
			continue
		}
		if err := next.Validate(); err != nil {
			skipped = append(skipped, Skipped{k, fmt.Sprintf("invalid configuration: %v", err)})
			continue
		}
		out = next
	}
	return &out, skipped
}
