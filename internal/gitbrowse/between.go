package gitbrowse

import (
	"context"
	"strings"
)

// MaxBetweenCommits bounds CommitsBetween.
const MaxBetweenCommits = 1000

// CommitsBetween returns up to limit commits reachable from newOID and not
// from oldOID, newest first. An empty or all-zero oldOID (a new branch)
// returns the ancestors of newOID. Both ids must be full lowercase hex
// object ids; anything else is ErrInvalid.
func (s *Service) CommitsBetween(ctx context.Context, repo Repo, oldOID, newOID string, limit int) ([]Commit, error) {
	if strings.Trim(oldOID, "0") == "" {
		oldOID = ""
	}
	if !isOID(newOID) || (oldOID != "" && !isOID(oldOID)) {
		return nil, invalidf("invalid commit id")
	}
	if limit <= 0 || limit > MaxBetweenCommits {
		limit = MaxBetweenCommits
	}
	revs := []string{newOID}
	if oldOID != "" {
		revs = append(revs, "^"+oldOID)
	}
	var list []Commit
	err := s.read(ctx, repo, func(rp string) error {
		var err error
		list, err = commitsOf(ctx, rp, limit, revs...)
		return err
	})
	return list, err
}

// isOID reports whether s is a full SHA-1 or SHA-256 hex object id.
func isOID(s string) bool {
	return (len(s) == 40 || len(s) == 64) && isHex(s)
}
