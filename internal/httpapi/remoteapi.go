package httpapi

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/credentials"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

var remoteSessionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (p problemResponse) VisitListErvisioCodeSessionsResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitActOnErvisioCodeSessionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitPollErvisioCodeRelayResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (s *v1Server) remoteAccount(ctx context.Context, selected *uuid.UUID) (uuid.UUID, problemResponse, error) {
	if s.creds == nil {
		return uuid.Nil, errCredentialsUnavailable, nil
	}
	if p, ok := caller(ctx); ok && (p.Method == auth.MethodSession || p.Method == auth.MethodToken && p.ErvisioCodeDevice) {
		if selected == nil || p.Method == auth.MethodToken && *selected == p.UserID {
			return p.UserID, problemResponse{}, nil
		}
	}
	if selected == nil {
		return uuid.Nil, errAuthRequired, nil
	}
	return s.remoteHubAccount(ctx, *selected)
}

func (s *v1Server) remoteHubAccount(ctx context.Context, accountID uuid.UUID) (uuid.UUID, problemResponse, error) {
	secret, ok := ctx.Value(hubSessionKey{}).(string)
	if !ok || accountID == uuid.Nil {
		return uuid.Nil, errAuthRequired, nil
	}
	_, err := s.creds.GetErvisioCodeHubSession(ctx, accountID, secret)
	if errors.Is(err, credentials.ErrNotFound) {
		return uuid.Nil, errAuthRequired, nil
	}
	return accountID, problemResponse{}, err
}

func (s *v1Server) ListErvisioCodeSessions(ctx context.Context, req v1gen.ListErvisioCodeSessionsRequestObject) (v1gen.ListErvisioCodeSessionsResponseObject, error) {
	accountID, problem, err := s.remoteAccount(ctx, req.Params.AccountId)
	if err != nil {
		return nil, err
	}
	if problem.status != 0 {
		return problem, nil
	}
	devices, err := s.creds.ListErvisioCodeRemoteMetadata(ctx, accountID)
	if err != nil {
		return nil, err
	}
	live := s.remote.list(accountID, devices)
	return v1gen.ListErvisioCodeSessions200JSONResponse{Sessions: live, Recent: offlineRemoteSessions(devices, live)}, nil
}

func offlineRemoteSessions(devices map[uuid.UUID][]credentials.ErvisioCodeRemoteMetadata, live []v1gen.ErvisioCodeRemoteSession) []v1gen.ErvisioCodeRemoteRecent {
	type sessionKey struct {
		deviceID  uuid.UUID
		sessionID string
	}
	online := make(map[sessionKey]struct{}, len(live))
	for _, session := range live {
		online[sessionKey{session.DeviceId, session.SessionId}] = struct{}{}
	}
	recent := make([]v1gen.ErvisioCodeRemoteRecent, 0)
	for deviceID, metadata := range devices {
		for _, session := range metadata {
			if _, live := online[sessionKey{deviceID, session.SessionID}]; !live {
				recent = append(recent, v1gen.ErvisioCodeRemoteRecent{
					DeviceId: deviceID, SessionId: session.SessionID, Title: session.Title, LastActivityAt: session.LastActivityAt,
				})
			}
		}
	}
	slices.SortFunc(recent, func(a, b v1gen.ErvisioCodeRemoteRecent) int {
		if order := b.LastActivityAt.Compare(a.LastActivityAt); order != 0 {
			return order
		}
		if order := slices.Compare(a.DeviceId[:], b.DeviceId[:]); order != 0 {
			return order
		}
		return cmp.Compare(a.SessionId, b.SessionId)
	})
	return recent
}

func (s *v1Server) remoteDevices(ctx context.Context, accountID uuid.UUID) (map[uuid.UUID]string, error) {
	devices, err := s.creds.ListErvisioCodeDevices(ctx, accountID)
	if err != nil {
		return nil, err
	}
	active := make(map[uuid.UUID]string, len(devices))
	for _, device := range devices {
		active[device.ID] = device.Name
	}
	return active, nil
}

func (s *v1Server) ActOnErvisioCodeSession(ctx context.Context, req v1gen.ActOnErvisioCodeSessionRequestObject) (v1gen.ActOnErvisioCodeSessionResponseObject, error) {
	accountID, problem, err := s.remoteAccount(ctx, req.Params.AccountId)
	if err != nil {
		return nil, err
	}
	if problem.status != 0 {
		return problem, nil
	}
	if !validRemoteAction(req.Body) {
		return problemResponse{status: http.StatusBadRequest, detail: "invalid Remote action"}, nil
	}
	action := *req.Body
	reply, err := s.sendRemoteRequest(ctx, remoteHostKey{accountID: accountID, deviceID: action.DeviceId}, action.RelayId,
		v1gen.ErvisioCodeRelayRequest{Op: v1gen.ErvisioCodeRelayOp(action.Op), SessionId: &action.SessionId, Text: action.Text})
	if err != nil {
		return nil, err
	}
	if reply.problem.status != 0 {
		return reply.problem, nil
	}
	return v1gen.ActOnErvisioCodeSession200JSONResponse(reply.state), nil
}

