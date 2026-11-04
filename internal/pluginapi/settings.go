package pluginapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/config"
)

// editable lists the settings the plugin may change, as "section.key".
var editable = []string{
	"server.public_url", "server.ssh_listen", "server.trusted_proxies",
	"log.level", "log.format",
	"security.registration", "security.user_directory",
	"auth.password_min_length", "auth.session_idle_timeout", "auth.session_max_age",
	"auth.require_verified_email", "auth.login_free_failures_account", "auth.login_free_failures_ip",
	"auth.login_backoff_base", "auth.login_backoff_max", "auth.actions_per_ip_per_hour", "auth.require_2fa",
	"auth.token_max_ttl", "auth.invite_ttl", "auth.verify_token_ttl", "auth.reset_token_ttl",
	"mail.enabled", "mail.host", "mail.port", "mail.tls", "mail.username", "mail.password_file",
	"mail.from", "mail.helo_name", "mail.timeout",
	"git.max_push_size", "git.idle_timeout", "git.max_duration", "git.requests_per_hour",
	"ssh.enabled", "ssh.max_connections", "ssh.login_grace_time",
	"actions.enabled", "actions.default_actions_url", "actions.max_job_timeout",
	"actions.runner_offline_after", "actions.registration_token_ttl", "actions.max_jobs_per_run",
	"actions.cache_max_repo_size", "actions.cache_ttl", "actions.artifact_retention",
	"webhooks.allow_private", "webhooks.allowed_cidrs", "webhooks.timeout", "webhooks.max_attempts",
}

var (
	sizeType     = reflect.TypeFor[config.Size]()
	durationType = reflect.TypeFor[config.Duration]()
)

// field returns the config field of a "section.key" path, found by toml tag.
func field(c *config.Config, path string) (reflect.Value, bool) {
	v := reflect.ValueOf(c).Elem()
	for part := range strings.SplitSeq(path, ".") {
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

// valuesOf returns the editable settings of c in their JSON form: sizes and
// durations as canonical strings, lists as string arrays.
func valuesOf(c *config.Config) map[string]any {
	out := make(map[string]any, len(editable))
	for _, path := range editable {
		f, ok := field(c, path)
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
		case f.Kind() == reflect.Int:
			out[path] = f.Int()
		case f.Kind() == reflect.Slice:
			out[path] = append([]string{}, f.Interface().([]string)...)
		}
	}
	return out
}

// fieldError is one entry of a 422 "errors" list.
type fieldError struct {
	Path string `json:"path"`
	Msg  string `json:"msg"`
}

// fieldErrors flattens the errors of config.Parse into per-key entries.
// An error that names no key (a TOML syntax error) gets an empty path.
func fieldErrors(err error) []fieldError {
	var out []fieldError
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		var fe *config.FieldError
		switch j, joined := e.(interface{ Unwrap() []error }); {
		case !errors.As(e, &fe):
			out = append(out, fieldError{"", e.Error()})
		case e == error(fe):
			out = append(out, fieldError{fe.Path, fe.Msg})
		case joined:
			for _, x := range j.Unwrap() {
				walk(x)
			}
		default:
			walk(errors.Unwrap(e))
		}
	}
	walk(err)
	if len(out) == 0 {
		out = append(out, fieldError{"", err.Error()})
	}
	return out
}

// encodeSetting checks a JSON value against the type of the setting and
// returns it TOML encoded. The message of an error is shown to the user.
func encodeSetting(path string, raw json.RawMessage) (string, error) {
	var tmpl config.Config
	f, ok := field(&tmpl, path)
	if !ok || !slices.Contains(editable, path) {
		return "", errors.New("not an editable setting")
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", errors.New("a value is required")
	}
	str := func() (string, error) {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", errors.New("must be a string")
		}
		if hasControl(s) {
			return "", errors.New("control characters are not allowed")
		}
		return strings.TrimSpace(s), nil
	}
	switch {
	case f.Type() == sizeType:
		s, err := str()
		if err != nil {
			return "", err
		}
		if _, err := config.ParseSize(s); err != nil {
			return "", err
		}
		return tomlString(s), nil
	case f.Type() == durationType:
		s, err := str()
		if err != nil {
			return "", err
		}
		if _, err := config.ParseDuration(s); err != nil {
			return "", err
		}
		return tomlString(s), nil
	case f.Kind() == reflect.String:
		s, err := str()
		if err != nil {
			return "", err
		}
		return tomlString(s), nil
	case f.Kind() == reflect.Bool:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return "", errors.New("must be true or false")
		}
		return strconv.FormatBool(b), nil
	case f.Kind() == reflect.Int:
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return "", errors.New("must be an integer")
		}
		return strconv.FormatInt(n, 10), nil
	case f.Kind() == reflect.Slice:
		var items []string
		if err := json.Unmarshal(raw, &items); err != nil {
			return "", errors.New("must be a list of strings")
		}
		for i, it := range items {
			if hasControl(it) {
				return "", errors.New("control characters are not allowed")
			}
			items[i] = strings.TrimSpace(it)
		}
		return tomlList(items), nil
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

func (s *Server) settingsDoc() settingsDoc {
	f := s.readFile()
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

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.settingsDoc())
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
			bad = append(bad, fieldError{k, err.Error()})
			continue
		}
		section, key, _ := strings.Cut(k, ".")
		edits = append(edits, edit{section, key, v})
	}
	if len(bad) > 0 {
		writeValidation(w, "invalid settings", bad)
		return
	}
	if _, ok := s.apply(w, edits); !ok {
		return
	}
	writeJSON(w, s.settingsDoc())
}

// apply patches the file, validates the result as a whole and writes it.
// Concurrent calls are serialized so that no write is lost.
func (s *Server) apply(w http.ResponseWriter, edits []edit) (*config.Config, bool) {
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
