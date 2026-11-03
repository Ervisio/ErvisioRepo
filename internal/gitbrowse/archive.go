package gitbrowse

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/git"
)

// Archive formats.
const (
	FormatZip   = "zip"
	FormatTarGz = "tar.gz"
)

// ArchiveStart is called by Archive before the content is written, with the
// resolved ref and the suggested file name; it returns the destination.
type ArchiveStart func(r Resolved, filename string) (io.Writer, error)

// Archive streams a zip or tar.gz of ref produced by git archive, under the
// Read lease. Every entry is prefixed by "<name>-<ref>/".
func (s *Service) Archive(ctx context.Context, repo Repo, ref, format string, start ArchiveStart) error {
	if format != FormatZip && format != FormatTarGz {
		return invalidf("unknown archive format %q", format)
	}
	return s.read(ctx, repo, func(rp string) error {
		r, err := s.resolve(ctx, rp, repo, ref)
		if err != nil {
			return err
		}
		base := repo.Name + "-" + archiveLabel(r)
		w, err := start(r, base+"."+format)
		if err != nil {
			return err
		}
		if err := git.Exec(ctx, git.Cmd{RepoPath: rp, Args: []string{"archive", "--format=" + format,
			"--prefix=" + base + "/", r.Commit}, Stdout: w, Timeout: s.o.ArchiveTimeout}); err != nil {
			return fmt.Errorf("%w: git archive: %w", errStreaming, err)
		}
		return nil
	})
}

// archiveLabel is the ref part of an archive name, safe as a file name.
func archiveLabel(r Resolved) string {
	label := r.Commit
	switch r.Kind {
	case KindBranch:
		label = strings.TrimPrefix(r.Name, "refs/heads/")
	case KindTag:
		label = strings.TrimPrefix(r.Name, "refs/tags/")
	default:
		if len(label) > 12 {
			label = label[:12]
		}
	}
	return strings.Map(func(c rune) rune {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			return c
		}
		return '-'
	}, label)
}
