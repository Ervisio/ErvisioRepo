package httpapi

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/credentials"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

const (
	remoteHostTTL       = 15 * time.Second
	remoteRequestTTL    = 10 * time.Second
	remoteCloneTTL      = 45 * time.Second
	remoteMaxHosts      = 1024
	remoteMaxSessions   = 16
	remoteMaxProjects   = 16
	remoteMaxRequests   = 8
	remoteMaxTranscript = 65536
	remoteProviderWait  = 12 * time.Second
	remoteProviderCheck = 8 * time.Second
)

type remoteHostKey struct {
	accountID uuid.UUID
	deviceID  uuid.UUID
}

type remoteRelay struct {
	mu        sync.Mutex
	hosts     map[remoteHostKey]*remoteHost
	pollLocks [64]sync.Mutex
}

type remoteHost struct {
	remotePublication
	expiresAt     time.Time
	pending       []*remoteCall
	projectOffers map[uuid.UUID]*remoteProjectOffer
}

type remoteProjectOffer struct {
	project v1gen.ErvisioCodeRemoteProjectPublication
}

type remotePublication struct {
	id               uuid.UUID
	enabled          bool
	historyFiltering bool
	sessions         map[string]v1gen.ErvisioCodeRemoteSession
	projects         map[uuid.UUID]v1gen.ErvisioCodeRemoteProjectPublication
}

type remoteCall struct {
	ctx       context.Context
	cancel    context.CancelFunc
	request   v1gen.ErvisioCodeRelayRequest
	delivered bool
	reply     chan remoteReply
	project   *remoteProjectOffer
	authorize remoteRepositoryAuthorizer
}

type remoteRepositoryAuthorizer func(context.Context, v1gen.ErvisioCodeRemoteRepository) problemResponse

type remoteReply struct {
	state      v1gen.ErvisioCodeRemoteState
	sessionID  string
	hostCursor string
	problem    problemResponse
}

var (
	errRemoteOffline      = problemResponse{status: http.StatusNotFound, detail: "Remote session is not live"}
	errProjectOffline     = problemResponse{status: http.StatusNotFound, detail: "Remote project is not advertised"}
	errProjectAccess      = problemResponse{status: http.StatusForbidden, detail: "Linked repository access could not be verified"}
	errRemoteBusy         = problemResponse{status: http.StatusTooManyRequests, detail: "Remote relay is busy", retryAfter: 2}
	errRemoteTimeout      = problemResponse{status: http.StatusGatewayTimeout, detail: "the host did not acknowledge in time; it may already have accepted the action"}
	errRemoteHost         = problemResponse{status: http.StatusBadGateway, detail: "the host did not accept the Remote action"}
	errRelayConflict      = problemResponse{status: http.StatusConflict, detail: "another Remote relay lifetime is active or this lifetime has stopped"}
	errHistoryUnsupported = problemResponse{status: http.StatusUnprocessableEntity, detail: "the linked host does not support restricted history"}
)

func (r *remoteRelay) list(accountID uuid.UUID, devices map[uuid.UUID][]credentials.ErvisioCodeRemoteMetadata) []v1gen.ErvisioCodeRemoteSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune(time.Now())
	sessions := make([]v1gen.ErvisioCodeRemoteSession, 0)
	for key, host := range r.hosts {
		if _, ok := devices[key.deviceID]; key.accountID != accountID || !ok {
			continue
		}
		for _, session := range host.sessions {
			sessions = append(sessions, session)
		}
	}
	slices.SortFunc(sessions, func(a, b v1gen.ErvisioCodeRemoteSession) int {
		if order := b.LastActivityAt.Compare(a.LastActivityAt); order != 0 {
			return order
		}
		if order := slices.Compare(a.DeviceId[:], b.DeviceId[:]); order != 0 {
			return order
		}
		return cmp.Compare(a.SessionId, b.SessionId)
	})
	return sessions
}

func (r *remoteRelay) listProjects(accountID uuid.UUID, devices map[uuid.UUID]string) []v1gen.ErvisioCodeRemoteProject {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune(time.Now())
	projects := make([]v1gen.ErvisioCodeRemoteProject, 0)
	for key, host := range r.hosts {
		name, ok := devices[key.deviceID]
		if key.accountID != accountID || !ok {
			continue
		}
		for id, project := range host.projects {
			projects = append(projects, v1gen.ErvisioCodeRemoteProject{
				DeviceId: key.deviceID, DeviceName: &name, RelayId: host.id,
				ProjectId: id, Label: project.Label, Repository: project.Repository,
			})
		}
	}
	slices.SortFunc(projects, func(a, b v1gen.ErvisioCodeRemoteProject) int {
		if order := cmp.Compare(a.Label, b.Label); order != 0 {
			return order
		}
		if order := slices.Compare(a.DeviceId[:], b.DeviceId[:]); order != 0 {
			return order
		}
		return slices.Compare(a.ProjectId[:], b.ProjectId[:])
	})
	return projects
}

