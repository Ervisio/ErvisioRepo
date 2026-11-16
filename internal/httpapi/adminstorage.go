package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/storage/storagecfg"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// StorageConfig is the storage configuration service behind the admin
// storage endpoints (satisfied by *storagecfg.Service).
type StorageConfig interface {
	Config(ctx context.Context) (storagecfg.View, error)
	PutTarget(ctx context.Context, actor uuid.UUID, ip, name string, in storagecfg.TargetInput) (storagecfg.View, error)
	DeleteTarget(ctx context.Context, actor uuid.UUID, ip, name string) (storagecfg.View, error)
	PutPolicy(ctx context.Context, actor uuid.UUID, ip, policy string, in storagecfg.PolicyInput) (storagecfg.View, error)
	Test(ctx context.Context, in storagecfg.TargetInput) (tier.CheckResult, error)
	ScanHostKeys(ctx context.Context, host string, port int) ([]tier.HostKey, error)
}

// maxStorageBody bounds the JSON bodies of the storage configuration
// endpoints, which carry keys and known_hosts text.
const maxStorageBody = 256 << 10

// storageBodyLimit caps the request body of the storage configuration
// endpoints; an oversized body fails to decode and is answered 400.
func storageBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, adminPrefix+"storage/") && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxStorageBody)
		}
		next.ServeHTTP(w, r)
	})
}

var errStorageCfgUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "storage configuration is not available"}

// noMasterKey is the answer when credentials cannot be sealed.
var noMasterKey = problemResponse{status: http.StatusConflict,
	detail: "credentials cannot be stored: no master key is available (security.master_key_file)"}

func (s *v1Server) GetAdminStorageConfig(ctx context.Context, _ v1gen.GetAdminStorageConfigRequestObject) (v1gen.GetAdminStorageConfigResponseObject, error) {
	if s.storageCfg == nil {
		return errStorageCfgUnavailable, nil
	}
	v, err := s.storageCfg.Config(ctx)
	if err != nil {
		return nil, err
	}
	return v1gen.GetAdminStorageConfig200JSONResponse(storageConfigDTO(v)), nil
}

func (s *v1Server) PutAdminStorageTarget(ctx context.Context, req v1gen.PutAdminStorageTargetRequestObject) (v1gen.PutAdminStorageTargetResponseObject, error) {
	if s.storageCfg == nil {
		return errStorageCfgUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("a target definition is required"), nil
	}
	v, err := s.storageCfg.PutTarget(ctx, principal(ctx).UserID, clientOf(ctx).IP, req.Name, targetInput(*req.Body))
	var verr *storagecfg.ValidationError
	switch {
	case errors.As(err, &verr):
		return v1gen.PutAdminStorageTarget422ApplicationProblemPlusJSONResponse(storageProblem(verr)), nil
	case errors.Is(err, storagecfg.ErrNoMasterKey):
		return noMasterKey, nil
	case err != nil:
		return nil, err
	}
	return v1gen.PutAdminStorageTarget200JSONResponse(storageConfigDTO(v)), nil
}

func (s *v1Server) DeleteAdminStorageTarget(ctx context.Context, req v1gen.DeleteAdminStorageTargetRequestObject) (v1gen.DeleteAdminStorageTargetResponseObject, error) {
	if s.storageCfg == nil {
		return errStorageCfgUnavailable, nil
	}
	v, err := s.storageCfg.DeleteTarget(ctx, principal(ctx).UserID, clientOf(ctx).IP, req.Name)
	var verr *storagecfg.ValidationError
	switch {
	case errors.As(err, &verr):
		return v1gen.DeleteAdminStorageTarget422ApplicationProblemPlusJSONResponse(storageProblem(verr)), nil
	case errors.Is(err, storagecfg.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: "no storage target with this name was created in the web admin"}, nil
	case err != nil:
		return nil, err
	}
	return v1gen.DeleteAdminStorageTarget200JSONResponse(storageConfigDTO(v)), nil
}

func (s *v1Server) PutAdminStoragePolicy(ctx context.Context, req v1gen.PutAdminStoragePolicyRequestObject) (v1gen.PutAdminStoragePolicyResponseObject, error) {
	if s.storageCfg == nil {
		return errStorageCfgUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("a policy definition is required"), nil
	}
	in := storagecfg.PolicyInput{Primary: req.Body.Primary, Replicas: req.Body.Replicas}
	if req.Body.Cache != nil {
		in.Cache = *req.Body.Cache
	}
	v, err := s.storageCfg.PutPolicy(ctx, principal(ctx).UserID, clientOf(ctx).IP, req.Policy, in)
	var verr *storagecfg.ValidationError
	switch {
	case errors.As(err, &verr):
		return v1gen.PutAdminStoragePolicy422ApplicationProblemPlusJSONResponse(storageProblem(verr)), nil
	case errors.Is(err, storagecfg.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: "no storage policy for this data type in config.toml"}, nil
	case err != nil:
		return nil, err
	}
	return v1gen.PutAdminStoragePolicy200JSONResponse(storageConfigDTO(v)), nil
}

func (s *v1Server) TestAdminStorageDraft(ctx context.Context, req v1gen.TestAdminStorageDraftRequestObject) (v1gen.TestAdminStorageDraftResponseObject, error) {
	if s.storageCfg == nil {
		return errStorageCfgUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("a target definition is required"), nil
	}
	res, err := s.storageCfg.Test(ctx, targetInput(*req.Body))
	var verr *storagecfg.ValidationError
	switch {
	case errors.As(err, &verr):
		return v1gen.TestAdminStorageDraft422ApplicationProblemPlusJSONResponse(storageProblem(verr)), nil
	case errors.Is(err, storagecfg.ErrNoMasterKey):
		return noMasterKey, nil
	case err != nil:
		return nil, err
	}
	return v1gen.TestAdminStorageDraft200JSONResponse(checkResultDTO(res)), nil
}

