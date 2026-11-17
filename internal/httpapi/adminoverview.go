package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// LocalDisk reports the disk of the first local storage target (satisfied
// by *tier.Tier).
type LocalDisk interface {
	LocalDisk() (name string, d tier.DiskUsage, ok bool)
}

// overviewRange is the bucket layout of one range of the admin overview.
type overviewRange struct {
	step    time.Duration
	buckets int
}

var overviewRanges = map[v1gen.GetAdminOverviewParamsRange]overviewRange{
	v1gen.GetAdminOverviewParamsRangeN24h: {step: 30 * time.Minute, buckets: 48},
	v1gen.GetAdminOverviewParamsRangeN7d:  {step: 2 * time.Hour, buckets: 84},
}

func (s *v1Server) GetAdminOverview(ctx context.Context, req v1gen.GetAdminOverviewRequestObject) (v1gen.GetAdminOverviewResponseObject, error) {
	rng := v1gen.GetAdminOverviewParamsRangeN24h
	if req.Params.Range != nil {
		rng = *req.Params.Range
	}
	lay, ok := overviewRanges[rng]
	if !ok {
		return badRequest("range must be 24h or 7d"), nil
	}
	now := time.Now()
	// The last bucket is the current, partial one.
	start := now.Truncate(lay.step).Add(-time.Duration(lay.buckets-1) * lay.step)
	out := v1gen.AdminOverview{
		Range: v1gen.AdminOverviewRange(rng), Start: start, StepSeconds: int(lay.step / time.Second),
		Requests: make([]float64, lay.buckets), Jobs: make([]v1gen.AdminJobBucket, lay.buckets),
		Disk: []v1gen.AdminDiskSample{}, CheckedAt: now,
	}
	if s.monitor != nil {
		out.Requests = s.monitor.RequestRate(start, lay.step, lay.buckets)
		out.RequestsNow = s.monitor.LastMinute()
		for _, d := range s.monitor.Disk(start) {
			out.Disk = append(out.Disk, v1gen.AdminDiskSample{At: d.At, Used: d.Used, Total: d.Total})
		}
	}
	if ld, ok := s.storage.(LocalDisk); ok {
		if name, _, ok := ld.LocalDisk(); ok {
			out.DiskTarget = &name
		}
	}
	if s.db != nil {
		q := dbgen.New(s.db)
		rows, err := q.AdminActionsJobBuckets(ctx, dbgen.AdminActionsJobBucketsParams{
			Since: pgtype.Timestamptz{Time: start, Valid: true}, StepSeconds: int32(lay.step / time.Second), //nolint:gosec // at most 7200
		})
		if err != nil {
			return nil, fmt.Errorf("job buckets: %w", err)
		}
		for _, r := range rows {
			if r.Bucket >= 0 && int(r.Bucket) < lay.buckets {
				out.Jobs[r.Bucket] = v1gen.AdminJobBucket{Success: r.Success, Failure: r.Failure}
			}
		}
		c, err := q.AdminInstanceCounts(ctx)
		if err != nil {
			return nil, fmt.Errorf("instance counts: %w", err)
		}
		out.Counts = v1gen.AdminCounts{Users: c.Users, Orgs: c.Orgs, Invites: c.Invites}
	}
	services, runners, err := s.services(ctx, now)
	if err != nil {
		return nil, err
	}
	out.Services, out.Counts.Runners = services, runners
	return v1gen.GetAdminOverview200JSONResponse(out), nil
}

// services reports the state of each subsystem, and the number of instance runners.
func (s *v1Server) services(ctx context.Context, now time.Time) ([]v1gen.AdminService, int, error) {
	ok, warn, failed, off := v1gen.AdminServiceStatusOk, v1gen.AdminServiceStatusWarn, v1gen.AdminServiceStatusErr, v1gen.AdminServiceStatusOff
	server := v1gen.AdminService{Id: v1gen.AdminServiceIdServer, Status: ok, Version: &s.meta.Version}
	if s.monitor != nil {
		up := int64(now.Sub(s.monitor.Started()) / time.Second)
		server.UptimeSeconds = &up
	}
	out := []v1gen.AdminService{server}

	dbSvc := v1gen.AdminService{Id: v1gen.AdminServiceIdDatabase, Status: failed}
	if s.db != nil {
		if size, err := dbgen.New(s.db).DatabaseSize(ctx); err == nil {
			dbSvc.Status, dbSvc.Bytes = ok, &size
		} else {
			s.log.WarnContext(ctx, "admin overview: database size", "error", err)
		}
	}
	out = append(out, dbSvc)

	st := v1gen.AdminService{Id: v1gen.AdminServiceIdStorage, Status: off}
	if s.storage != nil {
		status, err := s.storage.Status(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("storage status: %w", err)
		}
		n := len(status.Warnings)
		st.Status, st.Warnings = ok, &n
		if n > 0 {
			st.Status = warn
		}
	}
	out = append(out, st)

	hot := v1gen.AdminService{Id: v1gen.AdminServiceIdHotCache, Status: off}
	if s.tiering != nil {
		if h, err := s.tiering.HotSummary(ctx); err == nil {
			hot.Status, hot.Bytes, hot.Budget = ok, &h.Bytes, &h.Budget
			if h.Budget > 0 && h.High > 0 && float64(h.Bytes) >= h.High*float64(h.Budget) {
				hot.Status = warn
			}
		} else {
			s.log.WarnContext(ctx, "admin overview: hot cache summary", "error", err)
		}
	}
	out = append(out, hot)

	rn, runners := s.runnersService(ctx)
	out = append(out, rn)

	mail := v1gen.AdminService{Id: v1gen.AdminServiceIdEmail, Status: off}
	if s.mailHost != "" {
		mail.Status, mail.Host = ok, &s.mailHost
	}
	out = append(out, mail)

	jobs := v1gen.AdminService{Id: v1gen.AdminServiceIdJobs, Status: failed}
	if s.jobs != nil && s.jobs.Running() {
		jobs.Status = ok
	}
	return append(out, jobs), runners, nil
}

func (s *v1Server) runnersService(ctx context.Context) (v1gen.AdminService, int) {
	svc := v1gen.AdminService{Id: v1gen.AdminServiceIdRunners, Status: v1gen.AdminServiceStatusOff}
	if s.actions == nil {
		return svc, 0
	}
	who := principal(ctx)
	sc, err := s.actions.InstanceScope(who)
	if err != nil {
		return svc, 0
	}
	list, err := s.actions.ListRunners(ctx, who, sc)
	if err != nil {
		s.log.WarnContext(ctx, "admin overview: runners", "error", err)
		return svc, 0
	}
	online := 0
	for _, r := range list {
		if r.Online {
			online++
		}
	}
	total := len(list)
	svc.Online, svc.Total = &online, &total
	if s.db != nil {
		if q, err := dbgen.New(s.db).CountQueuedActionsJobs(ctx); err == nil {
			svc.Queued = &q
		}
	}
	switch {
	case total == 0:
		svc.Status = v1gen.AdminServiceStatusOff
	case online == 0:
		svc.Status = v1gen.AdminServiceStatusErr
	case online < total:
		svc.Status = v1gen.AdminServiceStatusWarn
	default:
		svc.Status = v1gen.AdminServiceStatusOk
	}
	return svc, total
}

func (p problemResponse) VisitGetAdminOverviewResponse(w http.ResponseWriter) error {
	return p.write(w)
}