func (s *v1Server) sendRemoteRequest(ctx context.Context, key remoteHostKey, relayID uuid.UUID, request v1gen.ErvisioCodeRelayRequest) (remoteReply, error) {
	return s.sendAuthorizedRemoteRequest(ctx, key, relayID, request, nil)
}

func (s *v1Server) sendAuthorizedRemoteRequest(ctx context.Context, key remoteHostKey, relayID uuid.UUID, request v1gen.ErvisioCodeRelayRequest, authorize remoteRepositoryAuthorizer) (remoteReply, error) {
	_, err := s.creds.GetErvisioCodeDevice(ctx, key.accountID, key.deviceID)
	if errors.Is(err, credentials.ErrNotFound) {
		return remoteReply{problem: missingRemoteTarget(request.Op)}, nil
	}
	if err != nil {
		return remoteReply{}, err
	}
	call, problem := s.remote.enqueue(ctx, key, relayID, request, authorize)
	if problem.status != 0 {
		return remoteReply{problem: problem}, nil
	}
	defer s.remote.remove(key, call)
	defer call.cancel()
	select {
	case reply := <-call.reply:
		if call.ctx.Err() == nil && time.Now().Before(call.request.ExpiresAt) {
			return reply, nil
		}
	case <-call.ctx.Done():
	}
	return remoteReply{problem: errRemoteTimeout}, nil
}

func validRemoteAction(action *v1gen.ErvisioCodeRemoteAction) bool {
	if action == nil || action.DeviceId == uuid.Nil || action.RelayId == uuid.Nil || !remoteSessionID.MatchString(action.SessionId) {
		return false
	}
	switch action.Op {
	case v1gen.Prompt:
		return action.Text != nil && len(*action.Text) <= 16384 && strings.TrimSpace(*action.Text) != "" && !strings.ContainsRune(*action.Text, 0)
	case v1gen.State, v1gen.Interrupt:
		return action.Text == nil
	default:
		return false
	}
}

func (s *v1Server) PollErvisioCodeRelay(ctx context.Context, req v1gen.PollErvisioCodeRelayRequestObject) (v1gen.PollErvisioCodeRelayResponseObject, error) {
	p, ok := caller(ctx)
	if !ok || p.Method != auth.MethodToken || !p.ErvisioCodeDevice {
		return errAuthRequired, nil
	}
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	if req.Body == nil || req.Body.RelayId == uuid.Nil || req.Body.Sessions == nil || len(req.Body.Sessions) > remoteMaxSessions {
		return problemResponse{status: http.StatusBadRequest, detail: "invalid Remote publication"}, nil
	}
	body := req.Body
	enabled := len(body.Sessions) > 0
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	key := remoteHostKey{accountID: p.UserID, deviceID: p.TokenID}
	unlock := s.remote.lockPoll(key)
	defer unlock()
	if !enabled {
		requests, _, _ := s.remote.poll(key, remotePublication{id: body.RelayId}, nil)
		return v1gen.PollErvisioCodeRelay200JSONResponse{Requests: requests}, nil
	}
	return s.publishRemote(ctx, key, body)
}

func (s *v1Server) publishRemote(ctx context.Context, key remoteHostKey, body *v1gen.ErvisioCodeRelayPoll) (v1gen.PollErvisioCodeRelayResponseObject, error) {
	metadata, valid := remoteMetadata(body.Sessions)
	if !valid {
		return problemResponse{status: http.StatusBadRequest, detail: "invalid Remote publication"}, nil
	}
	projects, valid := remoteProjects(body.Projects)
	if !valid {
		return problemResponse{status: http.StatusBadRequest, detail: "invalid Remote project advertisement"}, nil
	}
	results, valid := remoteResults(body.Results)
	if !valid {
		return problemResponse{status: http.StatusBadRequest, detail: "invalid Remote result"}, nil
	}
	if problem := s.remote.checkPublication(key, body.RelayId); problem.status != 0 {
		return problem, nil
	}
	err := s.creds.RecordErvisioCodeRemoteMetadata(ctx, key.accountID, key.deviceID, metadata)
	if errors.Is(err, credentials.ErrNotFound) {
		return errAuthRequired, nil
	}
	if err != nil {
		return nil, err
	}
	publication := remotePublication{
		id: body.RelayId, enabled: true, projects: projects,
		historyFiltering: body.HistoryFiltering != nil && *body.HistoryFiltering,
		sessions:         remoteSessions(key.deviceID, body.RelayId, metadata),
	}
	requests, gated, problem := s.remote.poll(key, publication, results)
	if problem.status != 0 {
		return problem, nil
	}
	if len(gated) > 0 && len(requests) == 0 {
		requests, problem, err = s.dispatchRepositoryProjects(ctx, key, body.RelayId, gated)
		if err != nil || problem.status != 0 {
			return problem, err
		}
	}
	return v1gen.PollErvisioCodeRelay200JSONResponse{Requests: requests}, nil
}