// Visit methods let a problemResponse answer the storage configuration
// operations.
func (p problemResponse) VisitGetAdminStorageConfigResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitPutAdminStorageTargetResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDeleteAdminStorageTargetResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitPutAdminStoragePolicyResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitTestAdminStorageDraftResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func targetInput(b v1gen.StorageTargetInput) storagecfg.TargetInput {
	in := storagecfg.TargetInput{
		Name: b.Name, Kind: string(b.Kind), Encrypt: deref(b.Encrypt),
		Endpoint: deref(b.Endpoint), Bucket: deref(b.Bucket), Region: deref(b.Region), PathStyle: deref(b.PathStyle), Prefix: deref(b.Prefix),
		Host: deref(b.Host), Port: deref(b.Port), User: deref(b.User), Root: deref(b.Root), MaxConns: deref(b.MaxConns),
	}
	if sec := b.Secrets; sec != nil {
		in.Secrets = storagecfg.Secrets{
			AccessKey: deref(sec.AccessKey), SecretKey: deref(sec.SecretKey), PrivateKey: deref(sec.PrivateKey),
			Passphrase: deref(sec.Passphrase), KnownHosts: deref(sec.KnownHosts), Password: deref(sec.Password),
		}
	}
	return in
}

func storageProblem(verr *storagecfg.ValidationError) v1gen.ValidationProblem {
	detail := verr.Error()
	out := v1gen.ValidationProblem{
		Type: "about:blank", Title: http.StatusText(http.StatusUnprocessableEntity), Status: http.StatusUnprocessableEntity,
		Detail: &detail, Errors: make([]v1gen.FieldProblem, 0, len(verr.Errors)),
	}
	for _, e := range verr.Errors {
		out.Errors = append(out.Errors, v1gen.FieldProblem{Path: e.Path, Msg: e.Msg})
	}
	return out
}

func optInt(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

func storageConfigDTO(v storagecfg.View) v1gen.StorageConfig {
	out := v1gen.StorageConfig{
		Targets:          make([]v1gen.StorageTargetConfig, 0, len(v.Targets)),
		Policies:         make([]v1gen.StoragePolicyConfig, 0, len(v.Policies)),
		SecretsAvailable: v.SecretsAvailable, RestartRequired: v.RestartRequired, Fallback: v.Fallback,
		FallbackCause: optString(v.FallbackCause),
	}
	for _, t := range v.Targets {
		out.Targets = append(out.Targets, v1gen.StorageTargetConfig{
			Name: t.Name, Kind: v1gen.StorageTargetConfigKind(t.Kind), Source: v1gen.StorageTargetConfigSource(t.Source),
			Encrypt: t.Encrypt, Path: optString(t.Path),
			Endpoint: optString(t.Endpoint), Bucket: optString(t.Bucket), Region: optString(t.Region), Prefix: optString(t.Prefix),
			PathStyle: t.PathStyle,
			Host:      optString(t.Host), Port: optInt(t.Port), User: optString(t.User), Root: optString(t.Root), MaxConns: optInt(t.MaxConns),
			HasAccessKey: t.HasAccessKey, HasSecretKey: t.HasSecretKey, HasPrivateKey: t.HasPrivateKey,
			HasPassphrase: t.HasPassphrase, HasKnownHosts: t.HasKnownHosts, HasPassword: t.HasPassword,
			UsedBy: t.UsedBy, RestartPending: t.RestartPending, RemovedAfterRestart: t.RemovedAfterRestart,
			Objects: t.Objects, Locked: t.Locked,
		})
	}
	for _, p := range v.Policies {
		out.Policies = append(out.Policies, v1gen.StoragePolicyConfig{
			Name: p.Name, Source: v1gen.StoragePolicyConfigSource(p.Source),
			Primary: p.Primary, RunningPrimary: p.RunningPrimary, Replicas: p.Replicas, Cache: optString(p.Cache),
			FilePrimary: p.FilePrimary, FileReplicas: p.FileReplicas, FileCache: optString(p.FileCache),
			Objects: p.Objects, PrimaryLocked: p.PrimaryLocked, RestartPending: p.RestartPending,
		})
	}
	return out
}

func (p problemResponse) VisitScanAdminStorageHostKeysResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (s *v1Server) ScanAdminStorageHostKeys(ctx context.Context, req v1gen.ScanAdminStorageHostKeysRequestObject) (v1gen.ScanAdminStorageHostKeysResponseObject, error) {
	if s.storageCfg == nil {
		return errStorageCfgUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("a host is required"), nil
	}
	keys, err := s.storageCfg.ScanHostKeys(ctx, req.Body.Host, deref(req.Body.Port))
	var verr *storagecfg.ValidationError
	switch {
	case errors.As(err, &verr):
		return v1gen.ScanAdminStorageHostKeys422ApplicationProblemPlusJSONResponse(storageProblem(verr)), nil
	case err != nil:
		return nil, err
	}
	out := v1gen.ScanAdminStorageHostKeys200JSONResponse{Keys: make([]struct {
		Fingerprint string `json:"fingerprint"`
		Type        string `json:"type"`
	}, 0, len(keys))}
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		out.Keys = append(out.Keys, struct {
			Fingerprint string `json:"fingerprint"`
			Type        string `json:"type"`
		}{Fingerprint: k.Fingerprint, Type: k.Type})
		lines = append(lines, k.Line)
	}
	out.KnownHosts = strings.Join(lines, "\n") + "\n"
	return out, nil
}
