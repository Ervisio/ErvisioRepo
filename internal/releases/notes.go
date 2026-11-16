package releases

import (
	"context"
	"fmt"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
)

// maxNoteCommits bounds the commits scanned for merged pull requests.
const maxNoteCommits = 5000

// NotesInput selects what the generated notes cover: the commits of Tag (or
// of Target when the tag does not exist yet) that PreviousTag does not have.
type NotesInput struct {
	Tag         string
	Target      string
	PreviousTag string // empty: the newest published release
}

// NotePull is a merged pull request listed in generated notes.
type NotePull struct {
	Number int64
	Title  string
	Author string
}

// Notes is the raw material of generated release notes; the caller writes
// the markdown, which needs the public URLs.
type Notes struct {
	Name     string
	Previous string // tag the changes are counted from; empty: the whole history
	Head     string // tag, or target ref, the changes end at
	Pulls    []NotePull
}

// GenerateNotes lists the pull requests merged between two tags.
func (s *Service) GenerateNotes(ctx context.Context, who auth.Principal, owner, name string, in NotesInput) (Notes, error) {
	r, err := s.access(ctx, who, owner, name, true)
	if err != nil {
		return Notes{}, err
	}
	if err := git.ValidateTagName(in.Tag); err != nil {
		return Notes{}, invalid("tag: %v", err)
	}
	if err := validateFields("", "", in.Target); err != nil {
		return Notes{}, err
	}
	prev := in.PreviousTag
	inferred := prev == ""
	if inferred {
		list, err := s.q.ListReleases(ctx, dbgen.ListReleasesParams{RepoID: r.ID, Lim: 50})
		if err != nil {
			return Notes{}, fmt.Errorf("list releases: %w", err)
		}
		for _, x := range list {
			if x.Tag != in.Tag {
				prev = x.Tag
				break
			}
		}
	} else if err := git.ValidateTagName(prev); err != nil {
		return Notes{}, invalid("previous_tag_name: %v", err)
	}
	if s.o.Git == nil {
		return Notes{}, fmt.Errorf("git storage is not available")
	}
	target := in.Target
	if target == "" {
		target = r.DefaultBranch
	}
	l, err := s.o.Git.Acquire(ctx, r.ID, gitstore.Read)
	if err != nil {
		return Notes{}, fmt.Errorf("acquire repository: %w", err)
	}
	defer l.Release()
	headName := in.Tag
	head, err := s.resolveCommit(ctx, l.Path, "refs/tags/"+in.Tag)
	if err != nil {
		return Notes{}, err
	}
	if head == "" {
		headName = target
		if head, err = s.resolveCommit(ctx, l.Path, target); err != nil {
			return Notes{}, err
		}
		if head == "" {
			return Notes{}, invalid("target %q does not exist in the repository", target)
		}
	}
	var base string
	if prev != "" {
		if base, err = s.resolveCommit(ctx, l.Path, "refs/tags/"+prev); err != nil {
			return Notes{}, err
		}
		if base == "" {
			if !inferred {
				return Notes{}, invalid("previous_tag_name %q does not exist in the repository", prev)
			}
			prev = ""
		}
	}
	ids, _, err := git.RevList(ctx, l.Path, nil, base, head, maxNoteCommits)
	if err != nil {
		return Notes{}, fmt.Errorf("list release commits: %w", err)
	}
	out := Notes{Name: in.Tag, Previous: prev, Head: headName}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q.ListMergedPullsByCommit(ctx, dbgen.ListMergedPullsByCommitParams{RepoID: r.ID, Shas: ids})
	if err != nil {
		return Notes{}, fmt.Errorf("list merged pull requests: %w", err)
	}
	for _, row := range rows {
		out.Pulls = append(out.Pulls, NotePull{Number: row.Number, Title: row.Title, Author: row.Author})
	}
	return out, nil
}

// resolveCommit returns the commit a revision points to, or "" when it does
// not exist.
func (s *Service) resolveCommit(ctx context.Context, path, rev string) (string, error) {
	out, err := git.Run(ctx, path, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	if err != nil {
		if isExit1(err) {
			return "", nil
		}
		return "", fmt.Errorf("resolve %s: %w", rev, err)
	}
	return strings.TrimSpace(string(out)), nil
}
