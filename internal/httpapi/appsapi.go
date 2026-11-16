package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/apps"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/webhooks"
)

// Apps in the style of GitHub Apps: developer settings, installation flow
// and installed Apps (APPS-PLAN A2).

var errAppsUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "apps are not available"}

func (p problemResponse) VisitGetAppCatalogResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitListMyAppsResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitCreateMyAppResponse(w http.ResponseWriter) error   { return p.write(w) }
func (p problemResponse) VisitListOrgAppsResponse(w http.ResponseWriter) error   { return p.write(w) }
func (p problemResponse) VisitCreateOrgAppResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitGetAppResponse(w http.ResponseWriter) error        { return p.write(w) }
func (p problemResponse) VisitUpdateAppResponse(w http.ResponseWriter) error     { return p.write(w) }
func (p problemResponse) VisitDeleteAppResponse(w http.ResponseWriter) error     { return p.write(w) }
func (p problemResponse) VisitCreateAppKeyResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitDeleteAppKeyResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitInstallAppResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitListAppDeliveriesResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetAppDeliveryResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitListAppInstallationsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListAppInstallTargetsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitRedeliverAppDeliveryResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListMyAppInstallationsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListOrgAppInstallationsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetAppInstallationResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitUpdateAppInstallationResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDeleteAppInstallationResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitSuspendAppInstallationResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitUnsuspendAppInstallationResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitAcceptAppInstallationPermissionsResponse(w http.ResponseWriter) error {
	return p.write(w)
}

// appsProblem maps the errors of the App operations.
func appsProblem(err error) (problemResponse, bool) {
	var ie *apps.InputError
	var ve *webhooks.ValidationError
	switch {
	case err == nil:
		return problemResponse{}, false
	case errors.As(err, &ie):
		return problemResponse{status: http.StatusUnprocessableEntity, detail: ie.Msg}, true
	case errors.As(err, &ve):
		return problemResponse{status: http.StatusUnprocessableEntity, detail: ve.Error()}, true
	case errors.Is(err, apps.ErrNotFound), errors.Is(err, webhooks.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: "not found"}, true
	case errors.Is(err, apps.ErrForbidden):
		return problemResponse{status: http.StatusForbidden, detail: "you are not allowed to do this"}, true
	case errors.Is(err, apps.ErrConflict):
		return problemResponse{status: http.StatusConflict, detail: "the App is already installed on this account"}, true
	case errors.Is(err, apps.ErrTooMany):
		return problemResponse{status: http.StatusConflict, detail: "the limit is reached"}, true
	}
	return problemResponse{}, false
}

// appsCall returns the caller of a session operation, or the problem.
func (s *v1Server) appsCall(ctx context.Context) (problemResponse, bool) {
	if s.apps == nil {
		return errAppsUnavailable, false
	}
	if p, ok := caller(ctx); !ok || p.Automated() {
		return errAuthRequired, false
	}
	return problemResponse{}, true
}

func appPerms(p authz.AppPermissions) v1gen.AppPermissions {
	out := v1gen.AppPermissions{}
	for k, v := range p {
		out[k] = v
	}
	return out
}

func appDTO(a apps.App) v1gen.App {
	out := v1gen.App{Id: a.NumID, Slug: a.Slug, Name: a.Name, Description: a.Description, HomepageUrl: a.HomepageURL,
		LogoUrl: a.LogoURL, Public: a.Public, Owner: v1gen.OAuthAppOwner{Kind: v1gen.OAuthAppOwnerKind(a.OwnerKind),
			Name: a.OwnerName}, BotLogin: a.BotLogin, ClientId: a.ClientID, Permissions: appPerms(a.Permissions),
		Events: nonNil(a.Events), Installations: a.Installations, Manageable: a.Manageable, CreatedAt: a.CreatedAt,
		UpdatedAt: a.UpdatedAt}
	if !a.Manageable {
		return out
	}
	setup, cbs := a.SetupURL, nonNil(a.CallbackURLs)
	out.SetupUrl, out.CallbackUrls = &setup, &cbs
	if a.Webhook != nil {
		h := hookDTO(*a.Webhook)
		out.Webhook = &v1gen.AppWebhookConfig{Url: h.Url, Active: h.Active, HasSecret: h.HasSecret, LastDelivery: h.LastDelivery}
	}
	if a.Keys != nil {
		keys := make([]v1gen.AppKey, 0, len(a.Keys))
		for _, k := range a.Keys {
			keys = append(keys, appKeyDTO(k))
		}
		out.Keys = &keys
	}
	if a.ClientSecrets != nil {
		secrets := make([]v1gen.OAuthAppSecret, 0, len(a.ClientSecrets))
		for _, c := range a.ClientSecrets {
			secrets = append(secrets, v1gen.OAuthAppSecret{Id: c.ID, Hint: c.Hint, CreatedBy: optString(c.CreatedBy),
				CreatedAt: c.CreatedAt, LastUsedAt: c.LastUsedAt})
		}
		out.ClientSecrets = &secrets
	}
	return out
}

