package tier

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// DriverFunc opens the store of one target. Implementations live in the
// driver packages; drivers.go registers them by kind.
type DriverFunc func(ctx context.Context, t config.Target, o BuildOptions) (blob.Store, error)

// WrapFunc decorates a store (encryption, retries, metrics).
type WrapFunc func(s blob.Store, t config.Target, o BuildOptions) (blob.Store, error)

// BuildOptions are shared by every driver and wrapper.
type BuildOptions struct {
	// MasterKey is required when a target has encrypt = true.
	MasterKey []byte
	// Registerer receives the metrics of the metered wrapper (nil disables them).
	Registerer prometheus.Registerer
	Log        *slog.Logger
}

var (
	registryMu sync.RWMutex
	drivers    = map[string]DriverFunc{}
	// Wrappers are applied innermost first: retry, then encryption, then
	// metering, so metrics count logical bytes and retries resend ciphertext.
	wrapRetry, wrapCrypt, wrapMetered WrapFunc
)

// RegisterDriver makes a driver available for a target kind.
func RegisterDriver(kind string, fn DriverFunc) {
	registryMu.Lock()
	defer registryMu.Unlock()
	drivers[kind] = fn
}

// RegisterWrappers sets the store wrappers; nil leaves a wrapper unset.
func RegisterWrappers(retry, crypt, metered WrapFunc) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if retry != nil {
		wrapRetry = retry
	}
	if crypt != nil {
		wrapCrypt = crypt
	}
	if metered != nil {
		wrapMetered = metered
	}
}

// ErrNoDriver is returned for a target kind without a registered driver.
var ErrNoDriver = errors.New("storage: no driver for target kind")

// BuildStore opens one target with its wrappers.
func BuildStore(ctx context.Context, t config.Target, o BuildOptions) (blob.Store, error) {
	registryMu.RLock()
	drv, retry, crypt, metered := drivers[t.Kind], wrapRetry, wrapCrypt, wrapMetered
	registryMu.RUnlock()
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if drv == nil {
		return nil, fmt.Errorf("target %s: %w %q", t.Name, ErrNoDriver, t.Kind)
	}
	s, err := drv(ctx, t, o)
	if err != nil {
		return nil, fmt.Errorf("target %s: open %s store: %w", t.Name, t.Kind, err)
	}
	wrap := func(name string, fn WrapFunc) error {
		if fn == nil {
			return nil
		}
		w, err := fn(s, t, o)
		if err != nil {
			return fmt.Errorf("target %s: %s wrapper: %w", t.Name, name, err)
		}
		s = w
		return nil
	}
	if t.Kind != "local" {
		if err := wrap("retry", retry); err != nil {
			closeStore(s)
			return nil, err
		}
	}
	if t.Encrypt {
		if crypt == nil {
			closeStore(s)
			return nil, fmt.Errorf("target %s: encryption requested but not available", t.Name)
		}
		if len(o.MasterKey) == 0 {
			closeStore(s)
			return nil, fmt.Errorf("target %s: encryption requires the master key", t.Name)
		}
		if err := wrap("crypt", crypt); err != nil {
			closeStore(s)
			return nil, err
		}
	}
	if err := wrap("metered", metered); err != nil {
		closeStore(s)
		return nil, err
	}
	return s, nil
}

// BuildStores opens every configured target. On error, the stores already
// opened are closed.
func BuildStores(ctx context.Context, cfg config.Storage, o BuildOptions) (map[string]blob.Store, error) {
	out := make(map[string]blob.Store, len(cfg.Targets))
	for _, t := range cfg.Targets {
		s, err := BuildStore(ctx, t, o)
		if err != nil {
			CloseStores(out)
			return nil, err
		}
		out[t.Name] = s
	}
	return out, nil
}

// CloseStores closes every store that holds resources (connections, handles).
func CloseStores(stores map[string]blob.Store) {
	for _, name := range slices.Sorted(maps.Keys(stores)) {
		closeStore(stores[name])
	}
}

func closeStore(s blob.Store) { _ = blob.Close(s) }
