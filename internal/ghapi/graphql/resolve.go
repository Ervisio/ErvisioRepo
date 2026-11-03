package graphql

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// request is the state of one GraphQL request: the caller and the caches
// shared by its resolvers.
type request struct {
	ctx      context.Context
	s        *Server
	who      auth.Principal
	mutation bool

	num   map[uuid.UUID]int64
	ns    map[string]*namespace // by lowercase name; nil = unknown
	users map[uuid.UUID]*identity.User
	repos map[string]repos.Repo // by lowercase owner/name
}

// namespace is a user or an organization.
type namespace struct {
	ID   uuid.UUID
	Kind string // "user" or "org"
	Name string
}

func newRequest(ctx context.Context, s *Server, who auth.Principal) *request {
	return &request{ctx: ctx, s: s, who: who, num: map[uuid.UUID]int64{},
		ns: map[string]*namespace{}, users: map[uuid.UUID]*identity.User{}, repos: map[string]repos.Repo{}}
}

// read checks the token scope of a read of repository data.
func (rq *request) read() error { return rq.scope(auth.ScopeReadRepo) }

// write checks that the caller may change repository data.
func (rq *request) write() error {
	if rq.who.UserID == uuid.Nil {
		return errorf("FORBIDDEN", "This endpoint requires you to be authenticated.")
	}
	return rq.scope(auth.ScopeWriteRepo)
}

func (rq *request) scope(s string) error {
	if rq.who.Method == auth.MethodToken && !rq.who.HasScope(s) {
		return errorf("INSUFFICIENT_SCOPES", "Resource not accessible by personal access token (the %s scope is required)", s)
	}
	return nil
}

func (rq *request) client() identity.Client {
	ri := auth.RequestInfoFrom(rq.ctx)
	return identity.Client{IP: ri.IP, UserAgent: ri.UserAgent}
}

// ---- numeric ids ----

// prefetch loads the numeric ids of us in one round trip, assigning new ones.
func (rq *request) prefetch(us ...uuid.UUID) error {
	var missing []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, u := range us {
		if u == uuid.Nil || seen[u] {
			continue
		}
		seen[u] = true
		if _, ok := rq.num[u]; !ok {
			missing = append(missing, u)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if err := rq.s.q.AssignGitHubAPIIDs(rq.ctx, missing); err != nil {
		return fmt.Errorf("assign api ids: %w", err)
	}
	rows, err := rq.s.q.ListGitHubAPIIDs(rq.ctx, missing)
	if err != nil {
		return fmt.Errorf("list api ids: %w", err)
	}
	for _, r := range rows {
		rq.num[r.Uuid] = r.ID
	}
	return nil
}

// numID returns the numeric id of u, as the REST API emits it.
func (rq *request) numID(u uuid.UUID) (any, error) {
	if u == uuid.Nil {
		return nil, nil
	}
	if n, ok := rq.num[u]; ok {
		return n, nil
	}
	if err := rq.prefetch(u); err != nil {
		return nil, err
	}
	return rq.num[u], nil
}

// ---- namespaces ----

// namespace returns the user or organization called name, nil when unknown.
func (rq *request) namespace(name string) (*namespace, error) {
	k := strings.ToLower(name)
	if k == "" {
		return nil, nil
	}
	if ns, ok := rq.ns[k]; ok {
		return ns, nil
	}
	rq.ns[k] = nil
	rows, err := rq.s.q.ListNamespacesByName(rq.ctx, []string{k})
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	for _, r := range rows {
		rq.ns[strings.ToLower(r.Name)] = &namespace{ID: r.ID, Kind: r.Kind, Name: r.Name}
	}
	return rq.ns[k], nil
}

// identityUser loads the profile of user id.
func (rq *request) identityUser(id uuid.UUID) (*identity.User, error) {
	if u, ok := rq.users[id]; ok {
		return u, nil
	}
	u, err := rq.s.d.Identity.User(rq.ctx, id)
	if errors.Is(err, identity.ErrNotFound) {
		rq.users[id] = nil
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load user: %w", err)
	}
	rq.users[id] = &u
	return &u, nil
}

// ---- repositories ----

// repo loads owner/name through the repository service (authorization
// included).
func (rq *request) repo(owner, name string) (repos.Repo, error) {
	k := strings.ToLower(owner + "/" + name)
	if r, ok := rq.repos[k]; ok {
		return r, nil
	}
	if err := rq.read(); err != nil {
		return repos.Repo{}, err
	}
	r, err := rq.s.d.Repos.Get(rq.ctx, rq.who, owner, name)
	if err != nil {
		return repos.Repo{}, err
	}
	rq.repos[k] = r
	rq.repos[strings.ToLower(r.Owner+"/"+r.Name)] = r
	return r, nil
}

// repoByID loads the repository id through the repository service.
func (rq *request) repoByID(id uuid.UUID) (repos.Repo, error) {
	for _, r := range rq.repos {
		if r.ID == id {
			return r, nil
		}
	}
	row, err := rq.s.q.GetRepo(rq.ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return repos.Repo{}, repos.ErrNotFound
	}
	if err != nil {
		return repos.Repo{}, fmt.Errorf("load repository: %w", err)
	}
	return rq.repo(row.OwnerName, row.Repo.Name)
}

// repoFull loads "owner/name".
func (rq *request) repoFull(full string) (repos.Repo, error) {
	owner, name, ok := strings.Cut(full, "/")
	if !ok {
		return repos.Repo{}, repos.ErrNotFound
	}
	return rq.repo(owner, name)
}

// ---- global ids ----

// nodeID is the opaque global id of an object, as the REST API emits it.
func nodeID(kind string, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(kind + ":" + id.String()))
}

// nodeIDString is nodeID for objects without a UUID (commits).
func nodeIDString(kind, key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(kind + ":" + key))
}

