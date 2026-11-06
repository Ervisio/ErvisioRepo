package httpapi

import (
	"context"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/auth/mfa"
	"github.com/Ervisio/ErvisioRepo/internal/credentials"
	"github.com/Ervisio/ErvisioRepo/internal/dashboard"
	"github.com/Ervisio/ErvisioRepo/internal/ghsync"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/notify"
	"github.com/Ervisio/ErvisioRepo/internal/sso"
	"github.com/Ervisio/ErvisioRepo/internal/webhooks"
)

// v1Server implements the strict server generated from api/openapi.yaml.
type v1Server struct {
	meta     Meta
	storage  Storage
	identity Identity
	cookies  auth.Cookies
	mfa      *mfa.Service
	orgs     Orgs
	repos    Repos
	audit    audit.Lister
	creds    *credentials.Service
	sso      *sso.Manager
	tiering  Tiering
	releases Releases
	browse   *gitbrowse.Service
	// protection serves the branch protection and tag rule endpoints.
	protection Protection
	// github serves the GitHub connection and sync endpoints; nil answers 503.
	github *ghsync.Service
	// issues serves the issue, label and milestone endpoints; nil answers 503.
	issues Issues
	// webhooks serves the repository and organization hook endpoints; nil answers 503.
	webhooks *webhooks.Service
	// notify serves the notification and subscription endpoints; nil answers 503.
	notify *notify.Service

	// pulls serves the pull request, review and fork endpoints; nil answers 503.
	pulls Pulls
	// statuses serves the commit status and checks endpoints; nil answers 503.
	statuses Statuses
	// actions serves the Actions endpoints; nil answers 503.
	actions Actions
	// dashboard serves the home feeds and preferences; nil answers 503.
	dashboard *dashboard.Service
	// settings serves /admin/settings; nil answers 503.
	settings InstanceSettings
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
	if s.meta.DefaultLayout != "" {
		l := v1gen.MetaDefaultLayout(s.meta.DefaultLayout)
		m.DefaultLayout = &l
	}
	return m, nil
}