func appKeyDTO(k apps.Key) v1gen.AppKey {
	return v1gen.AppKey{Id: k.ID, Fingerprint: k.Fingerprint, CreatedBy: optString(k.CreatedBy), CreatedAt: k.CreatedAt,
		LastUsedAt: k.LastUsedAt}
}

func appsDTO(list []apps.App) []v1gen.App {
	out := make([]v1gen.App, 0, len(list))
	for _, a := range list {
		out = append(out, appDTO(a))
	}
	return out
}

func installationDTO(i apps.Installation, repos []apps.RepoRef) v1gen.AppInstallation {
	out := v1gen.AppInstallation{Id: i.NumID, App: v1gen.AppInstallationApp{Id: i.AppNumID, Slug: i.AppSlug, Name: i.AppName,
		BotLogin: i.AppSlug + "[bot]"}, RepositorySelection: v1gen.AppInstallationRepositorySelection(i.RepositorySelection),
		Permissions: appPerms(i.Effective()), Events: nonNil(i.Events), Pending: i.Pending(), SuspendedAt: i.SuspendedAt,
		CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt}
	out.Account.Kind, out.Account.Name = v1gen.AppInstallationAccountKind(i.AccountKind), i.AccountName
	if out.Pending {
		rp, re := appPerms(i.AppPermissions), nonNil(i.AppEvents)
		out.RequestedPermissions, out.RequestedEvents = &rp, &re
	}
	if repos != nil {
		list := make([]v1gen.AppInstallationRepo, 0, len(repos))
		for _, r := range repos {
			list = append(list, v1gen.AppInstallationRepo{Owner: r.Owner, Name: r.Name, Visibility: r.Visibility})
		}
		out.Repositories = &list
	}
	return out
}

func installationsDTO(list []apps.Installation) []v1gen.AppInstallation {
	out := make([]v1gen.AppInstallation, 0, len(list))
	for _, i := range list {
		out = append(out, installationDTO(i, nil))
	}
	return out
}

func appInputCreate(b *v1gen.AppCreate) apps.Input {
	in := apps.Input{Name: &b.Name, Slug: b.Slug, Description: b.Description, HomepageURL: &b.HomepageUrl,
		LogoURL: b.LogoUrl, Public: b.Public, SetupURL: b.SetupUrl, WebhookURL: b.WebhookUrl,
		WebhookSecret: b.WebhookSecret, WebhookActive: b.WebhookActive}
	if b.Permissions != nil {
		in.Permissions = authz.AppPermissions(*b.Permissions)
	}
	if b.Events != nil {
		in.Events = nonNil(*b.Events)
	}
	if b.CallbackUrls != nil {
		in.CallbackURLs = nonNil(*b.CallbackUrls)
	}
	return in
}

func appInputUpdate(b *v1gen.AppUpdate) apps.Input {
	in := apps.Input{Name: b.Name, Description: b.Description, HomepageURL: b.HomepageUrl, LogoURL: b.LogoUrl,
		Public: b.Public, SetupURL: b.SetupUrl, WebhookURL: b.WebhookUrl, WebhookSecret: b.WebhookSecret,
		WebhookActive: b.WebhookActive}
	if b.Permissions != nil {
		in.Permissions = authz.AppPermissions(*b.Permissions)
	}
	if b.Events != nil {
		in.Events = nonNil(*b.Events)
	}
	if b.CallbackUrls != nil {
		in.CallbackURLs = nonNil(*b.CallbackUrls)
	}
	return in
}

