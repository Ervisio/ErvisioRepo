package lfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/githttp"
	"github.com/Ervisio/ErvisioRepo/internal/gitssh"
)

// ServeSSH implements gitssh.LFS: git-lfs-authenticate hands out a
// short-lived token for the HTTP API; git-lfs-transfer speaks the pure SSH
// protocol (git-lfs >= 3.0).
func (s *Server) ServeSSH(ctx context.Context, req gitssh.LFSRequest) uint32 {
	switch req.Command {
	case "git-lfs-authenticate":
		return s.authenticateSSH(ctx, req)
	case "git-lfs-transfer":
		return s.transferSSH(ctx, req)
	}
	_, _ = fmt.Fprintln(req.Stderr, "ERROR: unsupported git-lfs command")
	return 1
}

func (s *Server) authenticateSSH(ctx context.Context, req gitssh.LFSRequest) uint32 {
	tok, exp, err := s.issueToken(ctx, req.Repo.ID, req.Caller.UserID, req.Operation == opUpload)
	if err != nil {
		s.o.Log.ErrorContext(ctx, "git lfs: issue ssh token", "error", err)
		_, _ = fmt.Fprintln(req.Stderr, "ERROR: internal error")
		return 1
	}
	out := struct {
		Href      string            `json:"href"`
		Header    map[string]string `json:"header"`
		ExpiresIn int64             `json:"expires_in"`
	}{
		Href:      s.href(req.Repo, ""),
		Header:    map[string]string{"Authorization": githttp.LFSAuthScheme + " " + tok},
		ExpiresIn: int64(time.Until(exp) / time.Second),
	}
	if err := json.NewEncoder(req.Stdout).Encode(out); err != nil {
		return 1
	}
	return 0
}

// transfer is one git-lfs-transfer session.
type transfer struct {
	s      *Server
	req    gitssh.LFSRequest
	in     *pktReader
	out    *pktWriter
	upload bool
}

func (s *Server) transferSSH(ctx context.Context, req gitssh.LFSRequest) uint32 {
	t := &transfer{s: s, req: req, in: newPktReader(req.Stdin), out: &pktWriter{w: req.Stdout}, upload: req.Operation == opUpload}
	t.out.line("version=1")
	t.out.flush()
	if t.out.err != nil {
		return 1
	}
	// Version negotiation.
	lines, _, err := t.in.lines()
	if err != nil {
		return 1
	}
	if len(lines) != 1 || lines[0] != "version 1" {
		t.status(http.StatusBadRequest, "unsupported protocol version")
		return 1
	}
	t.ok()
	for t.out.err == nil {
		cmd, args, delim, err := t.command()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.o.Log.WarnContext(ctx, "git lfs transfer: read command", "repo", req.Repo.ID, "error", err)
			}
			return 1
		}
		name, arg, _ := strings.Cut(cmd, " ")
		switch name {
		case "quit":
			t.ok()
			return 0
		case "batch":
			err = t.batch(ctx, args, delim)
		case "put-object":
			err = t.putObject(ctx, arg, args, delim)
		case "verify-object":
			err = t.verifyObject(ctx, arg, args)
		case "get-object":
			err = t.getObject(ctx, arg)
		case "lock":
			err = t.lock(ctx, args)
		case "list-lock":
			err = t.listLock(ctx, args)
		case "unlock":
			err = t.unlockCmd(ctx, arg, args)
		default:
			if delim {
				if err := (&dataReader{p: t.in}).drain(); err != nil {
					return 1
				}
			}
			t.status(http.StatusBadRequest, "unknown command")
		}
		if err != nil {
			s.o.Log.WarnContext(ctx, "git lfs transfer: "+name, "repo", req.Repo.ID, "error", err)
			return 1
		}
	}
	return 1
}

