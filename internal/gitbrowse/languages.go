package gitbrowse

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"slices"
	"strconv"
	"strings"
)

const (
	// languageEntryCap bounds how many tree entries are looked at.
	languageEntryCap = 200000
	// languageOtherBelow folds languages under 0.1% of the counted bytes.
	languageOtherBelow = 0.001
	languageOtherName  = "Other"
	languageOtherColor = "#8b949e"
)

// Language is the share of one language in a tree.
type Language struct {
	Name  string
	Bytes int64
	// Percent is 0 to 100 with one decimal.
	Percent float64
	// Color is #rrggbb.
	Color string
}

// Languages is the language breakdown of a ref, most bytes first.
type Languages struct {
	Ref        Resolved
	TotalBytes int64
	Items      []Language
}

// Languages sizes the files of the tree of ref by language. Like GitHub it only
// counts programming and markup languages, decided from the file name alone.
func (s *Service) Languages(ctx context.Context, repo Repo, ref string) (Languages, error) {
	var res Languages
	err := s.read(ctx, repo, func(rp string) error {
		r, err := s.resolve(ctx, rp, repo, ref)
		if err != nil {
			return err
		}
		res.Ref = r
		sizes, err := languageSizes(ctx, rp, r.Commit)
		if err != nil {
			return err
		}
		res.TotalBytes, res.Items = breakdown(sizes)
		return nil
	})
	if errors.Is(err, ErrEmpty) {
		return Languages{}, nil
	}
	if err != nil {
		return Languages{}, err
	}
	return res, nil
}

// languageSizes sums the blob sizes of the tree of commit per language.
func languageSizes(ctx context.Context, rp, commit string) (map[*languageDef]int64, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	st, err := startStream(ctx, rp, readTimeout, "ls-tree", "-r", "-l", "-z", commit)
	if err != nil {
		return nil, fmt.Errorf("list tree: %w", err)
	}
	sizes := map[*languageDef]int64{}
	br := bufio.NewReader(st)
	truncated := false
	var perr error
	for n := 0; ; n++ {
		rec, err := br.ReadString(0)
		if len(rec) > 0 {
			if n >= languageEntryCap {
				truncated = true
				cancel()
				break
			}
			if def, size := classify(strings.TrimSuffix(rec, "\x00")); def != nil {
				sizes[def] += size
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				perr = err
			}
			break
		}
	}
	werr := st.Close()
	if perr != nil && !truncated {
		return nil, fmt.Errorf("read tree: %w", perr)
	}
	if werr != nil && !truncated && ctx.Err() == nil {
		return nil, fmt.Errorf("list tree: %w", werr)
	}
	return sizes, nil
}

// classify returns the language and size of one `ls-tree -r -l -z` record
// ("<mode> <type> <oid> <size>\t<path>"), or nil when it does not count.
func classify(rec string) (*languageDef, int64) {
	meta, p, ok := strings.Cut(rec, "\t")
	if !ok {
		return nil, 0
	}
	f := strings.Fields(meta)
	if len(f) != 4 || f[1] != "blob" || f[0] == "120000" {
		return nil, 0
	}
	size, err := strconv.ParseInt(f[3], 10, 64)
	if err != nil || size <= 0 {
		return nil, 0
	}
	return languageOf(p), size
}

// breakdown orders the languages by size and folds the small ones into Other.
func breakdown(sizes map[*languageDef]int64) (int64, []Language) {
	var total int64
	for _, n := range sizes {
		total += n
	}
	if total == 0 {
		return 0, []Language{}
	}
	items := make([]Language, 0, len(sizes)+1)
	var other int64
	for def, n := range sizes {
		if float64(n)/float64(total) < languageOtherBelow {
			other += n
			continue
		}
		items = append(items, Language{Name: def.name, Bytes: n, Color: def.color})
	}
	slices.SortFunc(items, func(a, b Language) int {
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), strings.Compare(a.Name, b.Name))
	})
	if other > 0 {
		items = append(items, Language{Name: languageOtherName, Bytes: other, Color: languageOtherColor})
	}
	for i := range items {
		items[i].Percent = math.Round(float64(items[i].Bytes)/float64(total)*1000) / 10
	}
	return total, items
}

// languageOf returns the language of a repository path, or nil when the file is
// not counted: prose, data, vendored or generated.
func languageOf(p string) *languageDef {
	dir, base := path.Split(p)
	for seg := range strings.SplitSeq(dir, "/") {
		if _, skip := skippedDirs[seg]; skip {
			return nil
		}
	}
	lower := strings.ToLower(base)
	for _, suffix := range skippedSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return nil
		}
	}
	if def, ok := languageByName[lower]; ok {
		return def
	}
	return languageByExt[path.Ext(lower)]
}

