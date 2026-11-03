package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// Storage is the storage tier as seen by the admin endpoints (satisfied by
// *tier.Tier).
type Storage interface {
	Status(ctx context.Context) (tier.Status, error)
	CheckTarget(ctx context.Context, name string) (tier.CheckResult, error)
}

// adminPrefix is the path prefix of the admin API, relative to the router.
const adminPrefix = "/api/v1/admin/"

// adminGuard restricts the admin API to direct loopback connections. The
// Host must be a loopback name too, so that a page whose domain was rebound
// to 127.0.0.1 cannot read the API, and unsafe methods refuse requests that a
// browser marks as coming from another site (no CSRF tokens before phase 2).
//
// TODO(phase 2): replace with admin authentication, authorization and CSRF.
func adminGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, adminPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		if !directLoopback(r) || !loopbackHost(r.Host) {
			writeProblem(w, http.StatusForbidden, "the admin API is only available from localhost")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			switch r.Header.Get("Sec-Fetch-Site") {
			case "", "same-origin", "none":
			default:
				writeProblem(w, http.StatusForbidden, "cross-site requests to the admin API are not allowed")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// loopbackHost reports whether the Host header names the local host.
func loopbackHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func problem(status int, detail string) v1gen.Problem {
	p := v1gen.Problem{Type: "about:blank", Title: http.StatusText(status), Status: status}
	if detail != "" {
		p.Detail = &detail
	}
	return p
}

var errStorageUnavailable = problem(http.StatusServiceUnavailable, "storage is not configured")

func (s *v1Server) GetAdminStorage(ctx context.Context, _ v1gen.GetAdminStorageRequestObject) (v1gen.GetAdminStorageResponseObject, error) {
	if s.storage == nil {
		return v1gen.GetAdminStoragedefaultApplicationProblemPlusJSONResponse{Body: errStorageUnavailable, StatusCode: http.StatusServiceUnavailable}, nil
	}
	st, err := s.storage.Status(ctx)
	if err != nil {
		return nil, err
	}
	return v1gen.GetAdminStorage200JSONResponse(storageStatusDTO(st)), nil
}

func (s *v1Server) TestAdminStorageTarget(ctx context.Context, req v1gen.TestAdminStorageTargetRequestObject) (v1gen.TestAdminStorageTargetResponseObject, error) {
	if s.storage == nil {
		return v1gen.TestAdminStorageTargetdefaultApplicationProblemPlusJSONResponse{Body: errStorageUnavailable, StatusCode: http.StatusServiceUnavailable}, nil
	}
	res, err := s.storage.CheckTarget(ctx, req.Target)
	if errors.Is(err, tier.ErrUnknownTarget) {
		return v1gen.TestAdminStorageTarget404ApplicationProblemPlusJSONResponse{
			ProblemApplicationProblemPlusJSONResponse: v1gen.ProblemApplicationProblemPlusJSONResponse(
				problem(http.StatusNotFound, "no storage target with this name")),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.TestAdminStorageTarget200JSONResponse(checkResultDTO(res)), nil
}

func storageStatusDTO(st tier.Status) v1gen.StorageStatus {
	out := v1gen.StorageStatus{
		Targets:  make([]v1gen.StorageTarget, 0, len(st.Targets)),
		Policies: make([]v1gen.StoragePolicy, 0, len(st.Policies)),
		Caches:   make([]v1gen.StorageCache, 0, len(st.Caches)),
		Warnings: make([]v1gen.StorageWarning, 0, len(st.Warnings)),
	}
	for _, t := range st.Targets {
		roles := t.Roles
		if roles == nil {
			roles = []string{}
		}
		out.Targets = append(out.Targets, v1gen.StorageTarget{
			Name: t.Name, Kind: v1gen.StorageTargetKind(t.Kind), Encrypted: t.Encrypted, Roles: roles,
			Caps: v1gen.StorageCaps{
				Range: t.Caps.Range, Presign: t.Caps.Presign, Multipart: t.Caps.Multipart,
				AtomicRename: t.Caps.AtomicRename, ConditionalPut: t.Caps.ConditionalPut, MaxConns: t.Caps.MaxConns,
			},
		})
	}
	for _, p := range st.Policies {
		replicas := p.Replicas
		if replicas == nil {
			replicas = []string{}
		}
		dto := v1gen.StoragePolicy{
			Name: p.Name, Primary: p.Primary, Replicas: replicas,
			CacheTtlSeconds: int64(p.CacheTTL.Seconds()), CacheShare: p.CacheShare, Durability: p.Durability,
			Objects: p.Objects, Bytes: p.Bytes, PendingReplicas: p.PendingReplicas, Errors: p.Errors,
		}
		if p.Cache != "" {
			dto.Cache = &p.Cache
		}
		out.Policies = append(out.Policies, dto)
	}
	for _, c := range st.Caches {
		usedBy := c.UsedBy
		if usedBy == nil {
			usedBy = map[string]int64{}
		}
		out.Caches = append(out.Caches, v1gen.StorageCache{
			Target: c.Target, Budget: c.Budget, Used: c.Used, Entries: c.Entries,
			HighWatermark: c.HighWatermark, LowWatermark: c.LowWatermark, UsedBy: usedBy,
		})
	}
	for _, w := range st.Warnings {
		dto := v1gen.StorageWarning{Code: v1gen.StorageWarningCode(w.Code), Message: w.Message}
		if w.Policy != "" {
			dto.Policy = &w.Policy
		}
		out.Warnings = append(out.Warnings, dto)
	}
	return out
}

func checkResultDTO(r tier.CheckResult) v1gen.StorageCheckResult {
	out := v1gen.StorageCheckResult{
		Target: r.Target, Ok: r.OK, DurationMs: r.Duration.Milliseconds(),
		Steps: make([]v1gen.StorageCheckStep, 0, len(r.Steps)),
	}
	for _, s := range r.Steps {
		dto := v1gen.StorageCheckStep{Name: v1gen.StorageCheckStepName(s.Name), Ok: s.OK, DurationMs: s.Duration.Milliseconds()}
		if s.Error != "" {
			dto.Error = &s.Error
		}
		out.Steps = append(out.Steps, dto)
	}
	return out
}