// command reads a command line and its arguments.
func (t *transfer) command() (string, map[string]string, bool, error) {
	lines, delim, err := t.in.lines()
	if err != nil {
		return "", nil, false, err
	}
	if len(lines) == 0 {
		return "", nil, false, errPktFormat
	}
	args := make(map[string]string, len(lines)-1)
	for _, l := range lines[1:] {
		k, v, _ := strings.Cut(l, "=")
		args[k] = v
	}
	return lines[0], args, delim, nil
}

func (t *transfer) ok() {
	t.out.line("status 200")
	t.out.flush()
}

// status writes an error response.
func (t *transfer) status(code int, msg string) {
	t.out.line("status " + strconv.Itoa(code))
	t.out.delim()
	t.out.line(msg)
	t.out.flush()
}

// fail writes the response of an object error; only I/O errors on the
// session are returned.
func (t *transfer) fail(ctx context.Context, err error) error {
	st := status(err)
	if st == http.StatusInternalServerError {
		t.s.o.Log.ErrorContext(ctx, "git lfs transfer", "repo", t.req.Repo.ID, "error", err)
	}
	if errors.Is(err, errBadLock) {
		st = http.StatusBadRequest
	}
	t.status(st, message(err))
	return t.out.err
}

func (t *transfer) requireWrite() bool {
	if !t.upload || !canWrite(t.req.Repo, t.req.Caller) {
		t.status(http.StatusForbidden, "this session is read-only")
		return false
	}
	return true
}

func (t *transfer) batch(ctx context.Context, args map[string]string, delim bool) error {
	var lines []string
	if delim {
		var err error
		if lines, _, err = t.in.lines(); err != nil {
			return err
		}
	}
	if h := args["hash-algo"]; h != "" && h != hashSHA256 {
		t.status(http.StatusConflict, "unsupported hash algorithm")
		return t.out.err
	}
	if len(lines) > maxBatchObjects {
		t.status(http.StatusRequestEntityTooLarge, fmt.Sprintf("at most %d objects per batch", maxBatchObjects))
		return t.out.err
	}
	type obj struct {
		oid  string
		size int64
	}
	objs := make([]obj, 0, len(lines))
	oids := make([]string, 0, len(lines))
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) < 2 || !validOID(f[0]) {
			t.status(http.StatusBadRequest, "invalid object line")
			return t.out.err
		}
		n, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil || n < 0 {
			t.status(http.StatusBadRequest, "invalid object size")
			return t.out.err
		}
		objs = append(objs, obj{f[0], n})
		oids = append(oids, f[0])
	}
	have, err := t.s.has(ctx, t.req.Repo.ID, oids)
	if err != nil {
		return t.fail(ctx, err)
	}
	t.out.line("status 200")
	t.out.line("hash-algo=sha256")
	t.out.delim()
	for _, o := range objs {
		_, ok := have[o.oid]
		act := "noop"
		switch {
		case t.upload && !ok:
			act = "upload"
		case !t.upload && ok:
			act = "download"
		}
		t.out.line(fmt.Sprintf("%s %d %s", o.oid, o.size, act))
	}
	t.out.flush()
	return t.out.err
}

func (t *transfer) putObject(ctx context.Context, oid string, args map[string]string, delim bool) error {
	data := &dataReader{p: t.in}
	if !delim {
		data.done = true
	}
	size, perr := strconv.ParseInt(args["size"], 10, 64)
	var err error
	switch {
	case !validOID(oid) || perr != nil || size < 0:
		err = fmt.Errorf("%w: invalid object id or size", errBadContent)
	case !t.upload || !canWrite(t.req.Repo, t.req.Caller):
		err = fmt.Errorf("%w: this session is read-only", errForbidden)
	default:
		err = t.s.receive(ctx, t.req.Repo, oid, size, data)
	}
	if derr := data.drain(); derr != nil {
		return derr
	}
	if err != nil {
		return t.fail(ctx, err)
	}
	t.ok()
	return t.out.err
}

