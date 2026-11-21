package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/jobs"
)

// storageJobs gives the storage configuration service the job runner and
// the git journal (storagecfg.Jobs).
type storageJobs struct {
	runner *jobs.Runner
	git    *gitstore.Tier
	log    *slog.Logger
}

func (j storageJobs) EnqueueReplicaSweep(ctx context.Context) error {
	return j.runner.EnqueueReplicaSweep(ctx)
}

func (j storageJobs) EnqueueFullVerify(ctx context.Context, limit int32) error {
	return j.runner.EnqueueFullVerify(ctx, limit)
}

func (j storageJobs) MirrorGitManifests() {
	if j.git == nil || j.git.Store() == nil {
		return
	}
	store := j.git.Store()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
		defer cancel()
		n, err := store.MirrorManifests(ctx)
		if err != nil {
			j.log.Warn("copy git manifests to the replicas", "copied", n, "error", err)
			return
		}
		j.log.Info("copied git manifests to the replicas", "repositories", n)
	}()
}
