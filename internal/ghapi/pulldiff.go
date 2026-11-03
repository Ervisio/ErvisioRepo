package ghapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/pulls"
)

// maxPatchCommits bounds the commits of a pull request served as a patch
// series (Accept: application/vnd.github.patch).
const maxPatchCommits = 250

// errTooLarge is the GitHub answer to a diff or patch over the limits.
var errTooLarge = errf(http.StatusNotAcceptable, "Sorry, the diff exceeded the maximum number of files or lines")

// pullMedia returns "diff" or "patch" when the Accept header asks for the
// pull request in that format, "" for JSON.
func (c *call) pullMedia() string {
	a := c.r.Header.Get("Accept")
	switch {
	case strings.Contains(a, ".diff"):
		return "diff"
	case strings.Contains(a, ".patch"):
		return "patch"
	}
	return ""
}

func (c *call) writeText(body string) error {
	h := c.w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	c.w.WriteHeader(http.StatusOK)
	_, err := c.w.Write([]byte(body))
	if err != nil {
		return fmt.Errorf("write diff: %w", err)
	}
	return nil
}

// pullDiff answers the diff of pull request n from its merge base.
func (c *call) pullDiff(n int64) error {
	d, err := c.s.d.Pulls.Diff(c.ctx, c.who, c.owner, c.repo, n, nil)
	if err != nil {
		return err
	}
	if d.Truncated {
		return errTooLarge
	}
	var b strings.Builder
	for _, f := range d.Files {
		writeFileDiff(&b, f)
	}
	return c.writeText(b.String())
}

// pullPatch answers the commits of pull request p as a format-patch
// series (merge commits are left out, as git format-patch does).
func (c *call) pullPatch(p pulls.Pull) error {
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	list, more, err := c.s.d.Pulls.ListCommits(c.ctx, c.who, c.owner, c.repo, p.Issue.Number, 0, maxPatchCommits)
	if err != nil {
		return err
	}
	if more {
		return errTooLarge
	}
	var commits []gitbrowse.Commit
	for _, cm := range list {
		if len(cm.Parents) <= 1 {
			commits = append(commits, cm)
		}
	}
	var b strings.Builder
	repo := browseRepo(r)
	for k, cm := range commits {
		d, err := c.s.d.Browse.Commit(c.ctx, repo, cm.SHA)
		if err != nil {
			return err
		}
		if d.Diff.Truncated {
			return errTooLarge
		}
		subject := "[PATCH] "
		if len(commits) > 1 {
			subject = "[PATCH " + strconv.Itoa(k+1) + "/" + strconv.Itoa(len(commits)) + "] "
		}
		b.WriteString("From " + cm.SHA + " Mon Sep 17 00:00:00 2001\n")
		b.WriteString("From: " + cm.Author.Name + " <" + cm.Author.Email + ">\n")
		b.WriteString("Date: " + cm.Author.Date.Format("Mon, 2 Jan 2006 15:04:05 -0700") + "\n")
		b.WriteString("Subject: " + subject + cm.Summary + "\n\n")
		if body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(cm.Message), cm.Summary)); body != "" {
			b.WriteString(body + "\n")
		}
		b.WriteString("---\n")
		for _, f := range d.Diff.Files {
			writeFileDiff(&b, f)
		}
		b.WriteString("\n")
	}
	return c.writeText(b.String())
}

// filePatch is the hunks of f in unified format, as in the "patch" field
// of the GitHub file objects; nil for a binary or truncated file.
func filePatch(f gitbrowse.FileDiff) *string {
	if f.Binary || f.Truncated || len(f.Hunks) == 0 {
		return nil
	}
	var b strings.Builder
	for _, h := range f.Hunks {
		writeHunk(&b, h)
	}
	s := strings.TrimSuffix(b.String(), "\n")
	return &s
}

