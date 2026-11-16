package actions

import (
	"context"
	"fmt"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// operatorPrincipal acts for the server operator in the CLI (it has shell
// access to the host and the database already).
var operatorPrincipal = auth.Principal{Username: "ervisio-cli", Role: auth.RoleAdmin}

// OperatorRegistrationToken issues a runner registration token from the
// server CLI. spec is "instance", "org/<owner>" or "repo/<owner>/<name>";
// it needs no wiring beyond the database.
func (s *Service) OperatorRegistrationToken(ctx context.Context, spec string) (RegistrationToken, error) {
	sc, err := s.operatorScope(ctx, spec)
	if err != nil {
		return RegistrationToken{}, err
	}
	return s.CreateRegistrationToken(ctx, operatorPrincipal, sc)
}

func (s *Service) operatorScope(ctx context.Context, spec string) (Scope, error) {
	parts := strings.Split(spec, "/")
	switch {
	case spec == "instance":
		return Scope{kind: "instance", label: "instance"}, nil
	case len(parts) == 2 && parts[0] == "org" && parts[1] != "":
		ns, err := s.q.GetNamespaceByName(ctx, parts[1])
		if isNoRows(err) {
			return Scope{}, fmt.Errorf("owner %q: %w", parts[1], ErrNotFound)
		}
		if err != nil {
			return Scope{}, fmt.Errorf("get owner: %w", err)
		}
		return Scope{kind: "org", ownerID: ns.ID, label: "org:" + ns.Name}, nil
	case len(parts) == 3 && parts[0] == "repo" && parts[1] != "" && parts[2] != "":
		r, err := s.q.GetRepoByPath(ctx, dbgen.GetRepoByPathParams{Owner: parts[1], Name: parts[2]})
		if isNoRows(err) {
			return Scope{}, fmt.Errorf("repository %s/%s: %w", parts[1], parts[2], ErrNotFound)
		}
		if err != nil {
			return Scope{}, fmt.Errorf("get repository: %w", err)
		}
		return Scope{kind: "repo", repoID: r.Repo.ID, label: "repo:" + r.OwnerName + "/" + r.Repo.Name}, nil
	}
	return Scope{}, invalid("scope must be instance, org/<owner> or repo/<owner>/<name>")
}
