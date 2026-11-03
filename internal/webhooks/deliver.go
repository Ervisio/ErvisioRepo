package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/jobs"
)

const (
	userAgent = "ErvisioRepo-Hookshot/1"
	// maxResponseBody is how much of a response body is kept.
	maxResponseBody = 32 << 10
	// maxResponseHeaders bounds the response headers kept.
	maxResponseHeaders = 64
	// Retention of the delivery history.
	deliveryRetention = 30 * 24 * time.Hour
	pruneInterval     = 24 * time.Hour
	// Backoff of the retries: 30s, 1m, 2m, ... capped at one hour.
	retryBase = 30 * time.Second
	retryMax  = time.Hour
)

// DeliverArgs sends one delivery.
type DeliverArgs struct {
	DeliveryID  uuid.UUID `json:"delivery_id"`
	MaxAttempts int       `json:"max_attempts"`
}

// Kind implements river.JobArgs.
func (DeliverArgs) Kind() string { return "webhook_deliver" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (a DeliverArgs) InsertOpts() river.InsertOpts {
	n := a.MaxAttempts
	if n <= 0 {
		n = DefaultMaxAttempts
	}
	return river.InsertOpts{Queue: river.QueueDefault, MaxAttempts: n, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// PruneArgs deletes the deliveries older than the retention.
type PruneArgs struct{}

// Kind implements river.JobArgs.
func (PruneArgs) Kind() string { return "webhook_prune" }

// JobsOption registers the delivery and prune workers.
func (s *Service) JobsOption() jobs.Option {
	return func(r *jobs.Registry) {
		river.AddWorker(r.Workers, &deliverWorker{s: s})
		river.AddWorker(r.Workers, &pruneWorker{s: s})
		r.Periodic = append(r.Periodic, river.NewPeriodicJob(river.PeriodicInterval(pruneInterval),
			func() (river.JobArgs, *river.InsertOpts) { return PruneArgs{}, nil }, nil))
	}
}

type deliverWorker struct {
	river.WorkerDefaults[DeliverArgs]
	s *Service
}

// NextRetry backs off exponentially from retryBase up to retryMax.
func (w *deliverWorker) NextRetry(job *river.Job[DeliverArgs]) time.Time {
	d := retryBase
	for i := 1; i < job.Attempt && d < retryMax; i++ {
		d *= 2
	}
	return time.Now().Add(min(d, retryMax))
}

func (w *deliverWorker) Work(ctx context.Context, job *river.Job[DeliverArgs]) error {
	return w.s.deliver(ctx, job.Args.DeliveryID, job.Attempt >= job.MaxAttempts)
}

type pruneWorker struct {
	river.WorkerDefaults[PruneArgs]
	s *Service
}

func (w *pruneWorker) Work(ctx context.Context, _ *river.Job[PruneArgs]) error {
	n, err := w.s.q.PruneWebhookDeliveries(ctx, pgtype.Timestamptz{Time: time.Now().Add(-deliveryRetention), Valid: true})
	if err != nil {
		return fmt.Errorf("prune webhook deliveries: %w", err)
	}
	if n > 0 {
		w.s.o.Log.InfoContext(ctx, "webhook deliveries pruned", "count", n)
	}
	return nil
}

// errRetry makes River retry a delivery; the message carries no secret.
type errRetry struct{ msg string }

func (e errRetry) Error() string { return e.msg }

// deliver makes one attempt. It returns an error only to be retried: on the
// final attempt, or for a failure that a retry cannot fix, it records the
// failure and returns nil.
func (s *Service) deliver(ctx context.Context, id uuid.UUID, final bool) error {
	d, err := s.q.GetWebhookDelivery(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get delivery: %w", err)
	}
	if d.Status != StatusPending {
		return nil // done: a redelivery is a new delivery
	}
	h, err := s.q.GetWebhook(ctx, d.WebhookID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get webhook: %w", err)
	}
	rec := dbgen.RecordWebhookAttemptParams{ID: d.ID, Url: h.Url, RequestHeaders: []byte("{}"), ResponseHeaders: []byte("{}")}
	var secret []byte
	if len(h.Secret) > 0 {
		secret, err = s.seal.open(h.Secret, h.ID)
		if err != nil {
			rec.Status, rec.Error = StatusFailed, err.Error()
			s.o.Log.WarnContext(ctx, "webhook secret cannot be opened", "webhook_id", h.ID, "delivery_id", d.ID)
			return s.record(ctx, rec)
		}
	}
	body, ctype := d.Payload, "application/json"
	if h.ContentType == ContentForm {
		body, ctype = []byte("payload="+url.QueryEscape(string(d.Payload))), "application/x-www-form-urlencoded"
	}
	prefix := "X-GitHub-"
	sigHeader := "X-Hub-Signature-256"
	if h.Format == FormatErvisio {
		prefix, sigHeader = "X-Ervisio-", "X-Ervisio-Signature-256"
	}
	headers := map[string]string{
		"Content-Type":      ctype,
		"User-Agent":        userAgent,
		prefix + "Event":    d.Event,
		prefix + "Delivery": d.Guid.String(),
		prefix + "Hook-ID":  h.ID.String(),
	}
	if len(secret) > 0 {
		mac := hmac.New(sha256.New, secret)
		mac.Write(body)
		headers[sigHeader] = "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}
	rec.RequestHeaders = encodeHeaders(headers)

	start := time.Now()
	status, code, respHeaders, respBody, sendErr := s.send(ctx, h.Url, headers, body)
	rec.DurationMs = int32(min(time.Since(start).Milliseconds(), 1<<31-1)) //nolint:gosec // bounded
	rec.StatusCode = int32(code)                                           //nolint:gosec // an HTTP status
	rec.ResponseHeaders = encodeHeaders(respHeaders)
	rec.ResponseBody = respBody
	switch {
	case sendErr == nil && status:
		rec.Status = StatusOK
	case final:
		rec.Status = StatusFailed
	default:
		rec.Status = StatusPending
	}
	if sendErr != nil {
		rec.Error = sanitize(sendErr.Error(), 1024)
	} else if !status {
		rec.Error = fmt.Sprintf("unexpected status %d", code)
	}
	if err := s.record(ctx, rec); err != nil {
		return err
	}
	if rec.Status == StatusPending {
		return errRetry{msg: "webhook delivery failed: " + rec.Error}
	}
	if rec.Status == StatusFailed {
		s.o.Log.InfoContext(ctx, "webhook delivery failed", "webhook_id", h.ID, "delivery_id", d.ID,
			"status_code", code, "error", rec.Error)
	}
	return nil
}

func (s *Service) record(ctx context.Context, rec dbgen.RecordWebhookAttemptParams) error {
	if err := s.q.RecordWebhookAttempt(ctx, rec); err != nil {
		return fmt.Errorf("record webhook attempt: %w", err)
	}
	return nil
}

// send posts one request and reads a bounded part of the response.
func (s *Service) send(ctx context.Context, target string, headers map[string]string, body []byte) (
	ok bool, code int, respHeaders map[string]string, respBody string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return false, 0, nil, "", fmt.Errorf("build request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the URL is shown next to the delivery already
		}
		return false, 0, nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	respHeaders = map[string]string{}
	keys := make([]string, 0, len(resp.Header))
	for k := range resp.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys[:min(len(keys), maxResponseHeaders)] {
		respHeaders[k] = sanitize(strings.Join(resp.Header.Values(k), ", "), 1024)
	}
	respBody = sanitize(string(b), maxResponseBody)
	ok = resp.StatusCode >= 200 && resp.StatusCode < 300
	if readErr != nil && ok {
		return false, resp.StatusCode, respHeaders, respBody, fmt.Errorf("read response: %w", readErr)
	}
	return ok, resp.StatusCode, respHeaders, respBody, nil
}

// sanitize makes s storable as text: valid UTF-8, no NUL, at most n bytes.
func sanitize(s string, n int) string {
	s = strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "�")
	if len(s) <= n {
		return s
	}
	s = s[:n]
	return strings.ToValidUTF8(s, "")
}

func encodeHeaders(h map[string]string) []byte {
	if len(h) == 0 {
		return []byte("{}")
	}
	b, err := json.Marshal(h)
	if err != nil {
		return []byte("{}")
	}
	return b
}

func decodeHeaders(b []byte) map[string]string {
	out := map[string]string{}
	if len(b) > 0 {
		_ = json.Unmarshal(b, &out)
	}
	return out
}