// decodeID splits a global id into its kind and key.
func decodeID(id string) (kind, key string, ok bool) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(id, "="))
	if err != nil {
		return "", "", false
	}
	return strings.Cut(string(b), ":")
}

// decodeUUID returns the UUID of a global id of one of kinds.
func decodeUUID(id string, kinds ...string) (string, uuid.UUID, error) {
	kind, key, ok := decodeID(id)
	if ok {
		for _, k := range kinds {
			if k == kind {
				if u, err := uuid.Parse(key); err == nil {
					return kind, u, nil
				}
			}
		}
	}
	return "", uuid.Nil, errorf("NOT_FOUND", "Could not resolve to a node with the global id of '%s'", id)
}

// ---- connections ----

// fetchFn returns up to lim items from offset off, and whether more may
// follow.
type fetchFn func(off, lim int) ([]any, bool, error)

// conn is a connection: a lazily fetched page of a list.
type conn struct {
	node  string // type of the nodes, for the edge type
	fetch fetchFn
	total func() (int, error) // nil: counted by scanning

	first, last int // 0 = unset
	after       int

	loaded bool
	start  int
	items  []any
	more   bool
	err    error
}

const (
	defaultPage = 100
	maxPage     = 100
	maxScan     = 10000
)

// newConn builds a connection from the paging arguments of a field.
func newConn(node string, a args, fetch fetchFn) *conn {
	c := &conn{node: node, fetch: fetch}
	c.first = a.int("first", 0)
	c.last = a.int("last", 0)
	if s := a.str("after"); s != "" {
		c.after = cursorOffset(s)
	}
	return c
}

// staticConn is a connection over an already loaded list.
func staticConn(node string, a args, items []any) *conn {
	c := newConn(node, a, func(off, lim int) ([]any, bool, error) {
		if off >= len(items) {
			return nil, false, nil
		}
		end := min(off+lim, len(items))
		return items[off:end], end < len(items), nil
	})
	n := len(items)
	c.total = func() (int, error) { return n, nil }
	return c
}

func cursor(off int) string {
	return base64.StdEncoding.EncodeToString([]byte("cursor:" + strconv.Itoa(off)))
}

// cursorOffset returns the offset after the item at cursor s.
func cursorOffset(s string) int {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimPrefix(string(b), "cursor:"))
	if err != nil || n < 0 {
		return 0
	}
	return n + 1
}

func clampPage(n int) int {
	if n <= 0 {
		return defaultPage
	}
	return min(n, maxPage)
}

func (c *conn) count() (int, error) {
	if c.total != nil {
		return c.total()
	}
	n := 0
	for n < maxScan {
		items, more, err := c.fetch(n, maxPage)
		if err != nil {
			return 0, err
		}
		n += len(items)
		if !more || len(items) == 0 {
			break
		}
	}
	return n, nil
}

