package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	"github.com/Ervisio/ErvisioRepo/internal/runner/config"
	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1"
)

// RegisterOptions are the inputs of Register.
type RegisterOptions struct {
	URL         string
	Token       string // registration token (ertk_…)
	Name        string
	Labels      config.Labels
	Ephemeral   bool
	InsecureTLS bool
	Version     string
}

// Register exchanges a registration token for a runner credential.
func Register(ctx context.Context, opts RegisterOptions) (*State, error) {
	if err := config.CheckURL(opts.URL); err != nil {
		return nil, fmt.Errorf("url: %w", err)
	}
	if strings.TrimSpace(opts.Token) == "" {
		return nil, errors.New("a registration token is required")
	}
	client := NewClient(opts.URL, "", opts.Version, opts.InsecureTLS)
	resp, err := client.Register(ctx, connect.NewRequest(&runnerv1.RegisterRequest{
		Token:     strings.TrimSpace(opts.Token),
		Name:      opts.Name,
		Version:   opts.Version,
		Labels:    opts.Labels.Wire(),
		Ephemeral: opts.Ephemeral,
	}))
	if err != nil {
		if isAuthError(err) || connect.CodeOf(err) == connect.CodeNotFound {
			return nil, errors.New("register: the registration token is invalid, expired or already used")
		}
		return nil, fmt.Errorf("register: %w", err)
	}
	if resp.Msg.GetCredential() == "" {
		return nil, errors.New("register: the server returned no credential")
	}
	st := &State{
		URL:        strings.TrimRight(opts.URL, "/"),
		Credential: resp.Msg.GetCredential(),
		Name:       opts.Name,
		Labels:     opts.Labels.Wire(),
		Ephemeral:  opts.Ephemeral,
	}
	if r := resp.Msg.GetRunner(); r != nil {
		st.ID = r.GetId()
		if r.GetName() != "" {
			st.Name = r.GetName()
		}
		if len(r.GetLabels()) > 0 {
			st.Labels = r.GetLabels()
		}
		st.Ephemeral = r.GetEphemeral()
	}
	return st, nil
}