func (s *v1Server) GetAppCatalog(ctx context.Context, _ v1gen.GetAppCatalogRequestObject) (v1gen.GetAppCatalogResponseObject, error) {
	if _, ok := caller(ctx); !ok {
		return errAuthRequired, nil
	}
	out := v1gen.GetAppCatalog200JSONResponse{Repository: slices.Clone(authz.AppRepoPermissions),
		Organization: slices.Clone(authz.AppOrgPermissions), User: slices.Clone(authz.AppUserPermissions),
		WriteOnly: []string{authz.AppWorkflows}}
	for _, e := range authz.AppEvents {
		ev := v1gen.AppCatalogEvent{Name: e, Permission: authz.AppEventPermission(e)}
		if ev.Permission == authz.AppIssues {
			alt := authz.AppPullRequests
			ev.Alternative = &alt
		}
		out.Events = append(out.Events, ev)
	}
	return out, nil
}

func (s *v1Server) ListMyApps(ctx context.Context, _ v1gen.ListMyAppsRequestObject) (v1gen.ListMyAppsResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	list, err := s.apps.ListUserApps(ctx, principal(ctx))
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.ListMyApps200JSONResponse(appsDTO(list)), nil
}

func (s *v1Server) CreateMyApp(ctx context.Context, req v1gen.CreateMyAppRequestObject) (v1gen.CreateMyAppResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	app, err := s.apps.Create(ctx, principal(ctx), "", appInputCreate(req.Body), clientOf(ctx).IP)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.CreateMyApp201JSONResponse(appDTO(app)), nil
}

func (s *v1Server) ListOrgApps(ctx context.Context, req v1gen.ListOrgAppsRequestObject) (v1gen.ListOrgAppsResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	list, err := s.apps.ListOrgApps(ctx, principal(ctx), req.Org)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.ListOrgApps200JSONResponse(appsDTO(list)), nil
}

func (s *v1Server) CreateOrgApp(ctx context.Context, req v1gen.CreateOrgAppRequestObject) (v1gen.CreateOrgAppResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	app, err := s.apps.Create(ctx, principal(ctx), req.Org, appInputCreate(req.Body), clientOf(ctx).IP)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.CreateOrgApp201JSONResponse(appDTO(app)), nil
}

func (s *v1Server) GetApp(ctx context.Context, req v1gen.GetAppRequestObject) (v1gen.GetAppResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	app, err := s.apps.Get(ctx, principal(ctx), req.Slug)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.GetApp200JSONResponse(appDTO(app)), nil
}

func (s *v1Server) UpdateApp(ctx context.Context, req v1gen.UpdateAppRequestObject) (v1gen.UpdateAppResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	app, err := s.apps.Update(ctx, principal(ctx), req.Slug, appInputUpdate(req.Body), clientOf(ctx).IP)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.UpdateApp200JSONResponse(appDTO(app)), nil
}

func (s *v1Server) DeleteApp(ctx context.Context, req v1gen.DeleteAppRequestObject) (v1gen.DeleteAppResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	err := s.apps.Delete(ctx, principal(ctx), req.Slug, clientOf(ctx).IP)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.DeleteApp204Response{}, nil
}

func (s *v1Server) CreateAppKey(ctx context.Context, req v1gen.CreateAppKeyRequestObject) (v1gen.CreateAppKeyResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	k, pemText, err := s.apps.CreateKey(ctx, principal(ctx), req.Slug, clientOf(ctx).IP)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.CreateAppKey201JSONResponse{Key: appKeyDTO(k), PrivateKey: pemText}, nil
}

func (s *v1Server) DeleteAppKey(ctx context.Context, req v1gen.DeleteAppKeyRequestObject) (v1gen.DeleteAppKeyResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	err := s.apps.DeleteKey(ctx, principal(ctx), req.Slug, req.Id, clientOf(ctx).IP)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.DeleteAppKey204Response{}, nil
}

