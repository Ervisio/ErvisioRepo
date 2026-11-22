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
	// ContentType is the blob's safe MIME type for previews.
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

// rawBinaryTypes preserves Raw's existing MIME types. SVG is active content
// when navigated to directly and is never allowed as a preview.
var rawBinaryTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".bmp": "image/bmp", ".ico": "image/x-icon", ".avif": "image/avif",
	".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".aac": "audio/aac", ".wav": "audio/wav",
	".ogg": "audio/ogg", ".oga": "audio/ogg", ".opus": "audio/ogg", ".flac": "audio/flac",
	".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm", ".ogv": "video/ogg",
	".mov": "video/quicktime", ".pdf": "application/pdf",
}

var additionalMediaTypes = map[string]string{
	".apng": "image/apng", ".jpe": "image/jpeg", ".jfif": "image/jpeg", ".cur": "image/x-icon",
	".tif": "image/tiff", ".tiff": "image/tiff", ".jxl": "image/jxl",
	".heic": "image/heic", ".heif": "image/heif",
	".mp2": "audio/mpeg", ".m4b": "audio/mp4", ".wave": "audio/wav", ".weba": "audio/webm",
	".aif": "audio/aiff", ".aiff": "audio/aiff", ".mpeg": "video/mpeg", ".mpg": "video/mpeg",
	".avi": "video/x-msvideo", ".mkv": "video/x-matroska", ".3gp": "video/3gpp", ".3g2": "video/3gpp2",
}

// MediaType is the allowlisted MIME type for name, or empty for other files.
func MediaType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if t := additionalMediaTypes[ext]; t != "" {
		return t
	}
	if ext == ".pdf" {
		return ""
	}
	return rawBinaryTypes[ext]
}

// ContentType returns the raw type served for a blob: text is always
// text/plain (never HTML or SVG, which could run scripts), known media
// keep their type, anything else is application/octet-stream.
func ContentType(name string, binary bool) string {
	if !binary {
		return "text/plain; charset=utf-8"
	}
	if t := rawBinaryTypes[strings.ToLower(path.Ext(name))]; t != "" {
		return t
	}
	return "application/octet-stream"
}

func (b *Blob) classify(data []byte) {
	b.LFS, _ = ParseLFSPointer(data)
	b.Binary = IsBinary(data)
	if !b.Binary {
		b.Content = data
	}
	b.setPreviewType()
}

func (b *Blob) setPreviewType() {
	if b.Binary || b.LFS != nil {
		if t := MediaType(b.Path); t != "" {
			b.ContentType = t
			return
		}
	}
	b.ContentType = ContentType(b.Path, b.Binary)
}

// Blob returns the file at path of ref.
func (s *Service) Blob(ctx context.Context, repo Repo, ref, path string) (Blob, error) {
	if path == "" {
		return Blob{}, invalidf("path is required")
	}
	if err := git.ValidateTreePath(path); err != nil {
		return Blob{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if b, ok, err := s.coldBlobRead(ctx, repo, ref, path, false); ok {
		return b, err
	}
	var b Blob
	err := s.read(ctx, repo, func(rp string) error {
		r, err := s.resolve(ctx, rp, repo, ref)
		if err != nil {
			return err
		}
		b, err = s.blob(ctx, rp, r, path)
		if err == nil && b.Content != nil && isReadme(pathBase(path)) {
			s.rememberBlob(repo.ID, b.OID, b.Content)
		}
		return err
	})
	return b, err
}

func pathBase(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Readme returns the README of the directory at path of ref.
func (s *Service) Readme(ctx context.Context, repo Repo, ref, path string) (Blob, error) {
	if err := git.ValidateTreePath(path); err != nil {
		return Blob{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if b, ok, err := s.coldBlobRead(ctx, repo, ref, path, true); ok {
		return b, err
	}
	var b Blob
	err := s.read(ctx, repo, func(rp string) error {
		r, err := s.resolve(ctx, rp, repo, ref)
		if err != nil {
			return err
		}
		entries, truncated, oid, err := s.listTree(ctx, rp, r.Commit, path)
		if err != nil {
			return err
		}
		s.rememberListing(ctx, rp, repo.ID, r, path, oid, entries, truncated)
		name := readmeOf(entries)
		if name == "" {
			return notFoundf("no README in %q", path)
		}
		b, err = s.blob(ctx, rp, r, name)
		if err == nil && b.Content != nil {
			s.rememberBlob(repo.ID, b.OID, b.Content)
		}
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
		b.setPreviewType()
		return b, nil
	}
	_, data, err := s.cat.Object(ctx, rp, info.OID, s.o.MaxBlobSize)
	if err != nil {
		return b, fmt.Errorf("read blob: %w", err)
	}
	b.classify(data)
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
	return s.RawContent(ctx, repo, ref, path, func(b Blob, write RawWrite) error {
		w, err := start(b)
		if err != nil {
			return err
		}
		return write(w, 0, b.Size)
	})
}

// RawWrite streams the selected span of a blob while the read lease is held.
type RawWrite func(w io.Writer, offset, length int64) error

// RawContent serves blob metadata and lets the caller stream all or part of it.
func (s *Service) RawContent(ctx context.Context, repo Repo, ref, path string, serve func(Blob, RawWrite) error) error {
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
		return serve(b, func(w io.Writer, offset, length int64) error {
			if err := s.writeRaw(ctx, rp, b, w, offset, length); err != nil {
				return fmt.Errorf("%w: stream blob: %w", errStreaming, err)
			}
			return nil
		})
	})
}

func (s *Service) writeRaw(ctx context.Context, rp string, b Blob, w io.Writer, offset, length int64) error {
	args := []string{"cat-file", "blob", b.OID}
	if offset == 0 && length == b.Size {
		return git.Exec(ctx, git.Cmd{RepoPath: rp, Args: args, Stdout: w, Timeout: s.o.ArchiveTimeout})
	}
	st, err := startStream(ctx, rp, s.o.ArchiveTimeout, args...)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }() // Stop git if the selected span ends before EOF.
	if offset > 0 {
		if _, err := io.CopyN(io.Discard, st, offset); err != nil {
			return err
		}
	}
	_, err = io.CopyN(w, st, length)
	return err
}

// errStreaming marks a failure after the response has started.
var errStreaming = errors.New("streaming")

// IsStreaming reports whether err happened after the response started (the
// status line is already sent).
func IsStreaming(err error) bool { return errors.Is(err, errStreaming) }
