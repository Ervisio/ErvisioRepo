package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

type fakeStorage struct {
	statusErr error
	checked   []string
}

func (f *fakeStorage) Status(context.Context) (tier.Status, error) {
	return tier.Status{
		Targets: []tier.TargetStatus{{Name: "disk", Kind: "local", Roles: []string{"primary:lfs"}, Caps: blob.Caps{Range: true}}},
		Policies: []tier.PolicyStatus{{Name: "lfs", Primary: "disk", Cache: "disk", CacheTTL: time.Hour,
			Objects: 2, Bytes: 10, PendingReplicas: 1}},
		Caches:   []tier.CacheStats{{Target: "disk", Budget: 100, Used: 10, Entries: 2}},
		Warnings: tier.Warnings(configLocalOnly),
	}, f.statusErr
}

func (f *fakeStorage) CheckTarget(_ context.Context, name string) (tier.CheckResult, error) {
	f.checked = append(f.checked, name)
	if name != "disk" {
		return tier.CheckResult{}, fmt.Errorf("%w %q", tier.ErrUnknownTarget, name)
	}
	return tier.CheckResult{Target: name, OK: false, Duration: 1500 * time.Millisecond, Steps: []tier.CheckStep{
		{Name: "put", OK: false, Error: "permission denied"}, {Name: "delete", OK: true},
	}}, nil
}

func fromLoopback(r *http.Request) { r.RemoteAddr = "127.0.0.1:4000"; r.Host = "127.0.0.1:3400" }

func TestAdminStorageStatus(t *testing.T) {
	fs := &fakeStorage{}
	h := newTestRouter(t, func(d *Deps) { d.Storage = fs })
	rec := do(h, http.MethodGet, "/api/v1/admin/storage", fromLoopback)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var st v1gen.StorageStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Targets) != 1 || !st.Targets[0].Caps.Range || st.Policies[0].CacheTtlSeconds != 3600 ||
		*st.Policies[0].Cache != "disk" || st.Policies[0].Replicas == nil || st.Caches[0].UsedBy == nil {
		t.Fatalf("status = %+v", st)
	}
	if len(st.Warnings) != 1 || st.Warnings[0].Code != v1gen.StorageWarningCode(tier.WarnLocalOnly) {
		t.Fatalf("local-only warning missing: %+v", st.Warnings)
	}

	fs.statusErr = errors.New("db down")
	assertProblem(t, do(h, http.MethodGet, "/api/v1/admin/storage", fromLoopback), http.StatusInternalServerError)
}

func TestAdminStorageTest(t *testing.T) {
	fs := &fakeStorage{}
	h := newTestRouter(t, func(d *Deps) { d.Storage = fs })
	rec := do(h, http.MethodPost, "/api/v1/admin/storage/disk/test", fromLoopback)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var res v1gen.StorageCheckResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Ok || res.DurationMs != 1500 || len(res.Steps) != 2 || *res.Steps[0].Error != "permission denied" || res.Steps[1].Error != nil {
		t.Fatalf("result = %+v", res)
	}
	assertProblem(t, do(h, http.MethodPost, "/api/v1/admin/storage/nope/test", fromLoopback), http.StatusNotFound)
	assertProblem(t, do(h, http.MethodGet, "/api/v1/admin/storage/disk/test", fromLoopback), http.StatusMethodNotAllowed)
}

func TestAdminStorageUnconfigured(t *testing.T) {
	h := newTestRouter(t, nil)
	assertProblem(t, do(h, http.MethodGet, "/api/v1/admin/storage", fromLoopback), http.StatusServiceUnavailable)
	assertProblem(t, do(h, http.MethodPost, "/api/v1/admin/storage/disk/test", fromLoopback), http.StatusServiceUnavailable)
}
