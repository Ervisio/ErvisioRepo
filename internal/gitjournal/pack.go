package gitjournal

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/sys"
)

// refState is the ref set of a hot repository.
type refState struct {
	Head string
	Refs map[string]string
}

func (r refState) equal(s *gitstore.RefsSnapshot) bool {
	if s == nil {
		return len(r.Refs) == 0
	}
	if r.Head != s.Head || len(r.Refs) != len(s.Refs) {
		return false
	}
	for k, v := range r.Refs {
		if s.Refs[k] != v {
			return false
		}
	}
	return true
}

// tips returns the distinct object IDs of refs, sorted.
func tips(refs map[string]string) []string {
	seen := make(map[string]bool, len(refs))
	out := make([]string, 0, len(refs))
	for _, oid := range refs {
		if !seen[oid] {
			seen[oid] = true
			out = append(out, oid)
		}
	}
	slices.Sort(out)
	return out
}

// readRefs lists the refs and the HEAD target of the repository at path.
func (j *Journal) readRefs(ctx context.Context, path string) (refState, error) {
	out, err := git.Run(ctx, path, "for-each-ref", "--format=%(objectname) %(refname)")
	if err != nil {
		return refState{}, fmt.Errorf("list refs: %w", err)
	}
	st := refState{Refs: map[string]string{}}
	for line := range strings.Lines(string(out)) {
		oid, name, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if git.ValidateObjectID(oid) != nil || git.ValidateRefName(name) != nil {
			j.log.WarnContext(ctx, "skipping unjournalable ref", "path", path, "ref", name)
			continue
		}
		st.Refs[name] = oid
	}
	head, err := git.Run(ctx, path, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		var ee *sys.ExitError
		if !errors.As(err, &ee) || ee.Code != 1 {
			return refState{}, fmt.Errorf("read HEAD: %w", err)
		}
		head = nil // detached HEAD
	}
	if h := strings.TrimSpace(string(head)); h != "" && git.ValidateRefName(h) == nil {
		st.Head = h
	}
	return st, nil
}

// existing keeps the object IDs present in the repository (env selects a
// quarantine).
func existing(ctx context.Context, path string, env []string, oids []string) ([]string, error) {
	if len(oids) == 0 {
		return nil, nil
	}
	var out bytes.Buffer
	in := strings.Join(oids, "\n") + "\n"
	if err := git.Exec(ctx, git.Cmd{RepoPath: path, Env: env, Args: []string{"cat-file", "--batch-check=%(objectname)"},
		Stdin: strings.NewReader(in), Stdout: &out}); err != nil {
		return nil, fmt.Errorf("check objects: %w", err)
	}
	var keep []string
	for line := range strings.Lines(out.String()) {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasSuffix(line, " missing") {
			keep = append(keep, line)
		}
	}
	return keep, nil
}

// revInput is the rev-list/pack-objects --stdin input: include ^exclude
// ("--not" is not accepted by rev-list --stdin before git 2.42).
func revInput(include, exclude []string) string {
	var b strings.Builder
	for _, oid := range include {
		b.WriteString(oid + "\n")
	}
	for _, oid := range exclude {
		b.WriteString("^" + oid + "\n")
	}
	return b.String()
}

// largeBlob is a blob over the file size limit.
type largeBlob struct {
	OID  string
	Path string
	Size int64
}

// scanBlobs walks the objects reachable from include and not from exclude
// and returns the blobs larger than limit (at most max of them).
func scanBlobs(ctx context.Context, path string, env, include, exclude []string, limit int64, maxReport int) ([]largeBlob, error) {
	if len(include) == 0 {
		return nil, nil
	}
	pr, pw := io.Pipe()
	revs, err := git.Start(ctx, git.Cmd{RepoPath: path, Env: env,
		Args:  []string{"rev-list", "--objects", "--stdin"},
		Stdin: strings.NewReader(revInput(include, exclude)), Stdout: pw})
	if err != nil {
		return nil, fmt.Errorf("list objects: %w", err)
	}
	var revErr error
	done := make(chan struct{})
	go func() {
		revErr = revs.Wait()
		_ = pw.CloseWithError(revErr)
		close(done)
	}()
	var found []largeBlob
	cr, cw := io.Pipe()
	catErrc := make(chan error, 1)
	go func() {
		err := git.Exec(ctx, git.Cmd{RepoPath: path, Env: env,
			Args:  []string{"cat-file", "--batch-check=%(objecttype) %(objectname) %(objectsize) %(rest)"},
			Stdin: pr, Stdout: cw})
		_ = cw.CloseWithError(err)
		catErrc <- err
	}()
	sc := bufio.NewScanner(cr)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		f := strings.SplitN(sc.Text(), " ", 4)
		if len(f) < 3 || f[0] != "blob" {
			continue
		}
		size, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil || size <= limit {
			continue
		}
		if len(found) < maxReport {
			b := largeBlob{OID: f[1], Size: size}
			if len(f) == 4 {
				b.Path = f[3]
			}
			found = append(found, b)
		}
	}
	scanErr := sc.Err()
	_, _ = io.Copy(io.Discard, cr)
	catErr := <-catErrc
	_ = pr.Close()
	<-done
	if err := errors.Join(revErr, catErr, scanErr); err != nil {
		return nil, fmt.Errorf("scan objects: %w", err)
	}
	return found, nil
}