type languageDef struct {
	name, color string
	exts        []string
	files       []string // lower-case file names
}

var (
	skippedDirs = map[string]struct{}{
		"vendor": {}, "node_modules": {}, "third_party": {}, "dist": {}, "build": {}, ".git": {},
	}
	skippedSuffixes = []string{".min.js", ".min.css", ".map", ".gen.go", ".pb.go", "_pb2.py", ".d.ts"}

	// Colours are the ones of GitHub linguist.
	languageDefs = []*languageDef{
		{name: "Go", color: "#00ADD8", exts: []string{".go"}},
		{name: "TypeScript", color: "#3178c6", exts: []string{".ts", ".tsx", ".mts", ".cts"}},
		{name: "JavaScript", color: "#f1e05a", exts: []string{".js", ".jsx", ".mjs", ".cjs"}},
		{name: "Python", color: "#3572A5", exts: []string{".py", ".pyw"}},
		{name: "Rust", color: "#dea584", exts: []string{".rs"}},
		{name: "C", color: "#555555", exts: []string{".c", ".h"}},
		{name: "C++", color: "#f34b7d", exts: []string{".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx", ".ipp"}},
		{name: "C#", color: "#178600", exts: []string{".cs"}},
		{name: "Java", color: "#b07219", exts: []string{".java"}},
		{name: "Kotlin", color: "#A97BFF", exts: []string{".kt", ".kts"}},
		{name: "Swift", color: "#F05138", exts: []string{".swift"}},
		{name: "Ruby", color: "#701516", exts: []string{".rb", ".rake", ".gemspec"}},
		{name: "PHP", color: "#4F5D95", exts: []string{".php"}},
		{name: "Lua", color: "#000080", exts: []string{".lua"}},
		{name: "Shell", color: "#89e051", exts: []string{".sh", ".bash", ".zsh"}},
		{name: "PowerShell", color: "#012456", exts: []string{".ps1", ".psm1", ".psd1"}},
		{name: "HTML", color: "#e34c26", exts: []string{".html", ".htm"}},
		{name: "CSS", color: "#663399", exts: []string{".css"}},
		{name: "SCSS", color: "#c6538c", exts: []string{".scss"}},
		{name: "Vue", color: "#41b883", exts: []string{".vue"}},
		{name: "Svelte", color: "#ff3e00", exts: []string{".svelte"}},
		{name: "Dart", color: "#00B4AB", exts: []string{".dart"}},
		{name: "Scala", color: "#c22d40", exts: []string{".scala", ".sc"}},
		{name: "Elixir", color: "#6e4a7e", exts: []string{".ex", ".exs"}},
		{name: "Haskell", color: "#5e5086", exts: []string{".hs"}},
		{name: "Zig", color: "#ec915c", exts: []string{".zig"}},
		{name: "SQL", color: "#e38c00", exts: []string{".sql"}},
		{name: "Dockerfile", color: "#384d54", exts: []string{".dockerfile"}, files: []string{"dockerfile"}},
		{name: "Makefile", color: "#427819", exts: []string{".mk"}, files: []string{"makefile", "gnumakefile"}},
		{name: "HCL", color: "#844FBA", exts: []string{".tf", ".hcl", ".tfvars"}},
		{name: "Protocol Buffer", color: "#4d6c8c", exts: []string{".proto"}},
		{name: "GDScript", color: "#355570", exts: []string{".gd"}},
		{name: "Batchfile", color: "#C1F12E", exts: []string{".bat", ".cmd"}},
		{name: "Nix", color: "#7e7eff", exts: []string{".nix"}},
		{name: "Perl", color: "#0298c3", exts: []string{".pl", ".pm"}},
		{name: "R", color: "#198CE7", exts: []string{".r"}},
		{name: "Objective-C", color: "#438eff", exts: []string{".m", ".mm"}},
		{name: "Assembly", color: "#6E4C13", exts: []string{".asm", ".s"}},
		{name: "GLSL", color: "#5686a5", exts: []string{".glsl", ".vert", ".frag", ".geom"}},
		{name: "HLSL", color: "#aace60", exts: []string{".hlsl", ".hlsli"}},
	}

	languageByExt  = map[string]*languageDef{}
	languageByName = map[string]*languageDef{}
)

func init() {
	for _, d := range languageDefs {
		for _, e := range d.exts {
			languageByExt[e] = d
		}
		for _, f := range d.files {
			languageByName[f] = d
		}
	}
}
