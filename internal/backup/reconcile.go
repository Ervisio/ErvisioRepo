package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// ReconcileReport sums up a ReconcileRepos run.
type ReconcileReport struct {
	Repos     int         // repositories in the restored database
	Reindexed int         // journal objects missing from storage_objects, re-added
	Degraded  []uuid.UUID // repositories without a usable remote journal
	Orphans   []uuid.UUID // manifests on the remote with no repository row
}

// ReconcileRepos aligns a freshly restored database with the remote git
// journal, which may be newer than the dump (PLAN §4.5): every repository
// goes cold, its manifest_seq is taken from the remote manifest, the
// journal objects the dump does not know are indexed again (after checking
// their digest), and a repository whose manifest is missing or invalid goes
// to the degraded state. Nothing else may run on the database meanwhile.
func ReconcileRepos(ctx context.Context, pool *pgxpool.Pool, store *gitstore.Store, log *slog.Logger) (ReconcileReport, error) {
	q := dbgen.New(pool)
	var rep ReconcileReport
	if _, err := q.MarkAllReposCold(ctx); err != nil {
		return rep, fmt.Errorf("mark repositories cold: %w", err)
	}
	rows, err := q.ListRepoTiering(ctx)
	if err != nil {
		return rep, fmt.Errorf("list repositories: %w", err)
	}
	rep.Repos = len(rows)
	known := make(map[uuid.UUID]bool, len(rows))
	for _, r := range rows {
		known[r.ID] = true
		n, err := reconcileRepo(ctx, q, store, r.ID)
		rep.Reindexed += n
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
		log.WarnContext(ctx, "repository degraded by restore", "repo", r.ID, "error", err)
		if err := q.ForceRepoStorageState(ctx, dbgen.ForceRepoStorageStateParams{ID: r.ID, State: gitstore.StateDegraded}); err != nil {
			return rep, fmt.Errorf("degrade %s: %w", r.ID, err)
		}
		rep.Degraded = append(rep.Degraded, r.ID)
	}
	class := store.Class()
	seen := map[uuid.UUID]bool{}
	err = class.Primary().Store.List(ctx, class.FullKey(""), func(i blob.Info) error {
		k, err := gitstore.ParseKey(strings.TrimPrefix(i.Key, class.FullKey("")))
		if err != nil || k.Kind != gitstore.KindManifest || known[k.Repo] || seen[k.Repo] {
			return nil //nolint:nilerr // not a journal manifest: ignored
		}
		seen[k.Repo] = true
		rep.Orphans = append(rep.Orphans, k.Repo)
		return nil
	})
	if err != nil {
		return rep, fmt.Errorf("list remote manifests: %w", err)
	}
	return rep, nil
}

func reconcileRepo(ctx context.Context, q *dbgen.Queries, store *gitstore.Store, id uuid.UUID) (int, error) {
	m, err := store.LoadManifest(ctx, id)
	if errors.Is(err, gitstore.ErrNoManifest) {
		// Never pushed to the journal: an empty repository is fine as is.
		return 0, q.ResetRepoManifestSeq(ctx, dbgen.ResetRepoManifestSeqParams{ID: id, Seq: 0})
	}
	if err != nil {
		return 0, err
	}
	n := 0
	index := func(key, sum string) error {
		added, err := reindex(ctx, q, store, key, sum)
		if added {
			n++
		}
		return err
	}
	for _, p := range m.Packs() {
		if err := index(p.Pack, p.SHA256); err != nil {
			return n, err
		}
		if err := index(p.Idx, p.IdxSHA256); err != nil {
			return n, err
		}
	}
	if m.RefsSeq > 0 {
		if err := index(gitstore.RefsKey(id, m.RefsSeq), ""); err != nil {
			return n, err
		}
	}
	if err := q.ResetRepoManifestSeq(ctx, dbgen.ResetRepoManifestSeqParams{ID: id, Seq: m.Seq()}); err != nil {
		return n, fmt.Errorf("reset manifest seq %s: %w", id, err)
	}
	return n, nil
}

// reindex adds the storage_objects row of a journal object the restored
// database does not know, reading it from the primary to compute its digest
// (checked against sum when set). Replicas are left to the replication job.
func reindex(ctx context.Context, q *dbgen.Queries, store *gitstore.Store, key, sum string) (bool, error) {
	class := store.Class()
	if _, err := class.Stat(ctx, key); err == nil {
		return false, nil
	} else if !errors.Is(err, blob.ErrNotFound) {
		return false, err
	}
	full := class.FullKey(key)
	rc, _, err := class.Primary().Store.Get(ctx, full, nil)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", key, err)
	}
	defer func() { _ = rc.Close() }()
	h := sha256.New()
	size, err := io.Copy(h, rc)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", key, err)
	}
	digest := h.Sum(nil)
	if sum != "" && !strings.EqualFold(hex.EncodeToString(digest), sum) {
		return false, fmt.Errorf("%s: digest differs from the manifest", key)
	}
	if _, err := q.UpsertStorageObject(ctx, dbgen.UpsertStorageObjectParams{
		Target: class.Primary().Name, Key: full, Policy: class.Name(), Size: size, Sha256: digest,
	}); err != nil {
		return false, fmt.Errorf("index %s: %w", key, err)
	}
	return true, nil
}