// quarantineSize is the size in bytes of the objects in a push quarantine,
// as reported by git count-objects.
func quarantineSize(ctx context.Context, path string, env []string) (int64, error) {
	var out bytes.Buffer
	if err := git.Exec(ctx, git.Cmd{RepoPath: path, Env: env, Args: []string{"count-objects", "-v"}, Stdout: &out}); err != nil {
		return 0, fmt.Errorf("count objects: %w", err)
	}
	var kib int64
	for line := range strings.Lines(out.String()) {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ": ")
		if !ok || (k != "size" && k != "size-pack") {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			kib += n
		}
	}
	return kib << 10, nil
}

// packFile is a pack written to a scratch directory.
type packFile struct {
	SHA       string
	Pack, Idx string // local paths
	Size      int64
	SHA256    string
	IdxSHA256 string
}

// writePack runs pack-objects for include --not exclude into a new scratch
// directory under the repository. It returns nil when there is no object
// to pack. The caller removes the returned directory.
func writePack(ctx context.Context, path string, env, include, exclude []string) (*packFile, string, error) {
	if len(include) == 0 {
		return nil, "", nil
	}
	dir, err := os.MkdirTemp(path, "erepo-pack-")
	if err != nil {
		return nil, "", fmt.Errorf("scratch directory: %w", err)
	}
	var out bytes.Buffer
	args := []string{"pack-objects", "--revs", "--non-empty", "--delta-base-offset", "-q", filepath.Join(dir, "p")}
	if err := git.Exec(ctx, git.Cmd{RepoPath: path, Env: env, Args: args, Timeout: packTimeout,
		Stdin: strings.NewReader(revInput(include, exclude)), Stdout: &out}); err != nil {
		return nil, dir, fmt.Errorf("pack objects: %w", err)
	}
	sha := strings.TrimSpace(out.String())
	if sha == "" {
		return nil, dir, nil
	}
	if err := git.ValidateObjectID(sha); err != nil {
		return nil, dir, fmt.Errorf("pack objects: checksum %q: %w", sha, err)
	}
	p := &packFile{SHA: sha, Pack: filepath.Join(dir, "p-"+sha+".pack"), Idx: filepath.Join(dir, "p-"+sha+".idx")}
	if p.Size, p.SHA256, err = hashFile(p.Pack); err != nil {
		return nil, dir, err
	}
	if _, p.IdxSHA256, err = hashFile(p.Idx); err != nil {
		return nil, dir, err
	}
	return p, dir, nil
}

func hashFile(path string) (int64, string, error) {
	f, err := os.Open(path) //nolint:gosec // scratch file written by pack-objects
	if err != nil {
		return 0, "", fmt.Errorf("open pack: %w", err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", fmt.Errorf("hash pack: %w", err)
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// upload stores a pack and its index under the given keys.
func (j *Journal) upload(ctx context.Context, p *packFile, packKey, idxKey string) error {
	for _, f := range []struct{ path, key, sum string }{{p.Pack, packKey, p.SHA256}, {p.Idx, idxKey, p.IdxSHA256}} {
		if err := j.putFile(ctx, f.path, f.key, f.sum); err != nil {
			return err
		}
	}
	return nil
}

func (j *Journal) putFile(ctx context.Context, path, key, sum string) error {
	raw, err := hex.DecodeString(sum)
	if err != nil {
		return fmt.Errorf("upload %s: %w", key, err)
	}
	f, err := os.Open(path) //nolint:gosec // scratch file written by pack-objects
	if err != nil {
		return fmt.Errorf("upload %s: %w", key, err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return fmt.Errorf("upload %s: %w", key, err)
	}
	if _, err := j.store.PutObject(ctx, key, f, st.Size(), raw); err != nil {
		return fmt.Errorf("upload %s: %w", key, err)
	}
	return nil
}

// uploadSegment packs include --not exclude and stores it as segment seq.
// It returns nil when there is nothing to pack. check, if set, sees the
// pack size before the upload and can refuse it.
func (j *Journal) uploadSegment(ctx context.Context, repo uuid.UUID, path string, env, include, exclude []string, seq int64,
	check func(size int64) error) (*gitstore.Segment, error) {
	p, dir, err := writePack(ctx, path, env, include, exclude)
	if dir != "" {
		defer func() { _ = os.RemoveAll(dir) }()
	}
	if err != nil || p == nil {
		return nil, err
	}
	seg := &gitstore.Segment{
		Seq: seq, SHA: p.SHA, Size: p.Size, SHA256: p.SHA256, IdxSHA256: p.IdxSHA256,
		Pack: gitstore.SegmentKey(repo, seq, p.SHA, gitstore.ExtPack),
		Idx:  gitstore.SegmentKey(repo, seq, p.SHA, gitstore.ExtIdx),
	}
	if check != nil {
		if err := check(seg.Size); err != nil {
			return nil, err
		}
	}
	if err := j.upload(ctx, p, seg.Pack, seg.Idx); err != nil {
		return nil, err
	}
	return seg, nil
}
