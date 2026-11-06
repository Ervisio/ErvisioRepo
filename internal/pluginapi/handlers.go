package pluginapi

import (
	"errors"
	"fmt"
	"net/http"
	netmail "net/mail"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/mail"
)

type counts struct {
	Repos int64 `json:"repos"`
	Users int64 `json:"users"`
	Orgs  int64 `json:"orgs"`
}

type statusResponse struct {
	Version          string    `json:"version"`
	Commit           string    `json:"commit,omitempty"`
	StartedAt        time.Time `json:"startedAt"`
	PublicURL        string    `json:"publicUrl"`
	Upstream         Upstream  `json:"upstream"`
	SSHEnabled       bool      `json:"sshEnabled"`
	SSHListen        string    `json:"sshListen"`
	ConfigPath       string    `json:"configPath"`
	ConfigWritable   bool      `json:"configWritable"`
	ConfigError      string    `json:"configError"`
	RestartRequired  bool      `json:"restartRequired"`
	ActionsEnabled   bool      `json:"actionsEnabled"`
	MailEnabled      bool      `json:"mailEnabled"`
	StorageConfigure bool      `json:"storageConfigured"`
	Counts           counts    `json:"counts"`
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	c := s.opt.Config
	f := s.effective(r.Context(), s.readFile())
	var n counts
	if s.opt.Counts != nil {
		if row, err := s.opt.Counts.InstanceCounts(r.Context()); err != nil {
			s.log.Warn("plugin API: instance counts", "err", err)
		} else {
			n = counts{row.Repos, row.Users, row.Orgs}
		}
	}
	writeJSON(w, statusResponse{
		Version: s.opt.Version, Commit: s.opt.Commit, StartedAt: s.opt.StartedAt.UTC(),
		PublicURL: c.Server.PublicURL, Upstream: s.upstream,
		SSHEnabled: c.SSH.Enabled, SSHListen: c.Server.SSHListen,
		ConfigPath: s.configPath(), ConfigWritable: s.writable(), ConfigError: f.errText(),
		RestartRequired: s.restartRequired(f), ActionsEnabled: c.Actions.Enabled, MailEnabled: c.Mail.Enabled,
		StorageConfigure: len(c.Storage.Policy) > 0, Counts: n,
	})
}

/* ---------- public URL ---------- */

// validPublicURL returns the origin of value: an absolute http(s) URL
// without credentials, path, query or fragment.
func validPublicURL(value string) (string, bool) {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", false
	}
	if u.Scheme != "https" && u.Scheme != "http" {
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
		writeProblem(w, http.StatusBadRequest, "use an http:// or https:// address without a path")
		return
	}
	if _, ok := s.apply(r.Context(), w, []edit{{"server", "public_url", tomlString(origin)}}); !ok {
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

// runnerToken issues an instance-scope runner registration token. The token
// is in the response only, never in a log.
func (s *Server) runnerToken(w http.ResponseWriter, r *http.Request) {
	if s.opt.RunnerAdmin == nil {
		writeProblem(w, http.StatusNotImplemented, "runners are not available")
		return
	}
	tok, exp, err := s.opt.RunnerAdmin.CreateInstanceRegistrationToken(r.Context())
	if err != nil {
		s.log.Error("plugin API: create runner registration token", "err", err)
		writeProblem(w, http.StatusInternalServerError, "cannot create a registration token")
		return
	}
	writeJSON(w, map[string]any{
		"token": tok, "expiresAt": exp.UTC(),
		"command": "ervisio-runner register --url " + s.opt.Config.Server.PublicURL + " --token " + tok,
	})
}

func (s *Server) deleteRunner(w http.ResponseWriter, r *http.Request, rawID string) {
	if s.opt.RunnerAdmin == nil {
		writeProblem(w, http.StatusNotImplemented, "runners are not available")
		return
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "")
		return
	}
	if err := s.opt.RunnerAdmin.DeleteInstanceRunner(r.Context(), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "no such runner")
			return
		}
		s.log.Error("plugin API: delete runner", "err", err)
		writeProblem(w, http.StatusInternalServerError, "cannot delete the runner")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

/* ---------- mail ---------- */

// mailTest sends a test message with the [mail] settings of the file on
// disk, so that they can be tried before a restart.
func (s *Server) mailTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To string `json:"to"`
	}
	if err := decode(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := netmail.ParseAddress(body.To); err != nil {
		writeValidation(w, "invalid recipient", []fieldError{{Path: "to", Msg: "must be an email address"}})
		return
	}
	f := s.effective(r.Context(), s.readFile())
	cfg := s.opt.Config
	switch {
	case f.err != nil:
		writeValidation(w, "the configuration file is not valid", fieldErrors(f.err))
		return
	case f.cfg != nil:
		cfg = f.cfg
	}
	if !cfg.Mail.Enabled {
		writeValidation(w, "mail is disabled", []fieldError{{Path: "mail.enabled", Msg: "mail is disabled"}})
		return
	}
	password, err := readSecret(cfg.Mail.PasswordFile)
	if err != nil {
		writeValidation(w, "cannot read the mail password", []fieldError{{Path: "mail.password_file", Msg: err.Error()}})
		return
	}
	sender, err := mail.NewSMTP(cfg.Mail, password)
	if err != nil {
		writeValidation(w, "invalid mail settings", []fieldError{{Path: "mail.from", Msg: err.Error()}})
		return
	}
	msg := mail.Message{To: body.To, Subject: "ErvisioRepo test message",
		Body: "This is a test message sent from the Ervisio plugin to check the mail settings of " +
			cfg.Server.PublicURL + ".\n"}
	if err := sender.Send(r.Context(), msg); err != nil {
		s.log.Warn("plugin API: test mail failed", "err", err)
		writeProblem(w, http.StatusBadGateway, err.Error())
		return
	}
	s.log.Info("plugin API: test mail sent")
	writeJSON(w, map[string]any{"sent": true})
}

// readSecret reads an operator-configured secret file, like serve does for
// the mail password.
func readSecret(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path) //nolint:gosec // operator-configured secret file, not request data
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return strings.TrimRight(string(raw), "\r\n"), nil
}
