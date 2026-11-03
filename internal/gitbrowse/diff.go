package gitbrowse

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// File statuses.
const (
	StatusAdded    = "added"
	StatusModified = "modified"
	StatusDeleted  = "deleted"
	StatusRenamed  = "renamed"
	StatusCopied   = "copied"
)

// Line types.
const (
	LineContext = "context"
	LineAdd     = "add"
	LineDelete  = "delete"
	// LineNoNewline is the "\ No newline at end of file" marker.
	LineNoNewline = "no-newline"
)

// maxDiffLineLen bounds the stored text of one diff line.
const maxDiffLineLen = 4096

// DiffLine is one line of a hunk. OldLine and NewLine are 0 when the line
// does not exist on that side.
type DiffLine struct {
	Type    string
	OldLine int
	NewLine int
	Text    string
}

// Hunk is a contiguous change of a file.
type Hunk struct {
	Header   string // the @@ line
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Lines    []DiffLine
}

// FileDiff is the change of one file.
type FileDiff struct {
	Status     string
	OldPath    string // empty for added files
	NewPath    string // empty for deleted files
	OldMode    string
	NewMode    string
	OldOID     string
	NewOID     string
	Similarity int // renames and copies, percent
	Binary     bool
	Additions  int
	Deletions  int
	Hunks      []Hunk
	// Truncated: hunks were dropped because the diff exceeded its limits.
	Truncated bool
}

// Diff is a parsed patch.
type Diff struct {
	Files     []FileDiff
	Additions int
	Deletions int
	// Truncated: lines or files were dropped (MaxDiffLines, MaxDiffFiles,
	// MaxDiffBytes); the totals count what was parsed.
	Truncated bool
}

// diffArgs are the options of every diff (core.quotepath=false keeps
// non-ASCII paths readable).
func diffArgs(extra ...string) []string {
	return append([]string{"-c", "core.quotepath=false", "diff-tree", "-r", "-p", "-M", "--full-index", "--no-color"}, extra...)
}

// diff runs git diff-tree with extra arguments and parses the patch within
// the configured limits.
func (s *Service) diff(ctx context.Context, rp string, extra ...string) (Diff, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	st, err := startStream(ctx, rp, readTimeout, diffArgs(extra...)...)
	if err != nil {
		return Diff{}, fmt.Errorf("diff: %w", err)
	}
	cr := &countReader{r: st, max: s.o.MaxDiffBytes}
	d, stopped, perr := parsePatch(cr, s.o.MaxDiffLines, s.o.MaxDiffFiles)
	if stopped || cr.over {
		cancel()
		d.Truncated = true
	}
	werr := st.Close()
	if perr != nil {
		return d, fmt.Errorf("parse diff: %w", perr)
	}
	if werr != nil && !d.Truncated && ctx.Err() == nil {
		return d, fmt.Errorf("diff: %w", werr)
	}
	return d, nil
}

// countReader returns EOF after max bytes.
type countReader struct {
	r    io.Reader
	n    int64
	max  int64
	over bool
}