func (s *v1Server) ListAppInstallations(ctx context.Context, req v1gen.ListAppInstallationsRequestObject) (v1gen.ListAppInstallationsResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	list, err := s.apps.ListForApp(ctx, principal(ctx), req.Slug)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.ListAppInstallations200JSONResponse(installationsDTO(list)), nil
}

func (s *v1Server) InstallApp(ctx context.Context, req v1gen.InstallAppRequestObject) (v1gen.InstallAppResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	var names []string
	if req.Body.Repositories != nil {
		names = *req.Body.Repositories
	}
	inst, redirect, err := s.apps.Install(ctx, principal(ctx), req.Slug, req.Body.Account,
		string(req.Body.RepositorySelection), names, clientOf(ctx).IP)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.InstallApp201JSONResponse{Installation: installationDTO(inst, nil), RedirectUrl: optString(redirect)}, nil
}

func (s *v1Server) ListAppInstallTargets(ctx context.Context, req v1gen.ListAppInstallTargetsRequestObject) (v1gen.ListAppInstallTargetsResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	_, targets, err := s.apps.InstallTargets(ctx, principal(ctx), req.Slug)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	out := make(v1gen.ListAppInstallTargets200JSONResponse, 0, len(targets))
	for _, t := range targets {
		o := v1gen.AppInstallTarget{Kind: v1gen.AppInstallTargetKind(t.Kind), Name: t.Name}
		if t.InstallationNumID != 0 {
			n := t.InstallationNumID
			o.InstallationId = &n
		}
		out = append(out, o)
	}
	return out, nil
}

// appHook resolves the hook of an App the caller manages.
func (s *v1Server) appHook(ctx context.Context, slug string) (webhooks.Scope, uuid.UUID, error) {
	if s.webhooks == nil {
		return webhooks.Scope{}, uuid.Nil, apps.ErrNotFound
	}
	return s.apps.HookScope(ctx, principal(ctx), slug)
}

