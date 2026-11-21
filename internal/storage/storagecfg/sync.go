package storagecfg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Backup copies. A replica is the backup of a policy: objects are written
// to the primary (the active storage, an S3 bucket say) and copied to the
// replicas in the background (a Storage Box over SFTP), which serve reads
// only when the primary fails. These operations keep that copy complete.

const (
	ActionResync    = "admin.storage_resync"
	ActionVerifyAll = "admin.storage_verify"

	// settingReplicaSets records the replicas of each policy at the last
	// start, to notice a replica added since.
	settingReplicaSets = "storage.replica_sets"
	// maxFullVerify bounds one verification asked from the admin.
	maxFullVerify = 1_000_000
)

// ErrNoReplicas: the policy has no replica to copy to.
var ErrNoReplicas = errors.New("storage configuration: the policy has no replica")

// Jobs starts the storage jobs (satisfied by an adapter over *jobs.Runner).
type Jobs interface {
	EnqueueReplicaSweep(ctx context.Context) error
	EnqueueFullVerify(ctx context.Context, limit int32) error
	// MirrorGitManifests copies the manifest of every repository to the
	// replicas of the git policy, in the background.
	MirrorGitManifests()
}

// SetJobs wires the job runner, created after the service.
func (s *Service) SetJobs(j Jobs) { s.jobs = j }

// Resync queues every object of a running policy for a new copy to its
// replicas and returns how many. Copies already complete are only
// re-hashed.
func (s *Service) Resync(ctx context.Context, actor uuid.UUID, ip, policy string) (int64, error) {
	p, ok := s.opt.Running.Policy[policy]
	if !ok {
		return 0, ErrNotFound
	}
	if len(p.Replicas) == 0 {
		return 0, ErrNoReplicas
	}
	n, err := s.q.MarkPolicyReplicasPending(ctx, policy)
	if err != nil {
		return 0, fmt.Errorf("queue %s for replication: %w", policy, err)
	}
	if err := audit.Record(ctx, s.q, audit.Event{Actor: actor, Action: ActionResync, TargetType: "storage_policy",
		TargetID: policy, IP: ip, Data: map[string]any{"policy": policy, "objects": n, "replicas": p.Replicas}}); err != nil {
		return n, err
	}
	s.kick(ctx, policy)
	return n, nil
}

func (s *Service) kick(ctx context.Context, policy string) {
	if s.jobs == nil {
		return
	}
	if err := s.jobs.EnqueueReplicaSweep(ctx); err != nil {
		s.opt.Log.WarnContext(ctx, "start replica sweep", "error", err)
	}
	if policy == "git" {
		s.jobs.MirrorGitManifests()
	}
}

// VerifyAll starts a verification of every stored object on the primary
// and on every replica; a bad copy is repaired from a good one. It returns
// how many objects will be checked.
func (s *Service) VerifyAll(ctx context.Context, actor uuid.UUID, ip string) (int64, error) {
	if s.jobs == nil {
		return 0, errors.New("storage verification: the job runner is not available")
	}
	n, err := s.q.CountStorageObjects(ctx)
	if err != nil {
		return 0, fmt.Errorf("count storage objects: %w", err)
	}
	limit := min(n, maxFullVerify)
	if err := s.jobs.EnqueueFullVerify(ctx, int32(limit)); err != nil { //nolint:gosec // bounded by maxFullVerify
		return 0, fmt.Errorf("start storage verification: %w", err)
	}
	if err := audit.Record(ctx, s.q, audit.Event{Actor: actor, Action: ActionVerifyAll, TargetType: "storage",
		TargetID: "all", IP: ip, Data: map[string]any{"objects": limit}}); err != nil {
		return limit, err
	}
	return limit, nil
}

// BackfillNewReplicas runs at start: when a policy got a replica since the
// last start, its existing objects are queued for replication (new
// objects are copied anyway). The first start that records the replicas
// assumes they are complete.
func (s *Service) BackfillNewReplicas(ctx context.Context) error {
	now := map[string][]string{}
	for name, p := range s.opt.Running.Policy {
		r := slices.Clone(p.Replicas)
		slices.Sort(r)
		now[name] = r
	}
	var before map[string][]string
	row, err := s.q.GetSetting(ctx, settingReplicaSets)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return fmt.Errorf("load replica sets: %w", err)
	default:
		if err := json.Unmarshal(row.Value, &before); err != nil {
			before = nil
		}
	}
	if before != nil {
		for _, name := range slices.Sorted(maps.Keys(now)) {
			added := false
			for _, r := range now[name] {
				if !slices.Contains(before[name], r) {
					added = true
				}
			}
			if !added {
				continue
			}
			n, err := s.q.MarkPolicyReplicasPending(ctx, name)
			if err != nil {
				return fmt.Errorf("queue %s for its new replica: %w", name, err)
			}
			s.opt.Log.InfoContext(ctx, "new storage replica: copying existing objects", "policy", name,
				"replicas", now[name], "objects", n)
			s.kick(ctx, name)
		}
	}
	b, err := json.Marshal(now)
	if err != nil {
		return fmt.Errorf("encode replica sets: %w", err)
	}
	if err := s.q.UpsertSetting(ctx, dbgen.UpsertSettingParams{Key: settingReplicaSets, Value: b}); err != nil {
		return fmt.Errorf("store replica sets: %w", err)
	}
	return nil
}
