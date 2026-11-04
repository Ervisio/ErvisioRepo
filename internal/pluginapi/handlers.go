package pluginapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

type statusResponse struct {
	Version          string   `json:"version"`
	Commit           string   `json:"commit,omitempty"`
	PublicURL        string   `json:"publicUrl"`
	Upstream         Upstream `json:"upstream"`
	SSHEnabled       bool     `json:"sshEnabled"`
	ConfigWritable   bool     `json:"configWritable"`
	RestartRequired  bool     `json:"restartRequired"`
	ActionsEnabled   bool     `json:"actionsEnabled"`
	StorageConfigure bool     `json:"storageConfigured"`
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	c := s.opt.Config
	writeJSON(w, statusResponse{
		Version: s.opt.Version, Commit: s.opt.Commit,
		PublicURL: c.Server.PublicURL, Upstream: s.upstream,
		SSHEnabled: c.SSH.Enabled, ConfigWritable: s.opt.File != nil,
		RestartRequired: s.restart.Load(), ActionsEnabled: c.Actions.Enabled,
		StorageConfigure: len(c.Storage.Policy) > 0,
	})
}

/* ---------- settings ---------- */

type settingsDoc struct {
	PublicURL         string `json:"publicUrl"`
	Registration      string `json:"registration"`
	UserDirectory     string `json:"userDirectory"`
	Require2FA        string `json:"require2fa"`
	ActionsEnabled    bool   `json:"actionsEnabled"`
	DefaultActionsURL string `json:"defaultActionsUrl"`
	Limits            limits `json:"limits"`
}

type limits struct {
	MaxPushSize        string `json:"maxPushSize"`
	GitIdleTimeout     string `json:"gitIdleTimeout"`
	GitMaxDuration     string `json:"gitMaxDuration"`
	GitRequestsPerHour int    `json:"gitRequestsPerHour"`
}

type settingsResponse struct {
	settingsDoc
	RestartRequired bool `json:"restartRequired"`
}

// settingsPatch holds the fields of PUT settings; absent fields are kept.
type settingsPatch struct {
	PublicURL         *string `json:"publicUrl"`
	Registration      *string `json:"registration"`
	UserDirectory     *string `json:"userDirectory"`
	Require2FA        *string `json:"require2fa"`
	ActionsEnabled    *bool   `json:"actionsEnabled"`
	DefaultActionsURL *string `json:"defaultActionsUrl"`
	Limits            *struct {
		MaxPushSize        *string `json:"maxPushSize"`
		GitIdleTimeout     *string `json:"gitIdleTimeout"`
		GitMaxDuration     *string `json:"gitMaxDuration"`
		GitRequestsPerHour *int    `json:"gitRequestsPerHour"`
	} `json:"limits"`
}

func docOf(c *config.Config) settingsDoc {
	return settingsDoc{
		PublicURL: c.Server.PublicURL, Registration: c.Security.Registration,
		UserDirectory: c.Security.UserDirectory, Require2FA: c.Auth.Require2FA,
		ActionsEnabled: c.Actions.Enabled, DefaultActionsURL: c.Actions.DefaultActionsURL,
		Limits: limits{
			MaxPushSize: c.Git.MaxPushSize.String(), GitIdleTimeout: c.Git.IdleTimeout.String(),
			GitMaxDuration: c.Git.MaxDuration.String(), GitRequestsPerHour: c.Git.RequestsPerHour,
		},
	}
}

// fileConfigNow parses the file (with the environment overrides): the
// configuration the next start will run with.
func (s *Server) fileConfigNow() (*config.Config, []byte, error) {
	data, err := s.opt.File.Read()
	if err != nil {
		return nil, nil, err
	}
	cfg, err := config.Parse(data, s.opt.LookupEnv)
	if err != nil {
		return nil, data, fmt.Errorf("parse config: %w", err)
	}
	return cfg, data, nil
}

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	cfg := s.opt.Config
	if s.opt.File != nil {
		if next, _, err := s.fileConfigNow(); err == nil {
			cfg = next
		} else {
			s.log.Warn("plugin API: read settings file", "err", err)
		}
	}
	writeJSON(w, settingsResponse{docOf(cfg), s.restart.Load()})
}

type edit struct{ section, key, value string }

func (p *settingsPatch) edits() ([]edit, error) {
	var out []edit
	str := func(section, key string, v *string) error {
		if v == nil {
			return nil
		}
		if hasControl(*v) {
			return fmt.Errorf("%s.%s: control characters are not allowed", section, key)
		}
		out = append(out, edit{section, key, tomlString(*v)})
		return nil
	}
	for _, e := range []error{
		str("server", "public_url", p.PublicURL),
		str("security", "registration", p.Registration),
		str("security", "user_directory", p.UserDirectory),
		str("auth", "require_2fa", p.Require2FA),
		str("actions", "default_actions_url", p.DefaultActionsURL),
	} {
		if e != nil {
			return nil, e
		}
	}
	if p.ActionsEnabled != nil {
		out = append(out, edit{"actions", "enabled", fmt.Sprint(*p.ActionsEnabled)})
	}
	if l := p.Limits; l != nil {
		for _, e := range []error{
			str("git", "max_push_size", l.MaxPushSize),
			str("git", "idle_timeout", l.GitIdleTimeout),
			str("git", "max_duration", l.GitMaxDuration),
		} {
			if e != nil {
				return nil, e
			}
		}
		if l.GitRequestsPerHour != nil {
			out = append(out, edit{"git", "requests_per_hour", fmt.Sprint(*l.GitRequestsPerHour)})
		}
	}
	return out, nil
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var p settingsPatch
	if err := decode(r, &p); err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	edits, err := p.edits()
	if err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(edits) == 0 {
		writeProblem(w, http.StatusBadRequest, "no setting to change")
		return
	}
	cfg, ok := s.apply(w, edits)
	if !ok {
		return
	}
	writeJSON(w, settingsResponse{docOf(cfg), true})
}