func (r *remoteRelay) sessionRelay(key remoteHostKey, request v1gen.ErvisioCodeRelayRequest) (uuid.UUID, problemResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune(time.Now())
	host := r.hosts[key]
	if host == nil {
		return uuid.Nil, errRemoteOffline
	}
	if problem := host.offerProblem(request); problem.status != 0 {
		return uuid.Nil, problem
	}
	return host.id, problemResponse{}
}

func (r *remoteRelay) enqueue(ctx context.Context, key remoteHostKey, relayID uuid.UUID, request v1gen.ErvisioCodeRelayRequest, authorize remoteRepositoryAuthorizer) (*remoteCall, problemResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.prune(now)
	host := r.hosts[key]
	if host == nil || host.id != relayID {
		return nil, missingRemoteTarget(request.Op)
	}
	if problem := host.offerProblem(request); problem.status != 0 {
		return nil, problem
	}
	if len(host.pending) >= remoteMaxRequests {
		return nil, errRemoteBusy
	}
	ttl := remoteRequestTTL
	var project *remoteProjectOffer
	if request.Op == v1gen.RelayCreate {
		project = host.projectOffers[*request.ProjectId]
		if project.project.Repository != nil {
			if authorize == nil {
				return nil, errProjectAccess
			}
			ttl = projectDeliveryTTL(project.project.Repository) + remoteProviderWait
		}
	}
	ctx, cancel := context.WithDeadline(ctx, now.Add(ttl))
	if ctx.Err() != nil {
		cancel()
		return nil, errRemoteTimeout
	}
	request.Id = uuid.New()
	request.ExpiresAt, _ = ctx.Deadline()
	call := &remoteCall{ctx: ctx, cancel: cancel, reply: make(chan remoteReply, 1), request: request, project: project, authorize: authorize}
	host.pending = append(host.pending, call)
	return call, problemResponse{}
}

func (r *remoteRelay) lockPoll(key remoteHostKey) func() {
	lock := &r.pollLocks[int(key.deviceID[15])%len(r.pollLocks)]
	lock.Lock()
	return lock.Unlock
}

func (r *remoteRelay) checkPublication(key remoteHostKey, relayID uuid.UUID) problemResponse {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune(time.Now())
	return r.publicationProblem(r.hosts[key], relayID)
}

func (r *remoteRelay) publicationProblem(host *remoteHost, relayID uuid.UUID) problemResponse {
	if host == nil {
		if len(r.hosts) >= remoteMaxHosts {
			return errRemoteBusy
		}
		return problemResponse{}
	}
	if (host.id == relayID) != host.enabled {
		return errRelayConflict
	}
	return problemResponse{}
}

func (r *remoteRelay) poll(key remoteHostKey, publication remotePublication, results map[uuid.UUID]v1gen.ErvisioCodeRelayResult) ([]v1gen.ErvisioCodeRelayRequest, []*remoteCall, problemResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	requests := make([]v1gen.ErvisioCodeRelayRequest, 0)
	now := time.Now()
	r.prune(now)
	host := r.hosts[key]
	if !publication.enabled {
		if host != nil && host.id == publication.id {
			host.withdraw(now)
		}
		return requests, nil, problemResponse{}
	}
	if problem := r.publicationProblem(host, publication.id); problem.status != 0 {
		return nil, nil, problem
	}
	if host == nil || host.id != publication.id {
		host = &remoteHost{}
		if r.hosts == nil {
			r.hosts = make(map[remoteHostKey]*remoteHost)
		}
		r.hosts[key] = host
	}
	host.updateProjectOffers(publication.projects)
	host.expiresAt, host.remotePublication = now.Add(remoteHostTTL), publication
	requests, gated := host.exchange(results, requests)
	return requests, gated, problemResponse{}
}

func (h *remoteHost) updateProjectOffers(projects map[uuid.UUID]v1gen.ErvisioCodeRemoteProjectPublication) {
	offers := make(map[uuid.UUID]*remoteProjectOffer, len(projects))
	for id, project := range projects {
		prior := h.projectOffers[id]
		if prior != nil && prior.project.Label == project.Label && sameRemoteRepository(prior.project.Repository, project.Repository) {
			offers[id] = prior
		} else {
			offers[id] = &remoteProjectOffer{project: project}
		}
	}
	h.projectOffers = offers
}

func sameRemoteRepository(a, b *v1gen.ErvisioCodeRemoteRepository) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (h *remoteHost) exchange(results map[uuid.UUID]v1gen.ErvisioCodeRelayResult, requests []v1gen.ErvisioCodeRelayRequest) ([]v1gen.ErvisioCodeRelayRequest, []*remoteCall) {
	var gated []*remoteCall
	h.pending = slices.DeleteFunc(h.pending, func(call *remoteCall) bool {
		if problem := h.offerProblem(call.request); problem.status != 0 {
			call.reply <- remoteReply{problem: problem}
			return true
		}
		if !call.delivered {
			if call.project != nil {
				if h.projectOffers[*call.request.ProjectId] != call.project {
					call.reply <- remoteReply{problem: errProjectOffline}
					return true
				}
				if call.project.project.Repository != nil {
					gated = append(gated, call)
					return false
				}
			}
			// Never reissue a request even if this HTTP response is lost.
			call.delivered = true
			requests = append(requests, call.request)
			return false
		}
		if result, ok := results[call.request.Id]; ok {
			call.reply <- h.result(call.request, result)
			return true
		}
		return false
	})
	return requests, gated
}

