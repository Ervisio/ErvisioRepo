package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/credentials"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

func (p problemResponse) VisitGetErvisioCodeAccountResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListErvisioCodeDevicesResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitRevokeErvisioCodeDeviceResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitSetErvisioCodeRemotePresenceResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func ervisioCodeDeviceDTO(d credentials.ErvisioCodeDevice) v1gen.ErvisioCodeDevice {
	device := v1gen.ErvisioCodeDevice{Id: d.ID, Name: d.Name, CreatedAt: d.CreatedAt,
		ExpiresAt: d.ExpiresAt, LastUsedAt: d.LastUsedAt}
	if d.RemoteOnline != nil {
		device.Remote = &v1gen.ErvisioCodeRemotePresence{Online: *d.RemoteOnline}
	}
	return device
}

func (s *v1Server) GetErvisioCodeAccount(ctx context.Context, _ v1gen.GetErvisioCodeAccountRequestObject) (v1gen.GetErvisioCodeAccountResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok || !p.ErvisioCodeDevice {
		return errAuthRequired, nil
	}
	device, err := s.creds.GetErvisioCodeDevice(ctx, p.UserID, p.TokenID)
	if errors.Is(err, credentials.ErrNotFound) {
		return errAuthRequired, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.GetErvisioCodeAccount200JSONResponse{
		AccountId: p.UserID, Username: p.Username, Device: ervisioCodeDeviceDTO(device),
	}, nil
}

func (s *v1Server) ListErvisioCodeDevices(ctx context.Context, _ v1gen.ListErvisioCodeDevicesRequestObject) (v1gen.ListErvisioCodeDevicesResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	devices, err := s.creds.ListErvisioCodeDevices(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]v1gen.ErvisioCodeDevice, 0, len(devices))
	for _, device := range devices {
		out = append(out, ervisioCodeDeviceDTO(device))
	}
	cacheControl := "no-store"
	return v1gen.ListErvisioCodeDevices200JSONResponse{
		Body: out, Headers: v1gen.ListErvisioCodeDevices200ResponseHeaders{CacheControl: &cacheControl},
	}, nil
}

func (s *v1Server) SetErvisioCodeRemotePresence(ctx context.Context, req v1gen.SetErvisioCodeRemotePresenceRequestObject) (v1gen.SetErvisioCodeRemotePresenceResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok || p.Method != auth.MethodToken || !p.ErvisioCodeDevice {
		return errAuthRequired, nil
	}
	if req.Body == nil {
		return problemResponse{status: http.StatusBadRequest, detail: "expected an object with an active boolean"}, nil
	}
	err := s.creds.SetErvisioCodeRemotePresence(ctx, p.UserID, p.TokenID, req.Body.Active)
	if errors.Is(err, credentials.ErrNotFound) {
		return errAuthRequired, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.SetErvisioCodeRemotePresence204Response{}, nil
}

func (s *v1Server) RevokeErvisioCodeDevice(ctx context.Context, req v1gen.RevokeErvisioCodeDeviceRequestObject) (v1gen.RevokeErvisioCodeDeviceResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	if p.Method == auth.MethodToken && (!p.ErvisioCodeDevice || p.TokenID != req.Id) {
		return problemResponse{status: http.StatusForbidden, detail: "a device credential can revoke only itself"}, nil
	}
	err := s.creds.RevokeErvisioCodeDevice(ctx, p.UserID, req.Id, clientOf(ctx).IP)
	if pr, ok := credentialsProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.RevokeErvisioCodeDevice204Response{}, nil
}
