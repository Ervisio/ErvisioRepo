package pluginapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/instancesettings"
)

// fieldError is one entry of a 422 "errors" list.
type fieldError = instancesettings.FieldError

func fieldErrors(err error) []fieldError { return instancesettings.FieldErrors(err) }

func valuesOf(c *config.Config) map[string]any { return instancesettings.Values(c) }

// encodeSetting checks a JSON value against the type of the setting and
// returns it TOML encoded. The message of an error is shown to the user.
func encodeSetting(path string, raw json.RawMessage) (string, error) {
	v, err := instancesettings.Normalize(path, raw)
	if err != nil {
		return "", err
	}
	switch x := v.(type) {
	case string:
		return tomlString(x), nil
	case bool:
		return strconv.FormatBool(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case []string:
		return tomlList(x), nil
	}
	return "", errors.New("not an editable setting")
}

type settingsDoc struct {
	Values          map[string]any `json:"values"`
	Defaults        map[string]any `json:"defaults"`
	ConfigPath      string         `json:"configPath"`
	ConfigWritable  bool           `json:"configWritable"`
	ConfigError     string         `json:"configError"`
	RestartRequired bool           `json:"restartRequired"`
}

// fileState is what the configuration file on disk says.
type fileState struct {
	cfg *config.Config // nil when the file is missing or does not parse
	err error          // why cfg is nil; nil without a file
}

// readFile parses the file with the environment overrides: the
// configuration the next start will run with.
func (s *Server) readFile() fileState {
	if s.opt.File == nil {
		return fileState{}
	}
	data, err := s.opt.File.Read()
	if err != nil {
		return fileState{err: err}
	}
	cfg, err := config.Parse(data, s.opt.LookupEnv)
	if err != nil {
		return fileState{err: err}
	}
	return fileState{cfg: cfg}
}

func (f fileState) errText() string {
	if f.err == nil {
		return ""
	}
	return f.err.Error()
}

func (s *Server) configPath() string {
	if s.opt.File == nil {
		return ""
	}
	return s.opt.File.Path()
}

// writable reports whether settings can be saved to the file.
func (s *Server) writable() bool {
	if s.opt.File == nil {
		return false
	}
	if c, ok := s.opt.File.(interface{ Writable() error }); ok {
		return c.Writable() == nil
	}
	return true
}

// restartRequired is true when the file sets an editable key to a value the
// process is not running with, or, when the file cannot be parsed, when this
// process has written it.
func (s *Server) restartRequired(f fileState) bool {
	if f.cfg == nil {
		return s.restart.Load()
	}
	return !reflect.DeepEqual(valuesOf(f.cfg), valuesOf(s.opt.Config))
}

// effective applies the overrides of the web admin, when wired, to the
// configuration read from the file.
func (s *Server) effective(ctx context.Context, f fileState) fileState {
	if f.cfg == nil || s.opt.Overrides == nil {
		return f
	}
	cfg, err := s.opt.Overrides.Overlay(ctx, f.cfg)
	if err != nil {
		s.log.Warn("plugin API: read instance settings", "err", err)
		return f
	}
	return fileState{cfg: cfg}
}

func (s *Server) settingsDoc(ctx context.Context) settingsDoc {
	f := s.effective(ctx, s.readFile())
	values := valuesOf(s.opt.Config)
	if f.cfg != nil {
		values = valuesOf(f.cfg)
	}
	def := config.Default()
	return settingsDoc{
		Values: values, Defaults: valuesOf(&def),
		ConfigPath: s.configPath(), ConfigWritable: s.writable(), ConfigError: f.errText(),
		RestartRequired: s.restartRequired(f),
	}
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.settingsDoc(r.Context()))
}

type edit struct{ section, key, value string }

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Values map[string]json.RawMessage `json:"values"`
	}
	if err := decode(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(body.Values) == 0 {
		writeProblem(w, http.StatusBadRequest, "no setting to change")
		return
	}
	keys := make([]string, 0, len(body.Values))
	for k := range body.Values {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var edits []edit
	var bad []fieldError
	for _, k := range keys {
		v, err := encodeSetting(k, body.Values[k])
		if err != nil {
			bad = append(bad, fieldError{Path: k, Msg: err.Error()})
			continue
		}
		section, key, _ := strings.Cut(k, ".")
		edits = append(edits, edit{section, key, v})
	}
	if len(bad) > 0 {
		writeValidation(w, "invalid settings", bad)
		return
	}
	if _, ok := s.apply(r.Context(), w, edits); !ok {
		return
	}
	writeJSON(w, s.settingsDoc(r.Context()))
}

// apply patches the file, validates the result as a whole and writes it.
// Concurrent calls are serialized so that no write is lost. The overrides of
// the web admin for the written keys are removed, so that the file wins.
func (s *Server) apply(ctx context.Context, w http.ResponseWriter, edits []edit) (*config.Config, bool) {
	if s.opt.File == nil {
		writeProblem(w, http.StatusNotImplemented, "the configuration file is not available")
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.opt.File.Read()
	if err != nil {
		s.log.Error("plugin API: read config", "err", err)
		writeProblem(w, http.StatusInternalServerError, "cannot read the configuration file")
		return nil, false
	}
	next := data
	for _, e := range edits {
		next = setKey(next, e.section, e.key, e.value)
	}
	cfg, err := config.Parse(next, s.opt.LookupEnv)
	if err != nil {
		writeValidation(w, "the configuration would not be valid", fieldErrors(err))
		return nil, false
	}
	if s.opt.Overrides != nil {
		keys := make([]string, 0, len(edits))
		for _, e := range edits {
			keys = append(keys, e.section+"."+e.key)
		}
		if err := s.opt.Overrides.Delete(ctx, keys); err != nil {
			s.log.Error("plugin API: drop instance settings", "err", err)
			writeProblem(w, http.StatusInternalServerError, "cannot update the instance settings")
			return nil, false
		}
	}
	if bytes.Equal(next, data) {
		return cfg, true
	}
	if err := s.opt.File.Write(next); err != nil {
		s.log.Error("plugin API: write config", "err", err)
		if errors.Is(err, errReadOnly) {
			writeProblem(w, http.StatusConflict, "the configuration file is not writable (read-only mount or missing permission)")
		} else {
			writeProblem(w, http.StatusInternalServerError, "cannot write the configuration file")
		}
		return nil, false
	}
	s.restart.Store(true)
	s.log.Info("plugin API: configuration updated", "keys", len(edits))
	return cfg, true
}

// configCheck parses the file on disk.
func (s *Server) configCheck(w http.ResponseWriter, _ *http.Request) {
	if s.opt.File == nil {
		writeProblem(w, http.StatusNotImplemented, "the configuration file is not available")
		return
	}
	f := s.readFile()
	if f.err != nil {
		writeJSON(w, map[string]any{"ok": false, "errors": fieldErrors(f.err)})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "errors": []fieldError{}})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}