// apply patches the file, validates the result as a whole and writes it.
func (s *Server) apply(w http.ResponseWriter, edits []edit) (*config.Config, bool) {
	if s.opt.File == nil {
		writeProblem(w, http.StatusNotImplemented, "the configuration file is not available")
		return nil, false
	}
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
		writeProblem(w, http.StatusUnprocessableEntity, err.Error())
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

/* ---------- public URL ---------- */

// validPublicURL returns the origin of value: https, or http for loopback,
// without credentials, path, query or fragment.
func validPublicURL(value string) (string, bool) {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", false
	}
	local := slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, u.Hostname())
	if u.Scheme != "https" && (u.Scheme != "http" || !local) {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

func (s *Server) putPublicURL(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := decode(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	origin, ok := validPublicURL(body.URL)
	if !ok {
		writeProblem(w, http.StatusBadRequest, "use an https:// address without a path")
		return
	}
	if _, ok := s.apply(w, []edit{{"server", "public_url", tomlString(origin)}}); !ok {
		return
	}
	writeJSON(w, map[string]any{"publicUrl": origin, "restartRequired": true})
}

/* ---------- storage ---------- */

type storageTarget struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Encrypted bool     `json:"encrypted"`
	Roles     []string `json:"roles"`
	Budget    int64    `json:"budget,omitempty"`
}

type storagePolicy struct {
	Name            string   `json:"name"`
	Primary         string   `json:"primary"`
	Replicas        []string `json:"replicas"`
	Cache           string   `json:"cache,omitempty"`
	Durability      string   `json:"durability"`
	Objects         int64    `json:"objects"`
	Bytes           int64    `json:"bytes"`
	PendingReplicas int64    `json:"pendingReplicas"`
	Errors          int64    `json:"errors"`
}

type storageCache struct {
	Target  string `json:"target"`
	Budget  int64  `json:"budget"`
	Used    int64  `json:"used"`
	Entries int    `json:"entries"`
}

type storageWarning struct {
	Code   string `json:"code"`
	Policy string `json:"policy,omitempty"`
}

func (s *Server) storage(w http.ResponseWriter, r *http.Request) {
	if s.opt.Storage == nil {
		writeProblem(w, http.StatusNotImplemented, "storage status is not available")
		return
	}
	st, err := s.opt.Storage.Status(r.Context())
	if err != nil {
		s.log.Error("plugin API: storage status", "err", err)
		writeProblem(w, http.StatusInternalServerError, "cannot read the storage status")
		return
	}
	budget := map[string]int64{}
	for _, t := range s.opt.Config.Storage.Targets {
		budget[t.Name] = int64(t.Budget)
	}
	out := struct {
		Targets  []storageTarget  `json:"targets"`
		Policies []storagePolicy  `json:"policies"`
		Caches   []storageCache   `json:"caches"`
		Warnings []storageWarning `json:"warnings"`
	}{[]storageTarget{}, []storagePolicy{}, []storageCache{}, []storageWarning{}}
	for _, t := range st.Targets {
		out.Targets = append(out.Targets, storageTarget{t.Name, t.Kind, t.Encrypted, t.Roles, budget[t.Name]})
	}
	for _, p := range st.Policies {
		out.Policies = append(out.Policies, storagePolicy{
			p.Name, p.Primary, p.Replicas, p.Cache, p.Durability, p.Objects, p.Bytes, p.PendingReplicas, p.Errors,
		})
	}
	for _, c := range st.Caches {
		out.Caches = append(out.Caches, storageCache{c.Target, c.Budget, c.Used, c.Entries})
	}
	for _, wn := range st.Warnings {
		out.Warnings = append(out.Warnings, storageWarning{wn.Code, wn.Policy})
	}
	writeJSON(w, out)
}

/* ---------- runners ---------- */

type runnerDoc struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Labels     []string   `json:"labels"`
	Version    string     `json:"version"`
	Ephemeral  bool       `json:"ephemeral"`
	Online     bool       `json:"online"`
	LastSeenAt *time.Time `json:"lastSeenAt"`
}

func (s *Server) runners(w http.ResponseWriter, r *http.Request) {
	if s.opt.Runners == nil {
		writeProblem(w, http.StatusNotImplemented, "runners are not available")
		return
	}
	rows, err := s.opt.Runners.ListActionsRunners(r.Context(), dbgen.ListActionsRunnersParams{Scope: "instance"})
	if err != nil {
		s.log.Error("plugin API: list runners", "err", err)
		writeProblem(w, http.StatusInternalServerError, "cannot list the runners")
		return
	}
	out := make([]runnerDoc, 0, len(rows))
	for _, x := range rows {
		d := runnerDoc{
			ID: x.ID.String(), Name: x.Name, Labels: x.RawLabels, Version: x.Version,
			Ephemeral: x.Ephemeral, Online: x.Online,
		}
		if d.Labels == nil {
			d.Labels = []string{}
		}
		if x.LastSeenAt.Valid {
			t := x.LastSeenAt.Time
			d.LastSeenAt = &t
		}
		out = append(out, d)
	}
	writeJSON(w, map[string]any{"runners": out, "scope": "instance"})
}