func (s *v1Server) ListAppDeliveries(ctx context.Context, req v1gen.ListAppDeliveriesRequestObject) (v1gen.ListAppDeliveriesResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	sc, hookID, err := s.appHook(ctx, req.Slug)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	if hookID == uuid.Nil {
		return v1gen.ListAppDeliveries200JSONResponse{Body: []v1gen.WebhookDelivery{},
			Headers: v1gen.ListAppDeliveries200ResponseHeaders{XHasMore: hasMoreHeader(false)}}, nil
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	ds, more, err := s.webhooks.Deliveries(ctx, sc, hookID, offset, limit)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.ListAppDeliveries200JSONResponse{Body: deliveriesDTO(ds),
		Headers: v1gen.ListAppDeliveries200ResponseHeaders{XHasMore: hasMoreHeader(more)}}, nil
}

func (s *v1Server) GetAppDelivery(ctx context.Context, req v1gen.GetAppDeliveryRequestObject) (v1gen.GetAppDeliveryResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	sc, hookID, err := s.appHook(ctx, req.Slug)
	if err == nil && hookID == uuid.Nil {
		err = apps.ErrNotFound
	}
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	d, err := s.webhooks.Delivery(ctx, sc, hookID, req.DeliveryId)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.GetAppDelivery200JSONResponse(deliveryDetailDTO(d)), nil
}

func (s *v1Server) RedeliverAppDelivery(ctx context.Context, req v1gen.RedeliverAppDeliveryRequestObject) (v1gen.RedeliverAppDeliveryResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	sc, hookID, err := s.appHook(ctx, req.Slug)
	if err == nil && hookID == uuid.Nil {
		err = apps.ErrNotFound
	}
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	d, err := s.webhooks.Redeliver(ctx, sc, hookID, req.DeliveryId)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.RedeliverAppDelivery202JSONResponse(deliveryDTO(d)), nil
}

func (s *v1Server) ListMyAppInstallations(ctx context.Context, _ v1gen.ListMyAppInstallationsRequestObject) (v1gen.ListMyAppInstallationsResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	list, err := s.apps.ManagedInstallations(ctx, principal(ctx), "")
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.ListMyAppInstallations200JSONResponse(installationsDTO(list)), nil
}

func (s *v1Server) ListOrgAppInstallations(ctx context.Context, req v1gen.ListOrgAppInstallationsRequestObject) (v1gen.ListOrgAppInstallationsResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	list, err := s.apps.ManagedInstallations(ctx, principal(ctx), req.Org)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.ListOrgAppInstallations200JSONResponse(installationsDTO(list)), nil
}

func (s *v1Server) GetAppInstallation(ctx context.Context, req v1gen.GetAppInstallationRequestObject) (v1gen.GetAppInstallationResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	inst, repos, err := s.apps.GetInstallation(ctx, principal(ctx), req.InstallationId)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.GetAppInstallation200JSONResponse(installationDTO(inst, repos)), nil
}

func (s *v1Server) UpdateAppInstallation(ctx context.Context, req v1gen.UpdateAppInstallationRequestObject) (v1gen.UpdateAppInstallationResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	var names []string
	if req.Body.Repositories != nil {
		names = *req.Body.Repositories
	}
	p := principal(ctx)
	_, err := s.apps.SetRepositories(ctx, p, req.InstallationId, string(req.Body.RepositorySelection), names, clientOf(ctx).IP)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	inst, repos, err := s.apps.GetInstallation(ctx, p, req.InstallationId)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.UpdateAppInstallation200JSONResponse(installationDTO(inst, repos)), nil
}

func (s *v1Server) DeleteAppInstallation(ctx context.Context, req v1gen.DeleteAppInstallationRequestObject) (v1gen.DeleteAppInstallationResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	err := s.apps.Uninstall(ctx, principal(ctx), req.InstallationId, clientOf(ctx).IP)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.DeleteAppInstallation204Response{}, nil
}

func (s *v1Server) setSuspended(ctx context.Context, num int64, suspend bool) (v1gen.AppInstallation, *problemResponse, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return v1gen.AppInstallation{}, &pr, nil
	}
	p := principal(ctx)
	if _, err := s.apps.SetSuspended(ctx, p, num, suspend, clientOf(ctx).IP); err != nil {
		if pr, ok := appsProblem(err); ok {
			return v1gen.AppInstallation{}, &pr, nil
		}
		return v1gen.AppInstallation{}, nil, err
	}
	inst, repos, err := s.apps.GetInstallation(ctx, p, num)
	if pr, ok := appsProblem(err); ok {
		return v1gen.AppInstallation{}, &pr, nil
	} else if err != nil {
		return v1gen.AppInstallation{}, nil, err
	}
	return installationDTO(inst, repos), nil, nil
}

func (s *v1Server) SuspendAppInstallation(ctx context.Context, req v1gen.SuspendAppInstallationRequestObject) (v1gen.SuspendAppInstallationResponseObject, error) {
	out, pr, err := s.setSuspended(ctx, req.InstallationId, true)
	if pr != nil {
		return *pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.SuspendAppInstallation200JSONResponse(out), nil
}

func (s *v1Server) UnsuspendAppInstallation(ctx context.Context, req v1gen.UnsuspendAppInstallationRequestObject) (v1gen.UnsuspendAppInstallationResponseObject, error) {
	out, pr, err := s.setSuspended(ctx, req.InstallationId, false)
	if pr != nil {
		return *pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.UnsuspendAppInstallation200JSONResponse(out), nil
}

func (s *v1Server) AcceptAppInstallationPermissions(ctx context.Context, req v1gen.AcceptAppInstallationPermissionsRequestObject) (v1gen.AcceptAppInstallationPermissionsResponseObject, error) {
	if pr, ok := s.appsCall(ctx); !ok {
		return pr, nil
	}
	p := principal(ctx)
	if _, err := s.apps.AcceptPermissions(ctx, p, req.InstallationId, clientOf(ctx).IP); err != nil {
		if pr, ok := appsProblem(err); ok {
			return pr, nil
		}
		return nil, err
	}
	inst, repos, err := s.apps.GetInstallation(ctx, p, req.InstallationId)
	if pr, ok := appsProblem(err); ok {
		return pr, nil
	} else if err != nil {
		return nil, err
	}
	return v1gen.AcceptAppInstallationPermissions200JSONResponse(installationDTO(inst, repos)), nil
}
