package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/credentials"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

var (
	errCredentialsUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "credentials are not available"}
	errAuthRequired           = problemResponse{status: http.StatusUnauthorized, detail: "authentication required"}
)

// credentialsProblem maps the errors of the credentials service.
func credentialsProblem(err error) (problemResponse, bool) {
	switch {
	case err == nil:
		return problemResponse{}, false
	case errors.Is(err, credentials.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, true
	case errors.Is(err, credentials.ErrInvalidName), errors.Is(err, credentials.ErrInvalidScope),
		errors.Is(err, credentials.ErrExpiry), errors.Is(err, credentials.ErrInvalidKey):
		return badRequest(err.Error()), true
	case errors.Is(err, credentials.ErrAdminScope):
		return problemResponse{status: http.StatusForbidden, detail: err.Error()}, true
	case errors.Is(err, credentials.ErrNameTaken), errors.Is(err, credentials.ErrKeyTaken), errors.Is(err, credentials.ErrTooMany):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, true
	}
	return problemResponse{}, false
}

func (p problemResponse) VisitListAccessTokensResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitCreateAccessTokenResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitRevokeAccessTokenResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListSshKeysResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitAddSshKeyResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitDeleteSshKeyResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitListGpgKeysResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitAddGpgKeyResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitDeleteGpgKeyResponse(w http.ResponseWriter) error { return p.write(w) }

// caller returns the authenticated principal; the security requirement of
// every credentials operation guarantees one.
func caller(ctx context.Context) (auth.Principal, bool) {
	p, ok := auth.PrincipalFrom(ctx)
	return p, ok
}

func tokenDTO(t credentials.Token) v1gen.AccessToken {
	return v1gen.AccessToken{Id: t.ID, Name: t.Name, Hint: t.Hint, Scopes: t.Scopes, ExpiresAt: t.ExpiresAt,
		LastUsedAt: t.LastUsedAt, CreatedAt: t.CreatedAt}
}

func sshKeyDTO(k credentials.StoredSSHKey) v1gen.SshKey {
	out := v1gen.SshKey{Id: k.ID, Title: k.Title, Type: k.Type, Fingerprint: "SHA256:" + k.Fingerprint, PublicKey: k.PublicKey,
		LastUsedAt: k.LastUsedAt, CreatedAt: k.CreatedAt}
	if k.Bits > 0 {
		bits := k.Bits
		out.Bits = &bits
	}
	return out
}

func gpgKeyDTO(k credentials.StoredGPGKey) v1gen.GpgKey {
	return v1gen.GpgKey{Id: k.ID, KeyId: k.KeyID, Fingerprint: k.Fingerprint, SubkeyIds: nonNil(k.SubkeyIDs),
		Emails: nonNil(k.Emails), VerifiedEmails: nonNil(k.Verified), ExpiresAt: k.ExpiresAt, CreatedAt: k.CreatedAt}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *v1Server) ListAccessTokens(ctx context.Context, _ v1gen.ListAccessTokensRequestObject) (v1gen.ListAccessTokensResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	list, err := s.creds.ListTokens(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := make(v1gen.ListAccessTokens200JSONResponse, 0, len(list))
	for _, t := range list {
		out = append(out, tokenDTO(t))
	}
	return out, nil
}

func (s *v1Server) CreateAccessToken(ctx context.Context, req v1gen.CreateAccessTokenRequestObject) (v1gen.CreateAccessTokenResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	scopes := make([]string, 0, len(req.Body.Scopes))
	for _, sc := range req.Body.Scopes {
		scopes = append(scopes, string(sc))
	}
	ct, err := s.creds.CreateToken(ctx, p, credentials.NewTokenInput{Name: req.Body.Name, Scopes: scopes,
		ExpiresAt: req.Body.ExpiresAt}, clientOf(ctx).IP)
	if pr, ok := credentialsProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return noStore{v1gen.CreateAccessToken201JSONResponse{Token: tokenDTO(ct.Token), Secret: ct.Secret}}, nil
}

// noStore keeps the one-time token secret out of caches.
type noStore struct {
	v1gen.CreateAccessToken201JSONResponse
}

func (n noStore) VisitCreateAccessTokenResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "no-store")
	return n.CreateAccessToken201JSONResponse.VisitCreateAccessTokenResponse(w)
}

func (s *v1Server) RevokeAccessToken(ctx context.Context, req v1gen.RevokeAccessTokenRequestObject) (v1gen.RevokeAccessTokenResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	err := s.creds.RevokeToken(ctx, p.UserID, req.Id, clientOf(ctx).IP)
	if pr, ok := credentialsProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.RevokeAccessToken204Response{}, nil
}

func (s *v1Server) ListSshKeys(ctx context.Context, _ v1gen.ListSshKeysRequestObject) (v1gen.ListSshKeysResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	list, err := s.creds.ListSSHKeys(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := make(v1gen.ListSshKeys200JSONResponse, 0, len(list))
	for _, k := range list {
		out = append(out, sshKeyDTO(k))
	}
	return out, nil
}

func (s *v1Server) AddSshKey(ctx context.Context, req v1gen.AddSshKeyRequestObject) (v1gen.AddSshKeyResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	k, err := s.creds.AddSSHKey(ctx, p.UserID, req.Body.Title, req.Body.Key, clientOf(ctx).IP)
	if pr, ok := credentialsProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.AddSshKey201JSONResponse(sshKeyDTO(k)), nil
}

func (s *v1Server) DeleteSshKey(ctx context.Context, req v1gen.DeleteSshKeyRequestObject) (v1gen.DeleteSshKeyResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	err := s.creds.RemoveSSHKey(ctx, p.UserID, req.Id, clientOf(ctx).IP)
	if pr, ok := credentialsProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.DeleteSshKey204Response{}, nil
}

func (s *v1Server) ListGpgKeys(ctx context.Context, _ v1gen.ListGpgKeysRequestObject) (v1gen.ListGpgKeysResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	list, err := s.creds.ListGPGKeys(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := make(v1gen.ListGpgKeys200JSONResponse, 0, len(list))
	for _, k := range list {
		out = append(out, gpgKeyDTO(k))
	}
	return out, nil
}

func (s *v1Server) AddGpgKey(ctx context.Context, req v1gen.AddGpgKeyRequestObject) (v1gen.AddGpgKeyResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	k, err := s.creds.AddGPGKey(ctx, p.UserID, req.Body.ArmoredKey, clientOf(ctx).IP)
	if pr, ok := credentialsProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.AddGpgKey201JSONResponse(gpgKeyDTO(k)), nil
}

func (s *v1Server) DeleteGpgKey(ctx context.Context, req v1gen.DeleteGpgKeyRequestObject) (v1gen.DeleteGpgKeyResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	err := s.creds.RemoveGPGKey(ctx, p.UserID, req.Id, clientOf(ctx).IP)
	if pr, ok := credentialsProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.DeleteGpgKey204Response{}, nil
}
