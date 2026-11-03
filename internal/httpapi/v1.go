package httpapi

import (
	"context"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

// v1Server implements the strict server generated from api/openapi.yaml.
type v1Server struct {
	meta     Meta
	storage  Storage
	identity Identity
	cookies  auth.Cookies
}

var _ v1gen.StrictServerInterface = (*v1Server)(nil)

func (s *v1Server) GetMeta(context.Context, v1gen.GetMetaRequestObject) (v1gen.GetMetaResponseObject, error) {
	m := v1gen.GetMeta200JSONResponse{
		Name:         "ErvisioRepo",
		Version:      s.meta.Version,
		InstanceId:   s.meta.InstanceID,
		PublicUrl:    s.meta.PublicURL,
		Registration: v1gen.MetaRegistration(s.meta.Registration),
	}
	if s.meta.Commit != "" {
		m.Commit = &s.meta.Commit
	}
	return m, nil
}
