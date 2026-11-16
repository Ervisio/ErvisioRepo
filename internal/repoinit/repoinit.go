// Package repoinit writes the first commit of a new repository: a README
// and, optionally, a .gitignore and a license from the templates embedded
// here (the auto_init, gitignore_template and license_template options of
// repository creation).
package repoinit

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

//go:embed gitignore/*.gitignore licenses/*.txt
var templates embed.FS

// License is a license template.
type License struct {
	Key         string // lower case SPDX id, as GitHub
	Name        string
	SPDXID      string
	Description string
	// Featured licenses are listed first by GitHub.
	Featured       bool
	Permissions    []string
	Conditions     []string
	Limitations    []string
	Implementation string
	file           string
}

var licenses = []License{
	{Key: "mit", Name: "MIT License", SPDXID: "MIT", Featured: true, file: "mit.txt",
		Description:    "A short and simple permissive license with conditions only requiring preservation of copyright and license notices.",
		Implementation: "Create a text file (typically named LICENSE or LICENSE.txt) in the root of your source code and copy the text of the license into the file. Replace [year] with the current year and [fullname] with the name (or names) of the copyright holders.",
		Permissions:    []string{"commercial-use", "modifications", "distribution", "private-use"},
		Conditions:     []string{"include-copyright"}, Limitations: []string{"liability", "warranty"}},
	{Key: "apache-2.0", Name: "Apache License 2.0", SPDXID: "Apache-2.0", Featured: true, file: "apache-2.0.txt",
		Description:    "A permissive license whose main conditions require preservation of copyright and license notices. Contributors provide an express grant of patent rights.",
		Implementation: "Create a text file (typically named LICENSE or LICENSE.txt) in the root of your source code and copy the text of the license into the file.",
		Permissions:    []string{"commercial-use", "modifications", "distribution", "patent-use", "private-use"},
		Conditions:     []string{"include-copyright", "document-changes"}, Limitations: []string{"trademark-use", "liability", "warranty"}},
	{Key: "gpl-3.0", Name: "GNU General Public License v3.0", SPDXID: "GPL-3.0", Featured: true, file: "gpl-3.0.txt",
		Description:    "Permissions of this strong copyleft license are conditioned on making available complete source code of licensed works and modifications under the same license.",
		Implementation: "Create a text file (typically named COPYING) in the root of your source code and copy the text of the license into the file.",
		Permissions:    []string{"commercial-use", "modifications", "distribution", "patent-use", "private-use"},
		Conditions:     []string{"include-copyright", "document-changes", "disclose-source", "same-license"}, Limitations: []string{"liability", "warranty"}},
	{Key: "bsd-3-clause", Name: `BSD 3-Clause "New" or "Revised" License`, SPDXID: "BSD-3-Clause", file: "bsd-3-clause.txt",
		Description:    "A permissive license similar to the BSD 2-Clause License, but with a 3rd clause that prohibits others from using the name of the copyright holder or its contributors to promote derived products without written consent.",
		Implementation: "Create a text file (typically named LICENSE or LICENSE.txt) in the root of your source code and copy the text of the license into the file. Replace [year] with the current year and [fullname] with the name (or names) of the copyright holders.",
		Permissions:    []string{"commercial-use", "modifications", "distribution", "private-use"},
		Conditions:     []string{"include-copyright"}, Limitations: []string{"liability", "warranty"}},
	{Key: "mpl-2.0", Name: "Mozilla Public License 2.0", SPDXID: "MPL-2.0", file: "mpl-2.0.txt",
		Description:    "Permissions of this weak copyleft license are conditioned on making available source code of licensed files and modifications of those files under the same license.",
		Implementation: "Create a text file (typically named LICENSE or LICENSE.txt) in the root of your source code and copy the text of the license into the file.",
		Permissions:    []string{"commercial-use", "modifications", "distribution", "patent-use", "private-use"},
		Conditions:     []string{"disclose-source", "include-copyright", "same-license--file"}, Limitations: []string{"liability", "trademark-use", "warranty"}},
	{Key: "unlicense", Name: "The Unlicense", SPDXID: "Unlicense", file: "unlicense.txt",
		Description:    "A license with no conditions whatsoever which dedicates works to the public domain.",
		Implementation: "Create a text file (typically named UNLICENSE or LICENSE.txt) in the root of your source code and copy the text of the license disclaimer into the file.",
		Permissions:    []string{"private-use", "commercial-use", "modifications", "distribution"},
		Conditions:     []string{}, Limitations: []string{"liability", "warranty"}},
}

// Licenses lists the license templates.
func Licenses() []License { return slices.Clone(licenses) }

// LicenseByKey finds a license template by key or SPDX id
// (case-insensitive).
func LicenseByKey(key string) (License, bool) {
	for _, l := range licenses {
		if strings.EqualFold(l.Key, key) || strings.EqualFold(l.SPDXID, key) {
			return l, true
		}
	}
	return License{}, false
}

