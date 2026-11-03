package gitbrowse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Ervisio/ErvisioRepo/internal/git"
)

// binarySniff is how many leading bytes are checked for NUL (as git does).
const binarySniff = 8000

// lfsPointerMax is the largest blob parsed as a Git LFS pointer.
const lfsPointerMax = 1024

const lfsVersion = "version https://git-lfs.github.com/spec/v1"

// LFSPointer is a Git LFS pointer file.
type LFSPointer struct {
	OID  string // sha256 hex
	Size int64
}

// Blob is a file of a tree.
type Blob struct {
	Ref    Resolved
	Path   string
	OID    string
	Mode   string
	Size   int64
	Binary bool
	// LFS is set when the blob is a Git LFS pointer.
	LFS *LFSPointer
	// Content is the text of a non-binary blob within MaxBlobSize; nil
	// otherwise (Truncated tells which).
	Content   []byte
	Truncated bool
	// ContentType is the type the raw endpoint serves.
	ContentType string
}

// IsBinary applies the git heuristic: a NUL byte in the first 8000 bytes,
// or invalid UTF-8 (the UI shows text as UTF-8 only).
func IsBinary(b []byte) bool {
	head := b[:min(len(b), binarySniff)]
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	if len(b) <= binarySniff {
		return !utf8.Valid(head)
	}
	// A multi-byte rune can be cut at the end of head.
	for cut := 0; cut < utf8.UTFMax && cut < len(head); cut++ {
		if utf8.Valid(head[:len(head)-cut]) {
			return false
		}
	}
	return true
}

// ParseLFSPointer returns the pointer described by b, if it is one.
func ParseLFSPointer(b []byte) (*LFSPointer, bool) {
	if len(b) > lfsPointerMax || !bytes.HasPrefix(b, []byte(lfsVersion+"\n")) {
		return nil, false
	}
	p := &LFSPointer{Size: -1}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(line, " ")
		switch k {
		case "oid":
			if h, ok := strings.CutPrefix(v, "sha256:"); ok && len(h) == 64 && isHex(h) {
				p.OID = h
			}
		case "size":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
				p.Size = n
			}
		}
	}
	if p.OID == "" || p.Size < 0 {
		return nil, false
	}
	return p, true
}

// safeImages are the binary types served inline by the raw endpoint; any
// other binary is an attachment. SVG and HTML are never served as such.
var safeImages = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".bmp": "image/bmp", ".ico": "image/x-icon", ".avif": "image/avif",
	".pdf": "application/pdf",
}

// ContentType returns the type served for a blob: text is always
// text/plain (never HTML or SVG, which could run scripts), known images
// keep their type, anything else is application/octet-stream.
func ContentType(name string, binary bool) string {
	if !binary {
		return "text/plain; charset=utf-8"
	}
	if t, ok := safeImages[strings.ToLower(path.Ext(name))]; ok {
		return t
	}
	return "application/octet-stream"
}

