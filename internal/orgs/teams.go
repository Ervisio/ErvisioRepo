package orgs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
)

// Team is a team of an organization.
type Team struct {
	ID          uuid.UUID
	Name        string
	Description string
	Members     int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func teamFrom(r dbgen.GetTeamByNameRow) Team {
	return Team{ID: r.ID, Name: r.Name, Description: r.Description, Members: r.Members,
		CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
}

func (s *Service) team(ctx context.Context, q *dbgen.Queries, org uuid.UUID, name string) (dbgen.GetTeamByNameRow, error) {
	t, err := q.GetTeamByName(ctx, dbgen.GetTeamByNameParams{OrgID: org, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, fmt.Errorf("get team: %w", err)
	}
	return t, nil
}

// TeamInput creates or changes a team; nil fields stay on update.
type TeamInput struct {
	Name        *string
	Description *string
}

func checkTeam(in TeamInput) error {
	if in.Name != nil {
		if err := identity.ValidateName(*in.Name); err != nil && !errors.Is(err, identity.ErrReservedName) {
			return &ValidationError{Msg: err.Error()}
		}
	}
	if in.Description != nil {
		return checkText("description", *in.Description, MaxDescription)
	}
	return nil
}

// ListTeams lists the teams of an organization.
func (s *Service) ListTeams(ctx context.Context, who auth.Principal, org string) ([]Team, error) {
	sc, err := s.load(ctx, s.q, who, org)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListTeams(ctx, sc.org.ID)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	out := make([]Team, 0, len(rows))
	for _, r := range rows {
		out = append(out, teamFrom(dbgen.GetTeamByNameRow(r)))
	}
	return out, nil
}

// GetTeam returns one team.
func (s *Service) GetTeam(ctx context.Context, who auth.Principal, org, team string) (Team, error) {
	sc, err := s.load(ctx, s.q, who, org)
	if err != nil {
		return Team{}, err
	}
	t, err := s.team(ctx, s.q, sc.org.ID, team)
	if err != nil {
		return Team{}, err
	}
	return teamFrom(t), nil
}

// CreateTeam creates a team (owners and admins).
func (s *Service) CreateTeam(ctx context.Context, who auth.Principal, org string, in TeamInput, c identity.Client) (Team, error) {
	if in.Name == nil {
		return Team{}, invalid("name is required")
	}
	if err := checkTeam(in); err != nil {
		return Team{}, err
	}
	desc := ""
	if in.Description != nil {
		desc = *in.Description
	}
	var out Team
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		sc, err := s.load(ctx, q, who, org)
		if err != nil {
			return err
		}
		if !sc.actor.CanManage() {
			return ErrForbidden
		}
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("team id: %w", err)
		}
		if err := q.InsertTeam(ctx, dbgen.InsertTeamParams{ID: id, OrgID: sc.org.ID, Name: *in.Name, Description: desc}); err != nil {
			if isUniqueViolation(err) {
				return ErrNameTaken
			}
			return fmt.Errorf("insert team: %w", err)
		}
		t, err := s.team(ctx, q, sc.org.ID, *in.Name)
		if err != nil {
			return err
		}
		out = teamFrom(t)
		return audit.Record(ctx, q, sc.event(audit.ActionOrgTeamCreated, "team", id.String(), c, map[string]any{"name": t.Name}))
	})
	return out, err
}

// UpdateTeam renames a team or changes its description (owners and admins).
func (s *Service) UpdateTeam(ctx context.Context, who auth.Principal, org, team string, in TeamInput, c identity.Client) (Team, error) {
	if err := checkTeam(in); err != nil {
		return Team{}, err
	}
	var out Team
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		sc, err := s.load(ctx, q, who, org)
		if err != nil {
			return err
		}
		t, err := s.team(ctx, q, sc.org.ID, team)
		if err != nil {
			return err
		}
		if !sc.actor.CanManage() {
			return ErrForbidden
		}
		if in.Name != nil {
			t.Name = *in.Name
		}
		if in.Description != nil {
			t.Description = *in.Description
		}
		if err := q.UpdateTeam(ctx, dbgen.UpdateTeamParams{ID: t.ID, Name: t.Name, Description: t.Description}); err != nil {
			if isUniqueViolation(err) {
				return ErrNameTaken
			}
			return fmt.Errorf("update team: %w", err)
		}
		if t, err = s.team(ctx, q, sc.org.ID, t.Name); err != nil {
			return err
		}
		out = teamFrom(t)
		return audit.Record(ctx, q, sc.event(audit.ActionOrgTeamUpdated, "team", t.ID.String(), c, map[string]any{"name": t.Name}))
	})
	return out, err
}