// Body is the license text with the [year] and [fullname] placeholders.
func (l License) Body() string {
	b, err := templates.ReadFile("licenses/" + l.file)
	if err != nil {
		return ""
	}
	return string(b)
}

// Gitignores lists the names of the .gitignore templates, sorted.
func Gitignores() []string {
	entries, err := templates.ReadDir("gitignore")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(e.Name(), ".gitignore"))
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return out
}

// Gitignore returns a .gitignore template by name (case-insensitive) with
// its canonical name.
func Gitignore(name string) (string, string, bool) {
	for _, n := range Gitignores() {
		if strings.EqualFold(n, name) {
			b, err := templates.ReadFile("gitignore/" + n + ".gitignore")
			return n, string(b), err == nil
		}
	}
	return "", "", false
}

// ErrUnknownTemplate is wrapped by Options.Check for a template that does
// not exist.
var ErrUnknownTemplate = errors.New("unknown template")

// Options is what the first commit holds.
type Options struct {
	Readme    bool
	Gitignore string // template name, "" = none
	License   string // template key, "" = none
	// Name, Description and Holder fill the README and the license.
	Name, Description, Holder string
}

// Wanted reports whether a first commit is asked for. A template alone
// implies a README too, as on GitHub.
func (o Options) Wanted() bool { return o.Readme || o.Gitignore != "" || o.License != "" }

// Check validates the template names before the repository is created.
func (o Options) Check() error {
	if o.Gitignore != "" {
		if _, _, ok := Gitignore(o.Gitignore); !ok {
			return fmt.Errorf("%w: gitignore template %q", ErrUnknownTemplate, o.Gitignore)
		}
	}
	if o.License != "" {
		if _, ok := LicenseByKey(o.License); !ok {
			return fmt.Errorf("%w: license template %q", ErrUnknownTemplate, o.License)
		}
	}
	return nil
}

// files builds the files of the first commit, README first.
func (o Options) files(now time.Time) ([]gitbrowse.NewFile, error) {
	readme := "# " + o.Name + "\n"
	if d := strings.TrimSpace(o.Description); d != "" {
		readme += "\n" + d + "\n"
	}
	out := []gitbrowse.NewFile{{Path: "README.md", Content: []byte(readme)}}
	if o.Gitignore != "" {
		_, body, ok := Gitignore(o.Gitignore)
		if !ok {
			return nil, fmt.Errorf("%w: gitignore template %q", ErrUnknownTemplate, o.Gitignore)
		}
		out = append(out, gitbrowse.NewFile{Path: ".gitignore", Content: []byte(body)})
	}
	if o.License != "" {
		l, ok := LicenseByKey(o.License)
		if !ok {
			return nil, fmt.Errorf("%w: license template %q", ErrUnknownTemplate, o.License)
		}
		body := strings.NewReplacer("[year]", strconv.Itoa(now.Year()), "[fullname]", o.Holder).Replace(l.Body())
		out = append(out, gitbrowse.NewFile{Path: "LICENSE", Content: []byte(body)})
	}
	return out, nil
}

// Commit writes the first commit of the empty repository repo on its
// default branch, authored by author.
func Commit(ctx context.Context, b *gitbrowse.Service, repo gitbrowse.Repo, author uuid.UUID, o Options) error {
	files, err := o.files(time.Now())
	if err != nil {
		return err
	}
	_, err = b.Edit(ctx, repo, gitbrowse.Edit{Op: gitbrowse.OpCreate, Path: files[0].Path, Content: files[0].Content,
		Extra: files[1:], Message: "Initial commit", AuthorID: author})
	if err != nil {
		return fmt.Errorf("initial commit: %w", err)
	}
	return nil
}

// Repos creates and reads repositories.
type Repos interface {
	Create(ctx context.Context, who auth.Principal, in repos.CreateInput, c identity.Client) (repos.Repo, error)
	Get(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error)
}

// Create creates a repository and writes its first commit when o asks for
// one. An unknown template is a validation error found before the
// repository exists; a failed first commit leaves the repository empty and
// is only logged.
func Create(ctx context.Context, rs Repos, b *gitbrowse.Service, who auth.Principal, in repos.CreateInput, o Options,
	c identity.Client, log *slog.Logger) (repos.Repo, error) {
	if err := o.Check(); err != nil {
		return repos.Repo{}, &repos.ValidationError{Msg: err.Error()}
	}
	r, err := rs.Create(ctx, who, in, c)
	if err != nil || !o.Wanted() || b == nil {
		return r, err
	}
	o.Name, o.Description, o.Holder = r.Name, r.Description, r.Owner
	repo := gitbrowse.Repo{ID: r.ID, OwnerID: r.OwnerID, Name: r.Name, DefaultBranch: r.DefaultBranch}
	if err := Commit(ctx, b, repo, who.UserID, o); err != nil {
		log.WarnContext(ctx, "first commit of a new repository failed", "repo", r.ID, "error", err)
		return r, nil
	}
	if fresh, err := rs.Get(ctx, who, r.Owner, r.Name); err == nil {
		r = fresh
	}
	return r, nil
}
