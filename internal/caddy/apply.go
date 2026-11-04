package caddy

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Steps of a change, reported in Result.Step ("done" on success).
const (
	StepHost        = "host"
	StepUnsupported = "unsupported"
	StepRead        = "read"
	StepConflict    = "conflict"
	StepNetwork     = "network"
	StepWrite       = "write"
	StepValidate    = "validate"
	StepReload      = "reload"
	StepDone        = "done"
)

// Result is the outcome of Apply or Remove.
type Result struct {
	OK      bool
	Step    string
	Message string
	// Snippet is what to paste by hand when the config cannot or may not be edited.
	Snippet string
	Host    string
	Target  string
	// Reason is set for StepUnsupported (an Unsupported* value, "unsafe" or "not-ours").
	Reason string
	// RolledBack: the files were written back as they were.
	RolledBack bool
	// NotRunning: Caddy was stopped, the change is checked and waits for its next start.
	NotRunning bool
	// Planned lists the files a dry run would change.
	Planned  []string
	Warnings []string
}

var unsupportedText = map[string]string{
	UnsupportedJSON:     "this Caddy runs from a JSON config, not a Caddyfile: add the site yourself",
	UnsupportedPath:     "the Caddyfile is outside /etc/caddy: add the site yourself",
	UnsupportedLabels:   "this Caddy is caddy-docker-proxy and reads sites from container labels: add these labels to the ErvisioRepo container",
	UnsupportedStopped:  "the Caddy container is not running",
	UnsupportedReadonly: "the container's Caddyfile is mounted read-only: add the site yourself",
}

// Current is our managed block on an instance.
type Current struct {
	Host   string
	Target string
	Layout string
	// Path holds the block: the file, or the Caddyfile for an inline block.
	Path string
	// Imported (file layout): the main Caddyfile loads the file.
	Imported bool
}

// readable reports whether the Caddyfile of an instance can be read.
func readable(i *Instance) bool {
	return i.Unsupported != UnsupportedJSON && i.Unsupported != UnsupportedStopped && i.Unsupported != UnsupportedLabels
}

// inventory lists the hosts every site of the Caddyfile and its imports serve,
// and our own block if any (left out of the hosts).
func inventory(ctx context.Context, inst *Instance, b Backend, main string) ([]string, *Current, error) {
	o, err := Scan(main)
	if err != nil {
		return nil, nil, err
	}
	hosts := SiteHosts(o)
	var own *Current
	if inline, err := ReadInline(main); err == nil && inline != "" {
		if m := ParseManaged(inline); m != nil {
			own = &Current{m.Host, m.Target, "inline", inst.ConfigPath, true}
			for i, h := range hosts {
				if h == m.Host {
					hosts = append(hosts[:i], hosts[i+1:]...)
					break
				}
			}
		}
	}
	imported := map[string]bool{}
	if len(o.Imports) > 0 {
		all, _ := b.Files(ctx)
		for _, f := range all {
			if f != inst.ConfigPath && len(ImportsCovering(o, inst.ConfigPath, f)) > 0 {
				imported[f] = true
			}
		}
	}
	ownFile := inst.FilePath()
	if text, ok, _ := b.Read(ctx, ownFile); ok && own == nil {
		if m := ParseManaged(text); m != nil {
			own = &Current{m.Host, m.Target, "file", ownFile, imported[ownFile]}
		}
	}
	for f := range imported {
		if f == ownFile {
			continue
		}
		text, ok, _ := b.Read(ctx, f)
		if !ok {
			continue
		}
		if so, err := Scan(text); err == nil {
			hosts = append(hosts, SiteHosts(so)...)
		}
	}
	return hosts, own, nil
}

// CurrentBlock returns our managed block on the instance, or nil.
func CurrentBlock(ctx context.Context, inst *Instance) (*Current, error) {
	if !readable(inst) {
		return nil, nil
	}
	b := BackendFor(inst)
	main, ok, err := b.Read(ctx, inst.ConfigPath)
	if err != nil || !ok {
		return nil, err
	}
	_, own, err := inventory(ctx, inst, b, main)
	return own, err
}

type files map[string]*string

func str(s string) *string { return &s }

