package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/caddy"
)

// caddyCmd publishes the server behind Caddy (PLAN §9.4):
//
//	ervisio-repo caddy setup  --host git.example.com [--upstream 127.0.0.1:3400]
//	                          [--container-upstream ervisiorepo-app:3400]
//	                          [--network ervisiorepo_edge] [--instance <name>] [--dry-run]
//	ervisio-repo caddy status [--instance <name>]
//	ervisio-repo caddy remove [--instance <name>] [--dry-run]
func caddyCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || (args[0] != "setup" && args[0] != "status" && args[0] != "remove") {
		return fmt.Errorf("%w: caddy needs \"setup\", \"status\" or \"remove\"", errUsage)
	}
	sub := args[0]
	fs := flag.NewFlagSet("caddy "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	instance := fs.String("instance", "", "Caddy to use: \"system\" or a container name (default: first running, supported one)")
	var host, upstream, cupstream, network *string
	var dry *bool
	if sub == "setup" {
		host = fs.String("host", "", "public host name, e.g. git.example.com")
		upstream = fs.String("upstream", "127.0.0.1:3400", "address of the server as seen by Caddy on the host")
		cupstream = fs.String("container-upstream", "ervisiorepo-app:3400", "address of the server as seen by Caddy in a container")
		network = fs.String("network", "ervisiorepo_edge", "Docker network joined by a Caddy container")
	}
	if sub != "status" {
		dry = fs.Bool("dry-run", false, "show what would change without writing anything")
	}
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: unexpected arguments %v", errUsage, fs.Args())
	}
	if sub == "setup" && *host == "" {
		return fmt.Errorf("%w: caddy setup needs --host", errUsage)
	}

	det := caddy.Detect(ctx)
	if det.DockerError != "" {
		fmt.Fprintf(stderr, "warning: containers not checked: %s\n", det.DockerError)
	}
	inst := det.Pick(*instance)
	if inst == nil {
		if *instance != "" {
			return fmt.Errorf("no Caddy named %q", *instance)
		}
		if sub == "setup" {
			printSnippet(stdout, *host, *upstream)
		}
		return caddy.ErrNoCaddy
	}

	switch sub {
	case "status":
		return caddyStatus(ctx, det, inst, *instance != "", stdout)
	case "remove":
		res := caddy.Remove(ctx, inst, caddy.ApplyOptions{DryRun: *dry})
		return reportCaddy(stdout, inst, res)
	}
	up, err := parseUpstream(*upstream, *cupstream, *network)
	if err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	res := caddy.Apply(ctx, inst, *host, up, caddy.ApplyOptions{DryRun: *dry})
	if err := reportCaddy(stdout, inst, res); err != nil {
		return err
	}
	if res.OK && !*dry {
		fmt.Fprintf(stdout, "Next: set server.public_url = \"https://%s\" in the configuration and restart ervisio-repo.\n", res.Host)
	}
	return nil
}

func printSnippet(w io.Writer, host, upstream string) {
	h, ok := caddy.NormalizeHost(host)
	if !ok {
		return
	}
	if block, err := caddy.GenerateBlock(h, upstream, false); err == nil {
		fmt.Fprintf(w, "No Caddy found. Add this to your Caddyfile:\n\n%s\n", block)
	}
}

func parseUpstream(hostPort, containerPort, network string) (caddy.Upstream, error) {
	h, p, err := net.SplitHostPort(hostPort)
	if err != nil {
		return caddy.Upstream{}, fmt.Errorf("--upstream: %w", err)
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535 {
		return caddy.Upstream{}, fmt.Errorf("--upstream: invalid port %q", p)
	}
	ch, cp, err := net.SplitHostPort(containerPort)
	if err != nil {
		return caddy.Upstream{}, fmt.Errorf("--container-upstream: %w", err)
	}
	cport, err := strconv.Atoi(cp)
	if err != nil || cport < 1 || cport > 65535 {
		return caddy.Upstream{}, fmt.Errorf("--container-upstream: invalid port %q", cp)
	}
	return caddy.Upstream{Host: h, Port: port, Container: ch, ContainerPort: cport, Network: network}, nil
}

func describe(i *caddy.Instance) string {
	state := "stopped"
	if i.Running {
		state = "running"
	}
	s := fmt.Sprintf("%s (%s, %s) %s layout=%s", i.Key, i.Kind, state, i.ConfigPath, i.Layout)
	if i.Unsupported != "" {
		s += " UNSUPPORTED: " + i.Unsupported
	}
	return s
}

func caddyStatus(ctx context.Context, det *caddy.Detection, picked *caddy.Instance, one bool, stdout io.Writer) error {
	list := det.Instances
	if one {
		list = []*caddy.Instance{picked}
	}
	for _, i := range list {
		fmt.Fprintln(stdout, describe(i))
		cur, err := caddy.CurrentBlock(ctx, i)
		switch {
		case err != nil:
			fmt.Fprintf(stdout, "  cannot read: %v\n", err)
		case cur == nil:
			fmt.Fprintln(stdout, "  no ErvisioRepo site")
		default:
			fmt.Fprintf(stdout, "  site %s -> %s (%s in %s", cur.Host, cur.Target, cur.Layout, cur.Path)
			if cur.Layout == "file" && !cur.Imported {
				fmt.Fprint(stdout, ", NOT imported by the Caddyfile")
			}
			fmt.Fprintln(stdout, ")")
		}
	}
	return nil
}

func reportCaddy(w io.Writer, inst *caddy.Instance, res caddy.Result) error {
	for _, wn := range res.Warnings {
		fmt.Fprintf(w, "warning: %s\n", wn)
	}
	if res.OK {
		msg := res.Message
		if msg == "" {
			msg = "done"
		}
		fmt.Fprintf(w, "%s: %s\n", inst.Key, msg)
		for _, p := range res.Planned {
			fmt.Fprintf(w, "  would change %s\n", p)
		}
		if len(res.Planned) > 0 && res.Snippet != "" {
			fmt.Fprintf(w, "\n%s", res.Snippet)
		}
		return nil
	}
	if res.Snippet != "" && res.Step == caddy.StepUnsupported {
		fmt.Fprintf(w, "Paste this by hand:\n\n%s\n", res.Snippet)
	}
	msg := res.Message
	if res.RolledBack {
		msg += " (previous files restored)"
	}
	return errors.New(strings.TrimSpace("caddy " + res.Step + ": " + msg))
}