func (c *countReader) Read(p []byte) (int, error) {
	if c.n >= c.max {
		c.over = true
		return 0, io.EOF
	}
	if int64(len(p)) > c.max-c.n {
		p = p[:c.max-c.n]
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// readLine reads one line without its newline, keeping at most
// maxDiffLineLen bytes of it. It returns io.EOF only when nothing is left.
func readLine(br *bufio.Reader) (string, error) {
	var b strings.Builder
	read := false
	for {
		frag, err := br.ReadSlice('\n')
		read = read || len(frag) > 0
		if room := maxDiffLineLen - b.Len(); room > 0 {
			b.Write(frag[:min(len(frag), room)])
		}
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && read:
			return strings.TrimSuffix(b.String(), "\n"), nil
		case err != nil:
			return "", err
		}
		return strings.TrimSuffix(b.String(), "\n"), nil
	}
}

// parsePatch parses git's patch output. stopped reports that it gave up
// at the file limit.
func parsePatch(r io.Reader, maxLines, maxFiles int) (d Diff, stopped bool, err error) {
	br := bufio.NewReaderSize(r, 64<<10)
	var (
		f        *FileDiff
		h        *Hunk
		oldN     int
		newN     int
		lines    int
		inHeader bool
	)
	finish := func() {
		if f == nil {
			return
		}
		if f.Status == "" {
			f.Status = StatusModified
		}
		d.Additions += f.Additions
		d.Deletions += f.Deletions
		d.Files = append(d.Files, *f)
		f, h = nil, nil
	}
	for {
		line, rerr := readLine(br)
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			finish()
			return d, false, rerr
		}
		switch {
		case strings.HasPrefix(line, "diff --git "):
			finish()
			if len(d.Files) >= maxFiles {
				return d, true, nil
			}
			f = &FileDiff{}
			f.OldPath, f.NewPath = splitGitHeader(strings.TrimPrefix(line, "diff --git "))
			inHeader = true
		case f == nil:
			// Commit header or noise before the first file.
		case inHeader && strings.HasPrefix(line, "@@"):
			inHeader = false
			fallthrough
		case !inHeader && strings.HasPrefix(line, "@@"):
			hk, ok := parseHunkHeader(line)
			if !ok {
				continue
			}
			oldN, newN = hk.OldStart, hk.NewStart
			if lines >= maxLines {
				f.Truncated, d.Truncated = true, true
				h = nil
				continue
			}
			f.Hunks = append(f.Hunks, hk)
			h = &f.Hunks[len(f.Hunks)-1]
		case inHeader:
			parseExtHeader(f, line)
		default:
			var dl DiffLine
			switch {
			case strings.HasPrefix(line, "+"):
				f.Additions++
				dl = DiffLine{Type: LineAdd, NewLine: newN, Text: line[1:]}
				newN++
			case strings.HasPrefix(line, "-"):
				f.Deletions++
				dl = DiffLine{Type: LineDelete, OldLine: oldN, Text: line[1:]}
				oldN++
			case strings.HasPrefix(line, " "):
				dl = DiffLine{Type: LineContext, OldLine: oldN, NewLine: newN, Text: strings.TrimPrefix(line, " ")}
				oldN++
				newN++
			case strings.HasPrefix(line, `\`):
				dl = DiffLine{Type: LineNoNewline, Text: line}
			default:
				continue
			}
			if h == nil {
				continue
			}
			if lines >= maxLines {
				f.Truncated, d.Truncated = true, true
				h = nil
				continue
			}
			h.Lines = append(h.Lines, dl)
			lines++
		}
	}
	finish()
	return d, false, nil
}

func parseExtHeader(f *FileDiff, line string) {
	cut := func(prefix string) (string, bool) { return strings.CutPrefix(line, prefix) }
	if v, ok := cut("new file mode "); ok {
		f.Status, f.OldPath, f.NewMode = StatusAdded, "", v
	} else if v, ok := cut("deleted file mode "); ok {
		f.Status, f.NewPath, f.OldMode = StatusDeleted, "", v
	} else if v, ok := cut("old mode "); ok {
		f.OldMode = v
	} else if v, ok := cut("new mode "); ok {
		f.NewMode = v
	} else if v, ok := cut("similarity index "); ok {
		f.Similarity, _ = strconv.Atoi(strings.TrimSuffix(v, "%"))
	} else if v, ok := cut("rename from "); ok {
		f.Status, f.OldPath = StatusRenamed, unquote(v)
	} else if v, ok := cut("rename to "); ok {
		f.Status, f.NewPath = StatusRenamed, unquote(v)
	} else if v, ok := cut("copy from "); ok {
		f.Status, f.OldPath = StatusCopied, unquote(v)
	} else if v, ok := cut("copy to "); ok {
		f.Status, f.NewPath = StatusCopied, unquote(v)
	} else if v, ok := cut("index "); ok {
		ids, mode, _ := strings.Cut(v, " ")
		a, b, _ := strings.Cut(ids, "..")
		f.OldOID, f.NewOID = zeroToEmpty(a), zeroToEmpty(b)
		if mode != "" {
			f.OldMode, f.NewMode = mode, mode
		}
	} else if strings.HasPrefix(line, "Binary files ") {
		f.Binary = true
	} else if v, ok := cut("--- "); ok {
		if p, ok := strings.CutPrefix(unquote(trimTab(v)), "a/"); ok && f.Status != StatusAdded {
			f.OldPath = p
		}
	} else if v, ok := cut("+++ "); ok {
		if p, ok := strings.CutPrefix(unquote(trimTab(v)), "b/"); ok && f.Status != StatusDeleted {
			f.NewPath = p
		}
	}
}

// trimTab drops the tab git appends to ---/+++ names containing a space.
func trimTab(s string) string { return strings.TrimSuffix(s, "\t") }

func zeroToEmpty(oid string) string {
	if strings.Trim(oid, "0") == "" {
		return ""
	}
	return oid
}

// unquote decodes a C-quoted git path ("a/caf\303\251").
func unquote(s string) string {
	if len(s) < 2 || s[0] != '"' {
		return s
	}
	if u, err := strconv.Unquote(s); err == nil {
		return u
	}
	return s
}

// splitGitHeader returns the paths of "a/<old> b/<new>" (without prefixes).
func splitGitHeader(s string) (string, string) {
	if strings.HasPrefix(s, `"`) {
		// Quoted old path: find its closing quote.
		for i := 1; i < len(s); i++ {
			if s[i] == '\\' {
				i++
				continue
			}
			if s[i] == '"' {
				a := unquote(s[:i+1])
				b := unquote(strings.TrimSpace(s[i+1:]))
				return strings.TrimPrefix(a, "a/"), strings.TrimPrefix(b, "b/")
			}
		}
		return "", ""
	}
	if strings.HasSuffix(s, `"`) {
		if i := strings.LastIndex(s, ` "`); i >= 0 {
			return strings.TrimPrefix(s[:i], "a/"), strings.TrimPrefix(unquote(s[i+1:]), "b/")
		}
	}
	// Unquoted and identical on both sides (renames are read from the
	// extended header): "a/P b/P".
	if n := len(s); n >= 5 && (n-1)%2 == 0 {
		half := (n - 1) / 2
		a, b := s[:half], s[half+1:]
		if strings.HasPrefix(a, "a/") && strings.HasPrefix(b, "b/") {
			return a[2:], b[2:]
		}
	}
	return "", ""
}

// parseHunkHeader parses "@@ -a[,b] +c[,d] @@ section".
func parseHunkHeader(line string) (Hunk, bool) {
	h := Hunk{Header: line}
	rest, ok := strings.CutPrefix(line, "@@ -")
	if !ok {
		return h, false
	}
	ranges, _, ok := strings.Cut(rest, " @@")
	if !ok {
		return h, false
	}
	o, n, ok := strings.Cut(ranges, " +")
	if !ok {
		return h, false
	}
	var err1, err2 error
	h.OldStart, h.OldLines, err1 = parseRange(o)
	h.NewStart, h.NewLines, err2 = parseRange(n)
	return h, err1 == nil && err2 == nil
}

func parseRange(s string) (int, int, error) {
	a, b, ok := strings.Cut(s, ",")
	start, err := strconv.Atoi(a)
	if err != nil {
		return 0, 0, err
	}
	if !ok {
		return start, 1, nil
	}
	n, err := strconv.Atoi(b)
	return start, n, err
}

// CommitDetail is a commit with its diff against the first parent.
type CommitDetail struct {
	Commit Commit
	Diff   Diff
}

// Commit returns the commit ref resolves to and its diff (against the
// first parent; a root commit against the empty tree).
func (s *Service) Commit(ctx context.Context, repo Repo, ref string) (CommitDetail, error) {
	var cd CommitDetail
	err := s.read(ctx, repo, func(rp string) error {
		r, err := s.resolve(ctx, rp, repo, ref)
		if err != nil {
			return err
		}
		list, err := commitsOf(ctx, rp, 1, r.Commit)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return notFoundf("commit %q", ref)
		}
		cd.Commit = list[0]
		if len(cd.Commit.Parents) == 0 {
			cd.Diff, err = s.diff(ctx, rp, "--root", "--no-commit-id", r.Commit)
		} else {
			cd.Diff, err = s.diff(ctx, rp, cd.Commit.Parents[0], r.Commit)
		}
		return err
	})
	return cd, err
}