// DeleteTeam deletes a team (owners and admins).
func (s *Service) DeleteTeam(ctx context.Context, who auth.Principal, org, team string, c identity.Client) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		sc, err := s.load(ctx, q, who, org)
		if err != nil {
			return err
		}
		t, err := s.team(ctx, q, sc.org.ID, team)
		if err != nil {
			return err
		}
		if !sc.actor.CanManage() {
			return ErrForbidden
		}
		if _, err := q.DeleteTeam(ctx, t.ID); err != nil {
			return fmt.Errorf("delete team: %w", err)
		}
		return audit.Record(ctx, q, sc.event(audit.ActionOrgTeamDeleted, "team", t.ID.String(), c, map[string]any{"name": t.Name}))
	})
}

// ListTeamMembers lists the members of a team.
func (s *Service) ListTeamMembers(ctx context.Context, who auth.Principal, org, team string) ([]Member, error) {
	sc, err := s.load(ctx, s.q, who, org)
	if err != nil {
		return nil, err
	}
	t, err := s.team(ctx, s.q, sc.org.ID, team)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListTeamMembers(ctx, t.ID)
	if err != nil {
		return nil, fmt.Errorf("list team members: %w", err)
	}
	out := make([]Member, 0, len(rows))
	for _, r := range rows {
		out = append(out, Member{UserID: r.UserID, Username: r.Username, DisplayName: r.DisplayName, Role: r.Role, Since: r.CreatedAt.Time})
	}
	return out, nil
}

func (s *Service) teamRoleOf(ctx context.Context, q *dbgen.Queries, team, user uuid.UUID) (authz.TeamRole, error) {
	r, err := q.GetTeamMemberRole(ctx, dbgen.GetTeamMemberRoleParams{TeamID: team, UserID: user})
	if errors.Is(err, pgx.ErrNoRows) {
		return authz.TeamNone, nil
	}
	if err != nil {
		return authz.TeamNone, fmt.Errorf("get team role: %w", err)
	}
	return authz.TeamRole(r), nil
}

// SetTeamMember adds a member of the organization to a team or changes its
// team role (owners, admins and team maintainers).
func (s *Service) SetTeamMember(ctx context.Context, who auth.Principal, org, team, username, role string, c identity.Client) error {
	tr, err := authz.ParseTeamRole(role)
	if err != nil {
		return &ValidationError{Msg: err.Error()}
	}
	return s.tx(ctx, func(q *dbgen.Queries) error {
		sc, err := s.load(ctx, q, who, org)
		if err != nil {
			return err
		}
		t, err := s.team(ctx, q, sc.org.ID, team)
		if err != nil {
			return err
		}
		mine, err := s.teamRoleOf(ctx, q, t.ID, who.UserID)
		if err != nil {
			return err
		}
		if !sc.actor.CanManageTeamMembers(mine) {
			return ErrForbidden
		}
		uid, orgRole, err := s.member(ctx, q, sc.org.ID, username)
		if err != nil {
			return err
		}
		if orgRole == authz.OrgNone {
			return ErrNotMember
		}
		if err := q.UpsertTeamMember(ctx, dbgen.UpsertTeamMemberParams{TeamID: t.ID, OrgID: sc.org.ID, UserID: uid, Role: string(tr)}); err != nil {
			return fmt.Errorf("upsert team member: %w", err)
		}
		return audit.Record(ctx, q, sc.event(audit.ActionOrgTeamMemberSet, "user", uid.String(), c,
			map[string]any{"team": t.Name, "role": string(tr)}))
	})
}

// RemoveTeamMember removes a member from a team (owners, admins, team
// maintainers, or the member itself).
func (s *Service) RemoveTeamMember(ctx context.Context, who auth.Principal, org, team, username string, c identity.Client) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		sc, err := s.load(ctx, q, who, org)
		if err != nil {
			return err
		}
		t, err := s.team(ctx, q, sc.org.ID, team)
		if err != nil {
			return err
		}
		uid, _, err := s.member(ctx, q, sc.org.ID, username)
		if err != nil {
			return err
		}
		mine, err := s.teamRoleOf(ctx, q, t.ID, who.UserID)
		if err != nil {
			return err
		}
		if uid != who.UserID && !sc.actor.CanManageTeamMembers(mine) {
			return ErrForbidden
		}
		n, err := q.DeleteTeamMember(ctx, dbgen.DeleteTeamMemberParams{TeamID: t.ID, UserID: uid})
		if err != nil {
			return fmt.Errorf("delete team member: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return audit.Record(ctx, q, sc.event(audit.ActionOrgTeamMemberDrop, "user", uid.String(), c, map[string]any{"team": t.Name}))
	})
}
