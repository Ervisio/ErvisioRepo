package ghsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/githooks"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
)

// Ref namespaces.
const (
	scratchPrefix  = "refs/sync/github/"
	scratchHeads   = scratchPrefix + "heads/"
	scratchTags    = scratchPrefix + "tags/"
	headsPrefix    = "refs/heads/"
	tagsPrefix     = "refs/tags/"
	conflictPrefix = headsPrefix + "sync-conflict/"
	// ConflictGitHub holds the GitHub commit of a diverged branch.
	ConflictGitHub = conflictPrefix + "github/"
	// ConflictErvisio holds the local commit GitHub won over.
	ConflictErvisio = conflictPrefix + "ervisio/"
)

const (
	fetchTimeout  = time.Hour
	pushTimeout   = 30 * time.Minute
	refsTimeout   = 2 * time.Minute
	pushChunk     = 200
	maxRefErrSize = 300
)

// Ref states.
const (
	stateOK       = "ok"
	stateDiverged = "diverged"
	stateError    = "error"
)

// ConflictRef is the branch holding the GitHub commit of a divergence on
// ref (refs/heads/<branch>).
func ConflictRef(ref string) string { return ConflictGitHub + strings.TrimPrefix(ref, headsPrefix) }

// refState is the per-ref record of a link.
type refState struct {
	local, remote, status, lastError, conflict, resolution string
	retries                                                int32
}

func stateOf(r dbgen.GithubSyncRef) refState {
	return refState{local: r.LocalSha, remote: r.RemoteSha, status: r.Status, lastError: r.LastError,
		conflict: r.ConflictSha, resolution: r.Resolution, retries: r.Retries}
}

// edit is one line of git update-ref --stdin. An empty newSHA deletes;
// verify checks the current value against oldSHA ("" = must not exist).
type edit struct {
	ref, newSHA, oldSHA string
	verify              bool
}

func (e edit) line() string {
	switch {
	case e.newSHA == "" && e.verify:
		return "delete " + e.ref + " " + e.oldSHA + "\n"
	case e.newSHA == "":
		return "delete " + e.ref + "\n"
	case e.verify && e.oldSHA == "":
		return "create " + e.ref + " " + e.newSHA + "\n"
	case e.verify:
		return "update " + e.ref + " " + e.newSHA + " " + e.oldSHA + "\n"
	}
	return "update " + e.ref + " " + e.newSHA + "\n"
}

// group is the edits of one synced ref, applied together or not at all.
type group struct {
	ref   string
	edits []edit
}

// listRefs returns sha by ref name under the given prefixes.
func listRefs(ctx context.Context, repo string, prefixes ...string) (map[string]string, error) {
	out, err := git.Output(ctx, git.Cmd{RepoPath: repo, Timeout: refsTimeout,
		Args: append([]string{"for-each-ref", "--format=%(objectname) %(refname)"}, prefixes...)})
	if err != nil {
		return nil, fmt.Errorf("list refs: %w", err)
	}
	refs := map[string]string{}
	for line := range strings.Lines(string(out)) {
		sha, ref, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok {
			refs[ref] = sha
		}
	}
	return refs, nil
}

// fetch copies every branch and tag of the GitHub repository into the
// scratch namespace of the leased repository. It runs outside any hook
// session: the reference-transaction hook ignores it, and the scratch
// refs are removed (or journaled) by the session that applies the run.
func fetch(ctx context.Context, repo, url string, env []string) error {
	err := git.Exec(ctx, git.Cmd{RepoPath: repo, Env: env, Timeout: fetchTimeout, Args: []string{
		"fetch", "--quiet", "--prune", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules",
		url, "+" + headsPrefix + "*:" + scratchHeads + "*", "+" + tagsPrefix + "*:" + scratchTags + "*"}})
	if err != nil {
		return fmt.Errorf("fetch from GitHub: %w", err)
	}
	return nil
}

