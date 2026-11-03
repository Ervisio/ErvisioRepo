package ghapi

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// ids maps the UUIDs of the services to the numeric ids GitHub clients
// expect, for one request. The first database error is kept in err and
// fails the response (call.ok), so the converters can stay error free.
type ids struct {
	ctx   context.Context
	q     *dbgen.Queries
	num   map[uuid.UUID]int64
	users map[string]*namespace // by lowercase name; nil = unknown
	err   error
}

// namespace is a user or an organization.
type namespace struct {
	ID   uuid.UUID
	Kind string // "user" or "org"
	Name string
}

func newIDs(ctx context.Context, q *dbgen.Queries) *ids {
	return &ids{ctx: ctx, q: q, num: map[uuid.UUID]int64{}, users: map[string]*namespace{}}
}

// prefetch loads the ids of us in one round trip, assigning new ones.
func (m *ids) prefetch(us ...uuid.UUID) {
	var missing []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, u := range us {
		if u == uuid.Nil || seen[u] {
			continue
		}
		seen[u] = true
		if _, ok := m.num[u]; !ok {
			missing = append(missing, u)
		}
	}
	if len(missing) == 0 || m.err != nil {
		return
	}
	if err := m.q.AssignGitHubAPIIDs(m.ctx, missing); err != nil {
		m.err = fmt.Errorf("assign api ids: %w", err)
		return
	}
	rows, err := m.q.ListGitHubAPIIDs(m.ctx, missing)
	if err != nil {
		m.err = fmt.Errorf("list api ids: %w", err)
		return
	}
	for _, r := range rows {
		m.num[r.Uuid] = r.ID
	}
}

// of returns the numeric id of u (0 for the nil UUID).
func (m *ids) of(u uuid.UUID) int64 {
	if u == uuid.Nil {
		return 0
	}
	if n, ok := m.num[u]; ok {
		return n
	}
	m.prefetch(u)
	return m.num[u]
}

// parse resolves a numeric id, or a UUID, from a URL to its UUID.
func (m *ids) parse(s string) (uuid.UUID, error) {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n <= 0 {
			return uuid.Nil, errNotFound
		}
		u, err := m.q.GetGitHubAPIUUID(m.ctx, n)
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, errNotFound
		}
		if err != nil {
			return uuid.Nil, fmt.Errorf("resolve api id: %w", err)
		}
		m.num[u] = n
		return u, nil
	}
	u, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, errNotFound
	}
	return u, nil
}

// prefetchUsers loads the namespaces called names in one round trip.
func (m *ids) prefetchUsers(names ...string) {
	var missing []string
	for _, n := range names {
		k := strings.ToLower(n)
		if k == "" {
			continue
		}
		if _, ok := m.users[k]; !ok {
			missing = append(missing, k)
			m.users[k] = nil
		}
	}
	if len(missing) == 0 || m.err != nil {
		return
	}
	rows, err := m.q.ListNamespacesByName(m.ctx, missing)
	if err != nil {
		m.err = fmt.Errorf("list namespaces: %w", err)
		return
	}
	us := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		m.users[strings.ToLower(r.Name)] = &namespace{ID: r.ID, Kind: r.Kind, Name: r.Name}
		us = append(us, r.ID)
	}
	m.prefetch(us...)
}

// user returns the namespace called name, nil when unknown.
func (m *ids) user(name string) *namespace {
	k := strings.ToLower(name)
	if ns, ok := m.users[k]; ok {
		return ns
	}
	m.prefetchUsers(name)
	return m.users[k]
}

// nodeID is the opaque global id of an object.
func nodeID(kind string, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(kind + ":" + id.String()))
}
