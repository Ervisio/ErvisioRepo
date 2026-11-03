package gitssh

import (
	"errors"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gittransport"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
)

// maxCommandLen bounds the exec command line.
const maxCommandLen = 4096

// LFS commands.
const (
	lfsAuthenticate = "git-lfs-authenticate"
	lfsTransfer     = "git-lfs-transfer"
)

var errCommand = errors.New("unsupported command; this server only serves git")

// command is a parsed exec request.
type command struct {
	service     gittransport.Service // git commands
	lfs         string               // git-lfs commands
	operation   string               // git-lfs: "upload" or "download"
	owner, name string
}

func (c command) action() authz.Action {
	if c.lfs != "" {
		if c.operation == "upload" {
			return authz.ActPush
		}
		return authz.ActRead
	}
	return c.service.Action()
}

// parseCommand parses the command line sent by git ("git-upload-pack
// 'owner/repo.git'") or git-lfs ("git-lfs-transfer 'owner/repo.git'
// upload"). It is never handed to a shell.
func parseCommand(line string) (command, error) {
	var c command
	if len(line) > maxCommandLen {
		return c, errCommand
	}
	argv, err := splitWords(line)
	if err != nil {
		return c, err
	}
	// "git upload-pack 'path'" is the same as "git-upload-pack 'path'".
	if len(argv) >= 2 && argv[0] == "git" {
		argv = append([]string{"git-" + argv[1]}, argv[2:]...)
	}
	if len(argv) == 0 {
		return c, errCommand
	}
	switch argv[0] {
	case lfsAuthenticate, lfsTransfer:
		if len(argv) != 3 || (argv[2] != "upload" && argv[2] != "download") {
			return c, errors.New("usage: " + argv[0] + " <repo> upload|download")
		}
		c.lfs, c.operation = argv[0], argv[2]
	default:
		svc, ok := gittransport.ParseService(argv[0])
		if !ok {
			return c, errCommand
		}
		if len(argv) != 2 {
			return c, errors.New("usage: " + argv[0] + " <repo>")
		}
		c.service = svc
	}
	c.owner, c.name, err = parseRepoPath(argv[1])
	return c, err
}

// parseRepoPath accepts "owner/repo", "/owner/repo", with an optional
// ".git" and trailing slash.
func parseRepoPath(p string) (string, string, error) {
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimSuffix(p, "/")
	p = strings.TrimSuffix(p, ".git")
	owner, name, ok := strings.Cut(p, "/")
	if !ok || strings.Contains(name, "/") ||
		identity.ValidateName(owner) != nil || git.ValidateRepoName(name) != nil {
		return "", "", errors.New("invalid repository path; use <owner>/<repo>.git")
	}
	return owner, name, nil
}

// splitWords splits a command line the way git quotes it (sq_quote): words
// separated by spaces, single-quoted strings taken literally, and outside
// quotes only \' and \! escapes. Anything else that a shell would expand is
// refused.
func splitWords(line string) ([]string, error) {
	var (
		words   []string
		cur     strings.Builder
		inWord  bool
		inQuote bool
	)
	bad := errors.New("invalid characters in command")
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if inQuote {
			if ch == '\'' {
				inQuote = false
			} else {
				cur.WriteByte(ch)
			}
			continue
		}
		switch {
		case ch == ' ':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case ch == '\'':
			inQuote, inWord = true, true
		case ch == '\\':
			if i+1 >= len(line) || (line[i+1] != '\'' && line[i+1] != '!') {
				return nil, bad
			}
			i++
			cur.WriteByte(line[i])
			inWord = true
		case ch < 0x20 || ch == 0x7f || strings.IndexByte("\"`$;&|<>(){}*?[]~#\t", ch) >= 0:
			return nil, bad
		default:
			cur.WriteByte(ch)
			inWord = true
		}
	}
	if inQuote {
		return nil, errors.New("unterminated quote in command")
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}
