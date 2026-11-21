package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/storage/storagecfg"
)

func (s *v1Server) ResyncAdminStoragePolicy(ctx context.Context, req v1gen.ResyncAdminStoragePolicyRequestObject) (v1gen.ResyncAdminStoragePolicyResponseObject, error) {
	if s.storageCfg == nil {
		return errStorageCfgUnavailable, nil
	}
	n, err := s.storageCfg.Resync(ctx, principal(ctx).UserID, clientOf(ctx).IP, req.Policy)
	switch {
	case errors.Is(err, storagecfg.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: "no storage policy for this data type"}, nil
	case errors.Is(err, storagecfg.ErrNoReplicas):
		return problemResponse{status: http.StatusConflict, detail: "this data type has no replica to copy to"}, nil
	case err != nil:
		return nil, err
	}
	return v1gen.ResyncAdminStoragePolicy202JSONResponse{Objects: n}, nil
}

func (s *v1Server) VerifyAdminStorage(ctx context.Context, _ v1gen.VerifyAdminStorageRequestObject) (v1gen.VerifyAdminStorageResponseObject, error) {
	if s.storageCfg == nil {
		return errStorageCfgUnavailable, nil
	}
	n, err := s.storageCfg.VerifyAll(ctx, principal(ctx).UserID, clientOf(ctx).IP)
	if err != nil {
		return nil, err
	}
	return v1gen.VerifyAdminStorage202JSONResponse{Objects: n}, nil
}

func (p problemResponse) VisitResyncAdminStoragePolicyResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitVerifyAdminStorageResponse(w http.ResponseWriter) error {
	return p.write(w)
}