// remoteRefs maps the scratch refs back to their GitHub names.
func remoteRefs(scratch map[string]string) map[string]string {
	out := make(map[string]string, len(scratch))
	for ref, sha := range scratch {
		switch {
		case strings.HasPrefix(ref, scratchHeads):
			out[headsPrefix+strings.TrimPrefix(ref, scratchHeads)] = sha
		case strings.HasPrefix(ref, scratchTags):
			out[tagsPrefix+strings.TrimPrefix(ref, scratchTags)] = sha
		}
	}
	return out
}

func cleanup(scratch map[string]string) group {
	g := group{}
	for _, ref := range sortedKeys(scratch) {
		g.edits = append(g.edits, edit{ref: ref})
	}
	return g
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// updateRefs runs one update-ref transaction inside a hook session, so
// that the journal records it and branch protection checks it. It returns
// the refusal of the hooks, or the git error.
func (s *Service) updateRefs(ctx context.Context, l *gitstore.Lease, edits []edit) error {
	if len(edits) == 0 {
		return nil
	}
	var b strings.Builder
	for _, e := range edits {
		b.WriteString(e.line())
	}
	var env []string
	var sess *githooks.Session
	if h := s.hooks.Load(); h != nil {
		var err error
		if sess, err = h.Begin(ctx, l, uuid.Nil, Protocol); err != nil {
			return fmt.Errorf("hook session: %w", err)
		}
		defer sess.Close()
		env = sess.Env()
	}
	err := git.Exec(ctx, git.Cmd{RepoPath: l.Path, Env: env, Timeout: refsTimeout,
		Args: []string{"update-ref", "--stdin"}, Stdin: strings.NewReader(b.String())})
	if err != nil {
		if sess != nil && sess.Rejected() != "" {
			return errors.New(sess.Rejected())
		}
		return fmt.Errorf("update refs: %w", err)
	}
	return nil
}

// updateRefsRaw runs update-ref without a hook session, for refs no
// session has seen (the scratch refs of a run that changed nothing).
func updateRefsRaw(ctx context.Context, repo string, edits []edit) error {
	if len(edits) == 0 {
		return nil
	}
	var b strings.Builder
	for _, e := range edits {
		b.WriteString(e.line())
	}
	if err := git.Exec(ctx, git.Cmd{RepoPath: repo, Timeout: refsTimeout,
		Args: []string{"update-ref", "--stdin"}, Stdin: strings.NewReader(b.String())}); err != nil {
		return fmt.Errorf("update refs: %w", err)
	}
	return nil
}

// apply writes the groups and drops the scratch refs: one transaction
// when possible, otherwise one per group so that a refused ref (branch
// protection, a concurrent change) does not hold back the others. It
// returns the error of each failed group.
func (s *Service) apply(ctx context.Context, l *gitstore.Lease, groups []group, scratch group) map[string]error {
	failed := map[string]error{}
	if len(groups) == 0 {
		if err := updateRefsRaw(ctx, l.Path, scratch.edits); err != nil {
			s.o.Log.WarnContext(ctx, "drop github scratch refs", "repo", l.RepoID, "error", err)
		}
		return failed
	}
	var all []edit
	for _, g := range groups {
		all = append(all, g.edits...)
	}
	if err := s.updateRefs(ctx, l, append(all, scratch.edits...)); err == nil {
		return failed
	} else if len(groups) == 1 {
		failed[groups[0].ref] = err
	} else {
		for _, g := range groups {
			if err := s.updateRefs(ctx, l, g.edits); err != nil {
				failed[g.ref] = err
			}
		}
	}
	if err := s.updateRefs(ctx, l, scratch.edits); err != nil {
		s.o.Log.WarnContext(ctx, "drop github scratch refs", "repo", l.RepoID, "error", err)
	}
	return failed
}

// pushResult is one line of git push --porcelain.
type pushResult struct {
	ok      bool
	deleted bool
	summary string
}

// parsePorcelain reads the per-ref results of git push --porcelain:
// "<flag>\t<from>:<to>\t<summary>", flag ! meaning rejected.
func parsePorcelain(out []byte) map[string]pushResult {
	res := map[string]pushResult{}
	for line := range strings.Lines(string(out)) {
		line = strings.TrimRight(line, "\n")
		if len(line) < 3 || line[1] != '\t' {
			continue
		}
		parts := strings.SplitN(line[2:], "\t", 2)
		_, to, ok := strings.Cut(parts[0], ":")
		if !ok {
			continue
		}
		r := pushResult{ok: line[0] != '!', deleted: line[0] == '-'}
		if len(parts) == 2 {
			r.summary = parts[1]
		}
		res[to] = r
	}
	return res
}

// push runs git push --porcelain with args (options and refspecs after the
// URL) and returns the per-ref results. A failure with no per-ref result
// is returned as an error.
func push(ctx context.Context, repo, url string, env, opts, refspecs []string) (map[string]pushResult, error) {
	var out bytes.Buffer
	args := append(append([]string{"push", "--porcelain", "--no-verify"}, opts...), url)
	err := git.Exec(ctx, git.Cmd{RepoPath: repo, Env: env, Timeout: pushTimeout, Stdout: &out,
		Args: append(args, refspecs...)})
	res := parsePorcelain(out.Bytes())
	if err != nil && len(res) == 0 {
		return nil, fmt.Errorf("push to GitHub: %w", err)
	}
	return res, nil
}

func refError(err error) string {
	msg := err.Error()
	if len(msg) > maxRefErrSize {
		msg = msg[:maxRefErrSize]
	}
	return msg
}

// trusted reports whether GitHub wins a divergence of ref.
func trusted(patterns []string, ref string) bool {
	branch := strings.TrimPrefix(ref, headsPrefix)
	for _, p := range patterns {
		if ok, _ := path.Match(p, branch); ok {
			return true
		}
	}
	return false
}

// syncable is a ref the link follows: branches other than the conflict
// branches, and tags.
func syncable(ref string) bool {
	if strings.HasPrefix(ref, headsPrefix) {
		return !strings.HasPrefix(ref, conflictPrefix)
	}
	return strings.HasPrefix(ref, tagsPrefix)
}

func union(a, b map[string]string) []string {
	m := map[string]string{}
	for k := range a {
		if syncable(k) {
			m[k] = ""
		}
	}
	for k := range b {
		if syncable(k) {
			m[k] = ""
		}
	}
	return sortedKeys(m)
}

// pushSpec is one ref to send to GitHub: sha "" deletes. expect is the
// value GitHub must still have (force-with-lease), "" = absent.
type pushSpec struct {
	ref, sha, expect string
}

// plan is the outcome of comparing both sides.
type plan struct {
	groups []group
	pushes []pushSpec
	states map[string]refState
}

// planMirror makes the local repository follow GitHub: every ref takes the
// GitHub value; local-only refs are deleted only with prune.
func planMirror(link dbgen.GithubSyncLink, local, remote map[string]string) plan {
	p := plan{states: map[string]refState{}}
	for _, ref := range union(local, remote) {
		l, r := local[ref], remote[ref]
		switch {
		case l == r:
			p.states[ref] = refState{local: l, remote: r, status: stateOK}
		case r != "":
			p.groups = append(p.groups, group{ref: ref, edits: []edit{{ref: ref, newSHA: r, oldSHA: l, verify: true}}})
			p.states[ref] = refState{local: r, remote: r, status: stateOK}
		case link.Prune:
			p.groups = append(p.groups, group{ref: ref, edits: []edit{{ref: ref, oldSHA: l, verify: true}}})
		default:
			p.states[ref] = refState{local: l, status: stateOK}
		}
	}
	return p
}

// planSync compares both sides of a sync link against the state of the
// previous run. Only fast-forwards are applied; a real divergence keeps
// the GitHub commit at refs/heads/sync-conflict/github/<branch>.
func (s *Service) planSync(ctx context.Context, repo string, link dbgen.GithubSyncLink, local, remote map[string]string, prev map[string]refState) (plan, error) {
	p := plan{states: map[string]refState{}}
	for _, ref := range union(local, remote) {
		l, r, old := local[ref], remote[ref], prev[ref]
		ok := refState{local: l, remote: r, status: stateOK}
		switch {
		case l == r:
			p.states[ref] = ok
		case l == "":
			if link.Prune && old.local != "" && old.remote == r {
				// Deleted here since the last run: delete on GitHub.
				p.pushes = append(p.pushes, pushSpec{ref: ref, expect: r})
				p.states[ref] = ok
				continue
			}
			p.groups = append(p.groups, group{ref: ref, edits: []edit{{ref: ref, newSHA: r, verify: true}}})
			p.states[ref] = refState{local: r, remote: r, status: stateOK}
		case r == "":
			if link.Prune && old.remote != "" && old.local == l {
				// Deleted on GitHub since the last run: delete here.
				p.groups = append(p.groups, group{ref: ref, edits: []edit{{ref: ref, oldSHA: l, verify: true}}})
				p.states[ref] = ok
				continue
			}
			p.pushes = append(p.pushes, pushSpec{ref: ref, sha: l})
			p.states[ref] = ok
		case strings.HasPrefix(ref, tagsPrefix):
			p.states[ref] = refState{local: l, remote: r, status: stateError,
				lastError: "the tag points to different objects here and on GitHub", retries: old.retries + 1}
		case old.status == stateDiverged && old.local == l && old.conflict == r:
			p.states[ref] = old // still diverged, nothing new on either side
		default:
			behind, err := git.IsAncestor(ctx, repo, nil, l, r)
			if err != nil {
				return p, fmt.Errorf("compare %s: %w", ref, err)
			}
			if behind {
				p.groups = append(p.groups, group{ref: ref, edits: []edit{{ref: ref, newSHA: r, oldSHA: l, verify: true}}})
				p.states[ref] = refState{local: r, remote: r, status: stateOK}
				continue
			}
			ahead, err := git.IsAncestor(ctx, repo, nil, r, l)
			if err != nil {
				return p, fmt.Errorf("compare %s: %w", ref, err)
			}
			if ahead {
				p.pushes = append(p.pushes, pushSpec{ref: ref, sha: l, expect: r})
				p.states[ref] = ok
				continue
			}
			branch := strings.TrimPrefix(ref, headsPrefix)
			if trusted(link.TrustGithub, ref) {
				p.groups = append(p.groups, group{ref: ref, edits: []edit{
					{ref: ConflictErvisio + branch, newSHA: l},
					{ref: ref, newSHA: r, oldSHA: l, verify: true},
				}})
				p.states[ref] = refState{local: r, remote: r, status: stateOK}
				continue
			}
			p.groups = append(p.groups, group{ref: ref, edits: []edit{{ref: ConflictGitHub + branch, newSHA: r}}})
			p.states[ref] = refState{local: l, remote: r, status: stateDiverged, conflict: r,
				resolution: old.resolution,
				lastError:  "diverged: the GitHub commit is kept at " + ConflictGitHub + branch}
		}
	}
	return p, nil
}

// pushSpecs sends specs to GitHub with a lease per ref and records the
// outcome in states.
func pushSpecs(ctx context.Context, repo, url string, env []string, specs []pushSpec, states map[string]refState, prev map[string]refState) error {
	for start := 0; start < len(specs); start += pushChunk {
		chunk := specs[start:min(start+pushChunk, len(specs))]
		var opts, refspecs []string
		for _, sp := range chunk {
			opts = append(opts, "--force-with-lease="+sp.ref+":"+sp.expect)
			refspecs = append(refspecs, sp.sha+":"+sp.ref)
		}
		res, err := push(ctx, repo, url, env, opts, refspecs)
		if err != nil {
			return err
		}
		for _, sp := range chunk {
			r, seen := res[sp.ref]
			st := states[sp.ref]
			switch {
			case seen && r.ok:
				st.remote, st.status, st.lastError, st.retries = sp.sha, stateOK, "", 0
			default:
				msg := "not pushed"
				if seen && r.summary != "" {
					msg = "GitHub refused the push: " + r.summary
				}
				st.remote, st.status, st.lastError, st.retries = sp.expect, stateError, msg, prev[sp.ref].retries+1
			}
			states[sp.ref] = st
		}
	}
	return nil
}