// writeFileDiff writes f as a git diff.
func writeFileDiff(b *strings.Builder, f gitbrowse.FileDiff) {
	oldPath, newPath := f.OldPath, f.NewPath
	if oldPath == "" {
		oldPath = newPath
	}
	if newPath == "" {
		newPath = oldPath
	}
	b.WriteString("diff --git a/" + oldPath + " b/" + newPath + "\n")
	switch f.Status {
	case gitbrowse.StatusAdded:
		b.WriteString("new file mode " + f.NewMode + "\n")
	case gitbrowse.StatusDeleted:
		b.WriteString("deleted file mode " + f.OldMode + "\n")
	default:
		if f.OldMode != "" && f.NewMode != "" && f.OldMode != f.NewMode {
			b.WriteString("old mode " + f.OldMode + "\nnew mode " + f.NewMode + "\n")
		}
	}
	if f.Status == gitbrowse.StatusRenamed || f.Status == gitbrowse.StatusCopied {
		verb := "rename"
		if f.Status == gitbrowse.StatusCopied {
			verb = "copy"
		}
		b.WriteString("similarity index " + strconv.Itoa(f.Similarity) + "%\n")
		b.WriteString(verb + " from " + f.OldPath + "\n" + verb + " to " + f.NewPath + "\n")
	}
	oldOID, newOID := orZero(f.OldOID), orZero(f.NewOID)
	if oldOID != newOID {
		line := "index " + oldOID + ".." + newOID
		if f.Status != gitbrowse.StatusAdded && f.Status != gitbrowse.StatusDeleted && f.OldMode == f.NewMode && f.NewMode != "" {
			line += " " + f.NewMode
		}
		b.WriteString(line + "\n")
	}
	from, to := "a/"+oldPath, "b/"+newPath
	if f.Status == gitbrowse.StatusAdded {
		from = "/dev/null"
	}
	if f.Status == gitbrowse.StatusDeleted {
		to = "/dev/null"
	}
	if f.Binary {
		b.WriteString("Binary files " + from + " and " + to + " differ\n")
		return
	}
	if len(f.Hunks) == 0 {
		return
	}
	b.WriteString("--- " + from + "\n+++ " + to + "\n")
	for _, h := range f.Hunks {
		writeHunk(b, h)
	}
}

func writeHunk(b *strings.Builder, h gitbrowse.Hunk) {
	b.WriteString(h.Header + "\n")
	for _, l := range h.Lines {
		switch l.Type {
		case gitbrowse.LineAdd:
			b.WriteString("+" + l.Text + "\n")
		case gitbrowse.LineDelete:
			b.WriteString("-" + l.Text + "\n")
		case gitbrowse.LineNoNewline:
			b.WriteString(l.Text + "\n")
		default:
			b.WriteString(" " + l.Text + "\n")
		}
	}
}

// fileName is the path a file change is reported under.
func fileName(oldPath, newPath string) string {
	if newPath != "" {
		return newPath
	}
	return oldPath
}

func orZero(oid string) string {
	if oid == "" {
		return strings.Repeat("0", 40)
	}
	return oid
}

// ---- update branch ----

type updateBranchBody struct {
	ExpectedHeadSHA string `json:"expected_head_sha"`
}

// updatePullBranch merges the base branch into the head branch
// (PUT /repos/{owner}/{repo}/pulls/{number}/update-branch).
func (c *call) updatePullBranch() error {
	if err := c.write(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	var b updateBranchBody
	if c.r.ContentLength != 0 {
		if err := c.decode(&b); err != nil {
			return err
		}
	}
	_, err = c.s.d.Pulls.UpdateBranch(c.ctx, c.who, c.owner, c.repo, n,
		pulls.UpdateBranchInput{Method: pulls.MethodMerge, ExpectedHeadSHA: b.ExpectedHeadSHA})
	if errors.Is(err, pulls.ErrConflict) {
		return errf(http.StatusUnprocessableEntity, "%s", strings.TrimPrefix(err.Error(), pulls.ErrConflict.Error()+": "))
	}
	if err != nil {
		return err
	}
	return c.ok(http.StatusAccepted, map[string]string{"message": "Updating pull request branch.",
		"url": c.repoAPI() + "/pulls/" + strconv.FormatInt(n, 10)})
}