func (t *transfer) verifyObject(ctx context.Context, oid string, args map[string]string) error {
	size, perr := strconv.ParseInt(args["size"], 10, 64)
	if !validOID(oid) || perr != nil || size < 0 {
		t.status(http.StatusBadRequest, "invalid object id or size")
		return t.out.err
	}
	if !t.requireWrite() {
		return t.out.err
	}
	if err := t.s.verify(ctx, t.req.Repo, oid, size); err != nil {
		return t.fail(ctx, err)
	}
	t.ok()
	return t.out.err
}

func (t *transfer) getObject(ctx context.Context, oid string) error {
	if !validOID(oid) {
		t.status(http.StatusBadRequest, "invalid object id")
		return t.out.err
	}
	if !canRead(t.req.Repo, t.req.Caller) {
		t.status(http.StatusForbidden, "permission denied")
		return t.out.err
	}
	rc, size, err := t.s.open(ctx, t.req.Repo, oid, nil)
	if err != nil {
		return t.fail(ctx, err)
	}
	defer func() { _ = rc.Close() }()
	t.out.line("status 200")
	t.out.line("size=" + strconv.FormatInt(size, 10))
	t.out.delim()
	if _, err := io.Copy(t.out, rc); err != nil {
		// The stream cannot be resumed once data was sent.
		return fmt.Errorf("send object: %w", err)
	}
	t.out.flush()
	return t.out.err
}

func (t *transfer) lockArgs(l Lock) {
	t.out.line("id=" + l.ID.String())
	t.out.line("path=" + l.Path)
	t.out.line("locked-at=" + l.LockedAt.Format(time.RFC3339))
	t.out.line("ownername=" + l.OwnerName)
}

func (t *transfer) lock(ctx context.Context, args map[string]string) error {
	if !t.requireWrite() {
		return t.out.err
	}
	l, err := t.s.createLock(ctx, t.req.Repo, t.req.Caller, args["path"], args["refname"])
	switch {
	case errors.Is(err, errLockExists):
		t.out.line("status 409")
		t.lockArgs(l)
		t.out.delim()
		t.out.line("lock already exists")
		t.out.flush()
		return t.out.err
	case err != nil:
		return t.fail(ctx, err)
	}
	t.out.line("status 201")
	t.lockArgs(l)
	t.out.flush()
	return t.out.err
}

func (t *transfer) listLock(ctx context.Context, args map[string]string) error {
	if !canRead(t.req.Repo, t.req.Caller) {
		t.status(http.StatusForbidden, "permission denied")
		return t.out.err
	}
	limit, _ := strconv.Atoi(args["limit"])
	locks, next, err := t.s.listLocks(ctx, t.req.Repo, lockFilter{Path: args["path"], ID: args["id"], Cursor: args["cursor"], Limit: limit})
	if err != nil {
		return t.fail(ctx, err)
	}
	t.out.line("status 200")
	if next != "" {
		t.out.line("next-cursor=" + next)
	}
	t.out.delim()
	for _, l := range locks {
		id := l.ID.String()
		t.out.line("lock " + id)
		t.out.line("path " + id + " " + l.Path)
		t.out.line("locked-at " + id + " " + l.LockedAt.Format(time.RFC3339))
		t.out.line("ownername " + id + " " + l.OwnerName)
		if t.upload {
			who := "theirs"
			if l.OwnerID == t.req.Caller.UserID {
				who = "ours"
			}
			t.out.line("owner " + id + " " + who)
		}
	}
	t.out.flush()
	return t.out.err
}

func (t *transfer) unlockCmd(ctx context.Context, id string, args map[string]string) error {
	if !t.requireWrite() {
		return t.out.err
	}
	l, err := t.s.unlock(ctx, t.req.Repo, t.req.Caller, id, args["force"] == "true")
	if err != nil {
		return t.fail(ctx, err)
	}
	t.out.line("status 200")
	t.lockArgs(l)
	t.out.flush()
	return t.out.err
}