func writeAll(ctx context.Context, b Backend, order []string, next files) error {
	for _, p := range order {
		var err error
		if next[p] == nil {
			err = b.Remove(ctx, p)
		} else {
			err = b.Write(ctx, p, *next[p])
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// restoreAll writes before back in reverse order and returns the paths it
// could not restore.
func restoreAll(ctx context.Context, b Backend, order []string, before files) []string {
	var failed []string
	for i := len(order) - 1; i >= 0; i-- {
		p := order[i]
		var err error
		if before[p] == nil {
			err = b.Remove(ctx, p)
		} else {
			err = b.Write(ctx, p, *before[p])
		}
		if err != nil {
			failed = append(failed, p)
		}
	}
	return failed
}

func same(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// commit writes in order, validates and reloads, rolling back on failure.
func commit(ctx context.Context, inst *Instance, b Backend, order []string, before, next files, dryRun bool, base Result) Result {
	var changed []string
	for _, p := range order {
		if !same(before[p], next[p]) {
			changed = append(changed, p)
		}
	}
	if len(changed) == 0 {
		base.OK, base.Step, base.Message = true, StepDone, "nothing to change"
		return base
	}
	if dryRun {
		base.OK, base.Step, base.Message, base.Planned = true, StepDone, "dry run: nothing was written", changed
		return base
	}
	rollback := func() bool {
		failed := restoreAll(ctx, b, changed, before)
		if len(failed) > 0 {
			base.Warnings = append(base.Warnings, "could not write back: "+strings.Join(failed, ", "))
		}
		return len(failed) == 0
	}
	if err := writeAll(ctx, b, changed, next); err != nil {
		base.Step, base.Message, base.RolledBack = StepWrite, err.Error(), rollback()
		return base
	}
	if v := b.Validate(ctx); !v.OK {
		msg := CleanOutput(v.Output)
		if msg == "" {
			msg = "caddy validate failed"
		}
		base.Step, base.Message, base.RolledBack = StepValidate, msg, rollback()
		return base
	}
	if !inst.Running {
		base.OK, base.Step, base.NotRunning = true, StepDone, true
		base.Message = "checked; Caddy is stopped and loads it when it starts"
		return base
	}
	if r := b.Reload(ctx); !r.OK {
		msg := CleanOutput(r.Output)
		if msg == "" {
			msg = "caddy did not reload"
		}
		base.Step, base.Message, base.RolledBack = StepReload, msg, rollback()
		if again := b.Reload(ctx); !again.OK {
			base.Warnings = append(base.Warnings, "Caddy did not reload the previous config either: "+CleanOutput(again.Output))
		}
		return base
	}
	base.OK, base.Step = true, StepDone
	return base
}

// ApplyOptions tunes Apply.
type ApplyOptions struct {
	DryRun bool
}

// Apply serves the server at hostInput through inst. Every change is checked
// with `caddy validate` and reloaded; on failure the files are restored.
func Apply(ctx context.Context, inst *Instance, hostInput string, up Upstream, opts ApplyOptions) Result {
	host, ok := NormalizeHost(hostInput)
	if !ok {
		return Result{Step: StepHost, Message: fmt.Sprintf("%q is not a valid host name (like git.example.com)", hostInput)}
	}
	target := ProxyTarget(inst.ViaContainer(), up)
	snippet, err := GenerateBlock(host, target, false)
	if err != nil {
		return Result{Step: StepHost, Message: err.Error(), Host: host}
	}
	base := Result{Host: host, Target: target, Snippet: snippet}
	if inst.Unsupported != "" {
		base.Step, base.Reason, base.Message = StepUnsupported, inst.Unsupported, unsupportedText[inst.Unsupported]
		if inst.Unsupported == UnsupportedLabels {
			base.Snippet = DockerProxyLabels(host, up)
		}
		return base
	}
	b := BackendFor(inst)
	main, found, err := b.Read(ctx, inst.ConfigPath)
	if err != nil {
		base.Step, base.Message = StepRead, err.Error()
		return base
	}
	if !found {
		base.Step, base.Message = StepRead, inst.ConfigPath+" does not exist"
		return base
	}
	if blocker := EditBlocker(main); blocker != "" {
		base.Step, base.Reason, base.Message = StepUnsupported, "unsafe", blocker+". Add the site yourself"
		return base
	}
	hosts, _, err := inventory(ctx, inst, b, main)
	if err != nil {
		base.Step, base.Message = StepRead, err.Error()
		return base
	}
	if HostTaken(hosts, host) {
		base.Step, base.Message = StepConflict, "Caddy already serves "+host+" in another site"
		return base
	}
	if inst.ViaContainer() && !contains(inst.Networks, up.Network) && !opts.DryRun {
		if err := ConnectNetwork(ctx, up.Network, inst.ContainerID); err != nil {
			base.Step, base.Message = StepNetwork, err.Error()
			return base
		}
	}
	if inst.Ephemeral {
		base.Warnings = append(base.Warnings, "the Caddyfile of this container is not on a volume: the change lasts until the container is recreated")
	}

	var before, next files
	var order []string
	if inst.Layout == "file" {
		file := inst.FilePath()
		old, has, err := b.Read(ctx, file)
		if err != nil {
			base.Step, base.Message = StepRead, err.Error()
			return base
		}
		var oldPtr *string
		if has {
			oldPtr = str(old)
			if first, _, _ := strings.Cut(old, "\n"); strings.TrimSpace(first) != Marker {
				base.Step, base.Reason, base.Message = StepConflict, "not-ours", file+" exists and was not written by ErvisioRepo"
				return base
			}
		}
		// An inline block left from another layout would define the site twice.
		cleaned, err := RemoveInline(main)
		if err == nil {
			cleaned, err = EnsureImport(cleaned, inst.ConfigPath, file)
		}
		if err != nil {
			base.Step, base.Reason, base.Message = StepUnsupported, "unsafe", err.Error()+". Add the site yourself"
			return base
		}
		before = files{file: oldPtr, inst.ConfigPath: str(main)}
		next = files{file: str(snippet), inst.ConfigPath: str(cleaned)}
		order = []string{file, inst.ConfigPath} // the file first, so the import never points at nothing
	} else {
		block, _ := GenerateBlock(host, target, true)
		text, err := UpsertInline(main, block)
		if err != nil {
			base.Step, base.Reason, base.Message = StepUnsupported, "unsafe", err.Error()+". Add the site yourself"
			return base
		}
		before = files{inst.ConfigPath: str(main)}
		next = files{inst.ConfigPath: str(text)}
		order = []string{inst.ConfigPath}
	}
	return commit(ctx, inst, b, order, before, next, opts.DryRun, base)
}

// Remove removes our site (the import line and our file, or the inline block),
// checks and reloads, with rollback.
func Remove(ctx context.Context, inst *Instance, opts ApplyOptions) Result {
	base := Result{}
	if inst.Unsupported != "" {
		base.Step, base.Reason, base.Message = StepUnsupported, inst.Unsupported, unsupportedText[inst.Unsupported]
		return base
	}
	b := BackendFor(inst)
	main, found, err := b.Read(ctx, inst.ConfigPath)
	if err != nil {
		base.Step, base.Message = StepRead, err.Error()
		return base
	}
	if !found {
		base.Step, base.Message = StepRead, inst.ConfigPath+" does not exist"
		return base
	}
	if _, err := Scan(main); err != nil {
		base.Step, base.Reason, base.Message = StepUnsupported, "unsafe", err.Error()
		return base
	}
	file := inst.FilePath()
	old, has, err := b.Read(ctx, file)
	if err != nil {
		base.Step, base.Message = StepRead, err.Error()
		return base
	}
	ours := false
	if has {
		first, _, _ := strings.Cut(old, "\n")
		ours = strings.TrimSpace(first) == Marker
	}
	text, err := RemoveInline(main)
	if err == nil && ours {
		text, err = RemoveImport(text, inst.ConfigPath, file)
	}
	if err != nil {
		base.Step, base.Reason, base.Message = StepUnsupported, "unsafe", err.Error()
		return base
	}
	before := files{inst.ConfigPath: str(main)}
	next := files{inst.ConfigPath: str(text)}
	order := []string{inst.ConfigPath}
	if ours {
		before[file], next[file] = str(old), nil
		order = append(order, file) // the import goes first, then the file
		if m := ParseManaged(old); m != nil {
			base.Host = m.Host
		}
	} else if inline, _ := ReadInline(main); inline != "" {
		if m := ParseManaged(inline); m != nil {
			base.Host = m.Host
		}
	}
	if !ours && same(before[inst.ConfigPath], next[inst.ConfigPath]) {
		base.OK, base.Step, base.Message = true, StepDone, "no ErvisioRepo site found"
		return base
	}
	return commit(ctx, inst, b, order, before, next, opts.DryRun, base)
}

// ErrNoCaddy is returned when no Caddy instance was found.
var ErrNoCaddy = errors.New("no Caddy found (neither caddy.service nor a caddy container)")
