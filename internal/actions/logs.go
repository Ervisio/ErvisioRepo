package actions

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

const (
	// maxLogRowBytes truncates single log rows.
	maxLogRowBytes = 64 << 10
	// maxLogRowsPerUpdate bounds one UpdateLog.
	maxLogRowsPerUpdate = 5000
	// logPage is the read page of log rows.
	logPage = 1000
	// archiveGrace lets a runner flush the last rows of a job ended by the
	// server before its log is archived.
	archiveGrace = 30 * time.Second
)

// LogLine is one row of a job log.
type LogLine struct {
	Index   int64
	Time    time.Time
	Content string
}

// logRow is a row to append.
type logRow struct {
	at      time.Time
	content string
}

// masker hides the secret values of a running job.
func (s *Service) masker(jobID uuid.UUID) *strings.Replacer {
	v, ok := s.masks.Load(jobID)
	if !ok {
		return nil
	}
	vals, _ := v.([]string)
	pairs := make([]string, 0, 2*len(vals))
	for _, m := range vals {
		if m != "" {
			pairs = append(pairs, m, "***")
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	return strings.NewReplacer(pairs...)
}

// appendLog stores rows starting at index from and returns the length of
// the stored log. Rows the server already has are ignored; a gap (rows the
// server never received) is kept as is, so the runner never retries
// forever.
func (s *Service) appendLog(ctx context.Context, jobID uuid.UUID, from int64, rows []logRow) (int64, error) {
	if from < 0 {
		return 0, invalid("negative log index")
	}
	if len(rows) > maxLogRowsPerUpdate {
		return 0, invalid("at most %d log rows per update", maxLogRowsPerUpdate)
	}
	mask := s.masker(jobID)
	var length int64
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		job, err := q.GetActionsJobForUpdate(ctx, jobID)
		if err != nil {
			return fmt.Errorf("lock job: %w", err)
		}
		length = job.LogLength
		if job.LogKey != "" {
			// Already archived: late rows are dropped.
			length = max(length, from+int64(len(rows)))
			return nil
		}
		skip := max(job.LogLength-from, 0)
		if skip >= int64(len(rows)) {
			return nil
		}
		p := dbgen.InsertActionsLogRowsParams{JobID: jobID}
		for i, r := range rows[skip:] {
			c := r.content
			if mask != nil {
				c = mask.Replace(c)
			}
			p.Idxs = append(p.Idxs, from+skip+int64(i))
			at := r.at
			if at.IsZero() {
				at = s.now()
			}
			p.Ats = append(p.Ats, pgtype.Timestamptz{Time: at, Valid: true})
			p.Contents = append(p.Contents, truncate(strings.ToValidUTF8(c, "�"), maxLogRowBytes))
		}
		if err := q.InsertActionsLogRows(ctx, p); err != nil {
			return fmt.Errorf("insert log rows: %w", err)
		}
		length = from + int64(len(rows))
		if err := q.SetActionsJobLogLength(ctx, dbgen.SetActionsJobLogLengthParams{LogLength: length, ID: jobID}); err != nil {
			return fmt.Errorf("log length: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	s.notifyLog(ctx, jobID, length)
	return length, nil
}

func (s *Service) notifyLog(ctx context.Context, jobID uuid.UUID, length int64) {
	if err := s.q.NotifyActions(ctx, dbgen.NotifyActionsParams{Channel: logChannel,
		Payload: jobID.String() + ":" + strconv.FormatInt(length, 10)}); err != nil {
		s.log.Warn("notify log followers", "job_id", jobID, "err", err)
	}
}

// appendServerLog adds an error line written by the server to a job log.
func (s *Service) appendServerLog(ctx context.Context, jobID uuid.UUID, msg string) {
	job, err := s.q.GetActionsJob(ctx, jobID)
	if err != nil {
		s.log.Warn("server log line", "job_id", jobID, "err", err)
		return
	}
	if _, err := s.appendLog(ctx, jobID, job.LogLength, []logRow{{at: s.now(), content: "##[error]" + msg}}); err != nil {
		s.log.Warn("server log line", "job_id", jobID, "err", err)
	}
}

func logKey(job dbgen.ActionsJob) string {
	return "logs/" + job.RepoID.String() + "/" + job.RunID.String() + "/" + job.ID.String() + ".log.gz"
}

// archiveLog moves the log of a completed job from Postgres to storage as
// gzip text, one "<RFC 3339 time> <content>" line per row.
func (s *Service) archiveLog(ctx context.Context, jobID uuid.UUID) error {
	if s.logs == nil {
		return nil
	}
	job, err := s.q.GetActionsJob(ctx, jobID)
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get job: %w", err)
	}
	if job.Status != "completed" || job.LogKey != "" {
		// The sweeper archives jobs completed later.
		return nil
	}
	if job.CompletedAt.Valid && s.now().Sub(job.CompletedAt.Time) < archiveGrace {
		return river.JobSnooze(archiveGrace)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	n := 0
	for from := int64(0); ; {
		rows, err := s.q.ListActionsLogRows(ctx, dbgen.ListActionsLogRowsParams{JobID: jobID, FromIdx: from, Lim: logPage})
		if err != nil {
			return fmt.Errorf("read log rows: %w", err)
		}
		for _, r := range rows {
			line := strings.ReplaceAll(r.Content, "\n", " ")
			if _, err := fmt.Fprintf(zw, "%s %s\n", r.At.Time.UTC().Format(time.RFC3339Nano), line); err != nil {
				return fmt.Errorf("compress log: %w", err)
			}
			from = r.Idx + 1
			n++
		}
		if len(rows) < logPage {
			break
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("compress log: %w", err)
	}
	if n == 0 {
		return nil
	}
	sum := sha256.Sum256(buf.Bytes())
	key := logKey(job)
	if _, err := s.logs.Put(ctx, key, bytes.NewReader(buf.Bytes()), int64(buf.Len()), sum[:]); err != nil {
		return fmt.Errorf("store log: %w", err)
	}
	if err := s.q.SetActionsJobLogKey(ctx, dbgen.SetActionsJobLogKeyParams{LogKey: key, ID: jobID}); err != nil {
		return fmt.Errorf("set log key: %w", err)
	}
	if err := s.q.DeleteActionsLogRows(ctx, jobID); err != nil {
		return fmt.Errorf("delete log rows: %w", err)
	}
	return nil
}

// readLog returns up to limit rows from index from.
func (s *Service) readLog(ctx context.Context, job dbgen.ActionsJob, from int64, limit int) ([]LogLine, error) {
	if limit <= 0 || limit > logPage {
		limit = logPage
	}
	from = max(from, 0)
	if job.LogKey == "" {
		rows, err := s.q.ListActionsLogRows(ctx, dbgen.ListActionsLogRowsParams{JobID: job.ID, FromIdx: from, Lim: int32(limit)})
		if err != nil {
			return nil, fmt.Errorf("read log rows: %w", err)
		}
		out := make([]LogLine, 0, len(rows))
		for _, r := range rows {
			out = append(out, LogLine{Index: r.Idx, Time: r.At.Time, Content: r.Content})
		}
		return out, nil
	}
	rc, err := s.archived(ctx, job)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	var out []LogLine
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 64<<10), 2*maxLogRowBytes)
	for i := int64(0); sc.Scan() && len(out) < limit; i++ {
		if i < from {
			continue
		}
		out = append(out, parseArchivedLine(i, sc.Text()))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read archived log: %w", err)
	}
	return out, nil
}

func parseArchivedLine(i int64, line string) LogLine {
	t, rest, ok := strings.Cut(line, " ")
	if ok {
		if at, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return LogLine{Index: i, Time: at, Content: rest}
		}
	}
	return LogLine{Index: i, Content: line}
}

// archived opens the decompressed archived log of a job.
func (s *Service) archived(ctx context.Context, job dbgen.ActionsJob) (io.ReadCloser, error) {
	if s.logs == nil {
		return nil, errors.New("actions: log storage is not configured")
	}
	r, _, err := s.logs.Get(ctx, job.LogKey, nil)
	if err != nil {
		return nil, fmt.Errorf("open archived log: %w", err)
	}
	zr, err := gzip.NewReader(r)
	if err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("open archived log: %w", err)
	}
	return &gzipReadCloser{zr: zr, r: r}, nil
}

type gzipReadCloser struct {
	zr *gzip.Reader
	r  io.ReadCloser
}

func (g *gzipReadCloser) Read(p []byte) (int, error) { return g.zr.Read(p) }

func (g *gzipReadCloser) Close() error { return errors.Join(g.zr.Close(), g.r.Close()) }

// rawLog streams the whole log of a job as text.
func (s *Service) rawLog(ctx context.Context, job dbgen.ActionsJob) (io.ReadCloser, error) {
	if job.LogKey != "" {
		return s.archived(ctx, job)
	}
	pr, pw := io.Pipe()
	go func() {
		w := bufio.NewWriter(pw)
		for from := int64(0); ; {
			rows, err := s.q.ListActionsLogRows(ctx, dbgen.ListActionsLogRowsParams{JobID: job.ID, FromIdx: from, Lim: logPage})
			if err != nil {
				pw.CloseWithError(fmt.Errorf("read log rows: %w", err))
				return
			}
			for _, r := range rows {
				if _, err := fmt.Fprintf(w, "%s %s\n", r.At.Time.UTC().Format(time.RFC3339Nano), r.Content); err != nil {
					pw.CloseWithError(err)
					return
				}
				from = r.Idx + 1
			}
			if len(rows) < logPage {
				break
			}
		}
		pw.CloseWithError(w.Flush())
	}()
	return pr, nil
}