func (s *v1Server) dispatchRepositoryProjects(ctx context.Context, key remoteHostKey, relayID uuid.UUID, gated []*remoteCall) ([]v1gen.ErvisioCodeRelayRequest, problemResponse, error) {
	problems := make([]problemResponse, len(gated))
	var checks sync.WaitGroup
	for i, call := range gated {
		checks.Go(func() {
			checkCtx, cancel := context.WithTimeout(call.ctx, remoteProviderCheck)
			defer cancel()
			stop := context.AfterFunc(ctx, cancel)
			defer stop()
			problems[i] = call.authorize(checkCtx, *call.project.project.Repository)
		})
	}
	checks.Wait()
	if ctx.Err() != nil {
		return nil, errRemoteTimeout, nil
	}
	_, err := s.creds.GetErvisioCodeDevice(ctx, key.accountID, key.deviceID)
	if errors.Is(err, credentials.ErrNotFound) {
		return nil, errAuthRequired, nil
	}
	if err != nil {
		return nil, problemResponse{}, err
	}
	return s.remote.dispatchAuthorizedProjects(key, relayID, gated, problems), problemResponse{}, nil
}

func remoteSessions(deviceID, relayID uuid.UUID, metadata []credentials.ErvisioCodeRemoteMetadata) map[string]v1gen.ErvisioCodeRemoteSession {
	sessions := make(map[string]v1gen.ErvisioCodeRemoteSession, len(metadata))
	for _, session := range metadata {
		sessions[session.SessionID] = v1gen.ErvisioCodeRemoteSession{
			DeviceId: deviceID, RelayId: relayID, SessionId: session.SessionID,
			Title: session.Title, LastActivityAt: session.LastActivityAt,
		}
	}
	return sessions
}

func remoteMetadata(sessions []v1gen.ErvisioCodeRemotePublication) ([]credentials.ErvisioCodeRemoteMetadata, bool) {
	metadata := make([]credentials.ErvisioCodeRemoteMetadata, 0, len(sessions))
	seen := make(map[string]struct{}, len(sessions))
	for _, session := range sessions {
		if !remoteSessionID.MatchString(session.SessionId) || len(session.Title) > 256 || strings.ContainsRune(session.Title, 0) {
			return nil, false
		}
		if _, ok := seen[session.SessionId]; ok {
			return nil, false
		}
		seen[session.SessionId] = struct{}{}
		entry := credentials.ErvisioCodeRemoteMetadata{SessionID: session.SessionId, Title: session.Title}
		if session.LastActivityAt != nil {
			entry.LastActivityAt = *session.LastActivityAt
		}
		metadata = append(metadata, entry)
	}
	return metadata, true
}

func remoteResults(input *[]v1gen.ErvisioCodeRelayResult) (map[uuid.UUID]v1gen.ErvisioCodeRelayResult, bool) {
	if input == nil {
		return nil, true
	}
	if len(*input) > remoteMaxRequests {
		return nil, false
	}
	results := make(map[uuid.UUID]v1gen.ErvisioCodeRelayResult, len(*input))
	for _, result := range *input {
		if result.Id == uuid.Nil || result.Error != nil && len(*result.Error) > 160 ||
			result.SessionId != nil && !remoteSessionID.MatchString(*result.SessionId) ||
			result.HostCursor != nil && !remoteSessionID.MatchString(*result.HostCursor) || !validRemoteMessages(result.Messages) {
			return nil, false
		}
		if _, ok := results[result.Id]; ok {
			return nil, false
		}
		results[result.Id] = result
	}
	return results, true
}

func validRemoteMessages(messages *[]v1gen.ErvisioCodeRemoteMessage) bool {
	if messages == nil {
		return true
	}
	totalBytes := 0
	for _, message := range *messages {
		if !message.Role.Valid() || len(message.Text) > 8192 ||
			message.MessageId != nil && !remoteSessionID.MatchString(*message.MessageId) {
			return false
		}
		totalBytes += len(message.Text)
	}
	return len(*messages) <= 50 && totalBytes <= remoteMaxTranscript
}
