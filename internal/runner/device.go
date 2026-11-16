package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"

	"connectrpc.com/connect"

	"github.com/Ervisio/ErvisioRepo/internal/runner/config"
	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1"
)

// DeviceOptions are the inputs of DeviceRegistration.
type DeviceOptions struct {
	URL         string
	InsecureTLS bool
	Version     string
	// Requested settings; the approver may change them.
	Name     string
	Labels   []string // label names
	Capacity int
	// Out receives the instructions for the user.
	Out io.Writer
}

// DeviceGrant is the result of an approved device registration.
type DeviceGrant struct {
	Token    string // single-use registration token
	Name     string
	Labels   []string // approved label names
	Capacity int
	Scope    string
}

// ErrDeviceDenied means the registration was denied on the server.
var ErrDeviceDenied = errors.New("the registration was denied on the server")

// DeviceRegistration asks the server for a device code, prints where to
// approve it and polls until it is approved, denied or expired.
func DeviceRegistration(ctx context.Context, o DeviceOptions) (DeviceGrant, error) {
	if err := config.CheckURL(o.URL); err != nil {
		return DeviceGrant{}, fmt.Errorf("url: %w", err)
	}
	client := NewClient(o.URL, "", o.Version, o.InsecureTLS)
	host, _ := os.Hostname()
	rctx, cancel := context.WithTimeout(ctx, time.Minute)
	resp, err := client.RequestDevice(rctx, connect.NewRequest(&runnerv1.RequestDeviceRequest{
		Hostname: host, Os: runtime.GOOS, Arch: runtime.GOARCH,
		Cpus:        int32(min(runtime.NumCPU(), 1<<16)), //nolint:gosec // bounded
		MemoryBytes: memoryBytes(), Docker: dockerPresent(), Version: o.Version,
		Name: o.Name, Labels: o.Labels, Capacity: int32(o.Capacity), //nolint:gosec // validated by the caller
	}))
	cancel()
	if err != nil {
		if connect.CodeOf(err) == connect.CodeUnimplemented || connect.CodeOf(err) == connect.CodeNotFound {
			return DeviceGrant{}, errors.New("this server does not support device registration: pass --token")
		}
		return DeviceGrant{}, fmt.Errorf("request device code: %w", err)
	}
	m := resp.Msg
	fmt.Fprintf(o.Out, "\nTo approve this runner, open\n\n    %s\n\nand enter the code\n\n    %s\n\nor open the direct link: %s\n(the code expires in %d minutes)\n\nWaiting for approval...\n",
		m.GetVerificationUri(), m.GetUserCode(), m.GetVerificationUriComplete(), m.GetExpiresIn()/60)
	interval := time.Duration(max(m.GetInterval(), 1)) * time.Second
	deadline := time.Now().Add(time.Duration(m.GetExpiresIn()) * time.Second)
	for {
		select {
		case <-ctx.Done():
			return DeviceGrant{}, fmt.Errorf("device registration: %w", ctx.Err())
		case <-time.After(interval):
		}
		if time.Now().After(deadline) {
			return DeviceGrant{}, errors.New("the code expired before it was approved; run the installer again")
		}
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		p, err := client.PollDevice(pctx, connect.NewRequest(&runnerv1.PollDeviceRequest{DeviceCode: m.GetDeviceCode()}))
		cancel()
		if err != nil {
			if connect.CodeOf(err) == connect.CodeNotFound {
				return DeviceGrant{}, errors.New("the server no longer knows this code; run the installer again")
			}
			// Transient failure: keep polling, a bit slower.
			interval += time.Second
			continue
		}
		if n := p.Msg.GetInterval(); n > 0 {
			interval = time.Duration(n) * time.Second
		}
		switch p.Msg.GetStatus() {
		case "pending", "slow_down":
			continue
		case "denied":
			return DeviceGrant{}, ErrDeviceDenied
		case "expired":
			return DeviceGrant{}, errors.New("the code expired before it was approved; run the installer again")
		case "approved":
			g := p.Msg
			if g.GetRegistrationToken() == "" {
				return DeviceGrant{}, errors.New("the server approved the runner but sent no token")
			}
			fmt.Fprintf(o.Out, "Approved for %s.\n", g.GetScope())
			return DeviceGrant{Token: g.GetRegistrationToken(), Name: g.GetName(), Labels: g.GetLabels(),
				Capacity: int(g.GetCapacity()), Scope: g.GetScope()}, nil
		default:
			return DeviceGrant{}, fmt.Errorf("unexpected device status %q", p.Msg.GetStatus())
		}
	}
}

// Deregister removes the runner of st from the server.
func Deregister(ctx context.Context, st *State, version string, insecureTLS bool) error {
	client := NewClient(st.URL, st.Credential, version, insecureTLS)
	if _, err := client.Deregister(ctx, connect.NewRequest(&runnerv1.DeregisterRequest{})); err != nil {
		if isAuthError(err) {
			return errors.New("the server does not know this runner any more")
		}
		return fmt.Errorf("deregister: %w", err)
	}
	return nil
}

// dockerPresent reports whether a Docker CLI or daemon socket is present.
func dockerPresent() bool {
	if _, err := exec.LookPath("docker"); err == nil {
		return true
	}
	_, err := os.Stat("/var/run/docker.sock")
	return err == nil
}