func (c *conn) load() error {
	if c.loaded {
		return c.err
	}
	c.loaded = true
	start, lim := c.after, clampPage(c.first)
	if c.last > 0 && c.first == 0 {
		lim = clampPage(c.last)
		total, err := c.count()
		if err != nil {
			c.err = err
			return err
		}
		start = max(total-lim, c.after)
		if start >= total {
			return nil
		}
	}
	c.start = start
	c.items, c.more, c.err = c.fetch(start, lim)
	if len(c.items) > lim {
		c.items, c.more = c.items[:lim], true
	}
	if c.last > 0 && c.first == 0 {
		c.more = false
	}
	return c.err
}

// filtered keeps the items of src for which keep is true, paging over the
// kept items.
func filtered(src fetchFn, keep func(any) bool) fetchFn {
	return func(off, lim int) ([]any, bool, error) {
		var out []any
		seen, pos := 0, 0
		for pos < maxScan {
			items, more, err := src(pos, maxPage)
			if err != nil {
				return nil, false, err
			}
			for _, it := range items {
				if !keep(it) {
					continue
				}
				if seen >= off {
					if len(out) == lim {
						return out, true, nil
					}
					out = append(out, it)
				}
				seen++
			}
			if !more || len(items) == 0 {
				break
			}
			pos += len(items)
		}
		return out, false, nil
	}
}

// edge is one edge of a connection.
type edge struct {
	node   any
	cursor string
}

// pageInfo is the PageInfo of a connection.
type pageInfo struct {
	hasNext, hasPrev bool
	start, end       string
}

// connFields are the fields of every connection type.
func connFields() map[string]resolver {
	return map[string]resolver{
		"nodes": func(rq *request, src any, a args) (any, error) {
			c := src.(*conn)
			if err := c.load(); err != nil {
				return nil, err
			}
			return c.items, nil
		},
		"edges": func(rq *request, src any, a args) (any, error) {
			c := src.(*conn)
			if err := c.load(); err != nil {
				return nil, err
			}
			out := make([]any, len(c.items))
			for i, it := range c.items {
				out[i] = obj{c.node + "Edge", edge{node: it, cursor: cursor(c.start + i)}}
			}
			return out, nil
		},
		"totalCount": func(rq *request, src any, a args) (any, error) {
			n, err := src.(*conn).count()
			return n, err
		},
		"pageInfo": func(rq *request, src any, a args) (any, error) {
			c := src.(*conn)
			if err := c.load(); err != nil {
				return nil, err
			}
			p := pageInfo{hasNext: c.more, hasPrev: c.start > 0}
			if len(c.items) > 0 {
				p.start, p.end = cursor(c.start), cursor(c.start+len(c.items)-1)
			}
			return obj{"PageInfo", p}, nil
		},
	}
}

func edgeFields() map[string]resolver {
	return map[string]resolver{
		"node":   func(rq *request, src any, a args) (any, error) { return src.(edge).node, nil },
		"cursor": func(rq *request, src any, a args) (any, error) { return src.(edge).cursor, nil },
	}
}

func pageInfoFields() map[string]resolver {
	opt := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	return map[string]resolver{
		"hasNextPage":     func(rq *request, src any, a args) (any, error) { return src.(pageInfo).hasNext, nil },
		"hasPreviousPage": func(rq *request, src any, a args) (any, error) { return src.(pageInfo).hasPrev, nil },
		"startCursor":     func(rq *request, src any, a args) (any, error) { return opt(src.(pageInfo).start), nil },
		"endCursor":       func(rq *request, src any, a args) (any, error) { return opt(src.(pageInfo).end), nil },
	}
}

// emptyConn is a connection with no items.
func emptyConn(node string) resolver {
	return func(rq *request, src any, a args) (any, error) {
		return obj{node + "Connection", staticConn(node, a, nil)}, nil
	}
}

// constant returns a resolver of a constant value.
func constant(v any) resolver {
	return func(*request, any, args) (any, error) { return v, nil }
}

// enumArg normalizes an enum argument (a string or a list of strings).
func enumList(a args, k string) []string {
	out := a.list(k)
	for i := range out {
		out[i] = strings.ToUpper(out[i])
	}
	return out
}