func (r *remoteRelay) dispatchAuthorizedProjects(key remoteHostKey, relayID uuid.UUID, gated []*remoteCall, problems []problemResponse) []v1gen.ErvisioCodeRelayRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.prune(now)
	requests := make([]v1gen.ErvisioCodeRelayRequest, 0, len(gated))
	host := r.hosts[key]
	if host == nil || !host.enabled || host.id != relayID {
		return requests
	}
	for i, call := range gated {
		pending := slices.Index(host.pending, call)
		if pending < 0 || call.delivered || call.ctx.Err() != nil {
			continue
		}
		problem := problems[i]
		if host.projectOffers[*call.request.ProjectId] != call.project {
			problem = errProjectOffline
		}
		if problem.status != 0 {
			call.reply <- remoteReply{problem: problem}
			host.pending = slices.Delete(host.pending, pending, pending+1)
			continue
		}
		if deadline := now.Add(projectDeliveryTTL(call.project.project.Repository)); deadline.Before(call.request.ExpiresAt) {
			call.request.ExpiresAt = deadline
		}
		call.delivered = true
		requests = append(requests, call.request)
	}
	return requests
}

func projectDeliveryTTL(repository *v1gen.ErvisioCodeRemoteRepository) time.Duration {
	if repository.Checkout == v1gen.Separate {
		return remoteCloneTTL
	}
	return remoteRequestTTL
}

func (h *remoteHost) offerProblem(request v1gen.ErvisioCodeRelayRequest) problemResponse {
	if !h.enabled {
		return missingRemoteTarget(request.Op)
	}
	if request.Op == v1gen.RelayCreate {
		if _, ok := h.projects[*request.ProjectId]; !ok {
			return errProjectOffline
		}
		return problemResponse{}
	}
	if _, ok := h.sessions[*request.SessionId]; !ok {
		return errRemoteOffline
	}
	if (request.History != nil || request.Op == v1gen.RelayHistoryBoundary) && !h.historyFiltering {
		return errHistoryUnsupported
	}
	return problemResponse{}
}

func (h *remoteHost) result(request v1gen.ErvisioCodeRelayRequest, result v1gen.ErvisioCodeRelayResult) remoteReply {
	if !result.Ok {
		return remoteReply{problem: errRemoteHost}
	}
	switch request.Op {
	case v1gen.RelayCreate:
		if result.SessionId != nil {
			if _, live := h.sessions[*result.SessionId]; live {
				return remoteReply{sessionID: *result.SessionId}
			}
		}
		return remoteReply{problem: errRemoteTimeout}
	case v1gen.RelayState:
		if result.Messages != nil && result.Busy != nil &&
			(request.History == nil || result.HistoryApplied != nil && *result.HistoryApplied) {
			return remoteReply{state: v1gen.ErvisioCodeRemoteState{Messages: result.Messages, Busy: result.Busy}}
		}
	case v1gen.RelayHistoryBoundary:
		if result.HostCursor != nil && result.Messages == nil && result.Busy == nil {
			return remoteReply{hostCursor: *result.HostCursor}
		}
	default:
		return remoteReply{}
	}
	return remoteReply{problem: errRemoteHost}
}

func missingRemoteTarget(op v1gen.ErvisioCodeRelayOp) problemResponse {
	if op == v1gen.RelayCreate {
		return errProjectOffline
	}
	return errRemoteOffline
}

func (h *remoteHost) withdraw(now time.Time) {
	for _, call := range h.pending {
		call.reply <- remoteReply{problem: missingRemoteTarget(call.request.Op)}
	}
	h.enabled, h.sessions, h.projects, h.pending = false, nil, nil, nil
	h.projectOffers = nil
	h.expiresAt = now.Add(remoteHostTTL)
}

func (r *remoteRelay) prune(now time.Time) {
	for key, host := range r.hosts {
		if !now.Before(host.expiresAt) {
			host.withdraw(now)
			delete(r.hosts, key)
			continue
		}
		host.pending = slices.DeleteFunc(host.pending, func(call *remoteCall) bool {
			return call.ctx.Err() != nil || !now.Before(call.request.ExpiresAt)
		})
	}
}

func (r *remoteRelay) revoke(key remoteHostKey) {
	unlock := r.lockPoll(key)
	defer unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if host := r.hosts[key]; host != nil {
		host.withdraw(time.Now())
		delete(r.hosts, key)
	}
}

func (r *remoteRelay) remove(key remoteHostKey, call *remoteCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if host := r.hosts[key]; host != nil {
		if i := slices.Index(host.pending, call); i >= 0 {
			host.pending = slices.Delete(host.pending, i, i+1)
		}
	}
}