// Blob returns the file at path of ref.
func (s *Service) Blob(ctx context.Context, repo Repo, ref, path string) (Blob, error) {
	if path == "" {
		return Blob{}, invalidf("path is required")
	}
	if err := git.ValidateTreePath(path); err != nil {
		return Blob{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var b Blob
	err := s.read(ctx, repo, func(rp string) error {
		r, err := s.resolve(ctx, rp, repo, ref)
		if err != nil {
			return err
		}
		b, err = s.blob(ctx, rp, r, path)
		return err
	})
	return b, err
}

// Readme returns the README of the directory at path of ref.
func (s *Service) Readme(ctx context.Context, repo Repo, ref, path string) (Blob, error) {
	if err := git.ValidateTreePath(path); err != nil {
		return Blob{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var b Blob
	err := s.read(ctx, repo, func(rp string) error {
		r, err := s.resolve(ctx, rp, repo, ref)
		if err != nil {
			return err
		}
		entries, _, err := s.listTree(ctx, rp, r.Commit, path)
		if err != nil {
			return err
		}
		name := readmeOf(entries)
		if name == "" {
			return notFoundf("no README in %q", path)
		}
		b, err = s.blob(ctx, rp, r, name)
		return err
	})
	return b, err
}

func (s *Service) blob(ctx context.Context, rp string, r Resolved, p string) (Blob, error) {
	b := Blob{Ref: r, Path: p}
	info, err := s.object(ctx, rp, r.Commit, p)
	if err != nil {
		return b, err
	}
	if info.Type != "blob" {
		return b, invalidf("%q is not a file", p)
	}
	b.OID, b.Size = info.OID, info.Size
	b.Mode = s.mode(ctx, rp, r.Commit, p)
	if info.Size > s.o.MaxBlobSize {
		// Sniff the head only: enough for the binary check.
		head, err := s.head(ctx, rp, info.OID, binarySniff)
		if err != nil {
			return b, err
		}
		b.Binary, b.Truncated = IsBinary(head), true
		b.ContentType = ContentType(p, b.Binary)
		return b, nil
	}
	_, data, err := s.cat.Object(ctx, rp, info.OID, s.o.MaxBlobSize)
	if err != nil {
		return b, fmt.Errorf("read blob: %w", err)
	}
	if ptr, ok := ParseLFSPointer(data); ok {
		b.LFS = ptr
	}
	b.Binary = IsBinary(data)
	if !b.Binary {
		b.Content = data
	}
	b.ContentType = ContentType(p, b.Binary)
	return b, nil
}

// mode returns the file mode of commit:path ("" if unknown).
func (s *Service) mode(ctx context.Context, rp, commit, p string) string {
	out, err := run(ctx, rp, readTimeout, "ls-tree", "-z", commit, "--", p)
	if err != nil {
		return ""
	}
	m, _, _ := strings.Cut(string(out), " ")
	return m
}

// head returns the first n bytes of a blob.
func (s *Service) head(ctx context.Context, rp, oid string, n int64) ([]byte, error) {
	st, err := startStream(ctx, rp, readTimeout, "cat-file", "blob", oid)
	if err != nil {
		return nil, fmt.Errorf("read blob: %w", err)
	}
	buf, rerr := io.ReadAll(io.LimitReader(st, n))
	_ = st.Close() // killed after n bytes
	if rerr != nil {
		return nil, fmt.Errorf("read blob: %w", rerr)
	}
	return buf, nil
}

// RawStart is called by Raw before the content is written, with the blob
// metadata (Content is not loaded); it returns the destination.
type RawStart func(b Blob) (io.Writer, error)

// Raw streams the file at path of ref. The Read lease is held until the
// copy ends.
func (s *Service) Raw(ctx context.Context, repo Repo, ref, path string, start RawStart) error {
	if path == "" {
		return invalidf("path is required")
	}
	if err := git.ValidateTreePath(path); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return s.read(ctx, repo, func(rp string) error {
		r, err := s.resolve(ctx, rp, repo, ref)
		if err != nil {
			return err
		}
		info, err := s.object(ctx, rp, r.Commit, path)
		if err != nil {
			return err
		}
		if info.Type != "blob" {
			return invalidf("%q is not a file", path)
		}
		head, err := s.head(ctx, rp, info.OID, binarySniff)
		if err != nil {
			return err
		}
		b := Blob{Ref: r, Path: path, OID: info.OID, Size: info.Size, Binary: IsBinary(head)}
		b.ContentType = ContentType(path, b.Binary)
		if ptr, ok := ParseLFSPointer(head); ok && info.Size <= lfsPointerMax {
			b.LFS = ptr
		}
		w, err := start(b)
		if err != nil {
			return err
		}
		if err := git.Exec(ctx, git.Cmd{RepoPath: rp, Args: []string{"cat-file", "blob", info.OID},
			Stdout: w, Timeout: s.o.ArchiveTimeout}); err != nil {
			return fmt.Errorf("%w: stream blob: %w", errStreaming, err)
		}
		return nil
	})
}

// errStreaming marks a failure after the response has started.
var errStreaming = errors.New("streaming")

// IsStreaming reports whether err happened after the response started (the
// status line is already sent).
func IsStreaming(err error) bool { return errors.Is(err, errStreaming) }
