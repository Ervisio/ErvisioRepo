// Package s3 implements blob.Store on any S3-compatible object storage
// (Hetzner Object Storage, AWS S3, Cloudflare R2, MinIO).
//
// Objects up to MultipartThreshold (64 MiB by default) are sent with a single
// PutObject; larger or unknown-size objects use a multipart upload that is
// completed only after the whole body has been read and verified, and aborted
// otherwise, so a failed upload never leaves a visible object.
package s3

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// Defaults for Options.
const (
	DefaultMultipartThreshold = 64 << 20
	DefaultPartSize           = 16 << 20
	MinPartSize               = 5 << 20
	maxParts                  = 10_000
)

// Options configures a Store.
type Options struct {
	Name      string // target name
	Endpoint  string // e.g. https://fsn1.your-objectstorage.com; empty for AWS
	Region    string // default us-east-1
	Bucket    string
	Prefix    string // optional key prefix inside the bucket, e.g. "erepo/"
	PathStyle bool

	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string

	MultipartThreshold int64 // default DefaultMultipartThreshold
	PartSize           int64 // default DefaultPartSize, at least MinPartSize
	ListPageSize       int32 // default 1000
	HTTPClient         *http.Client
}

// Store is a blob.Store on an S3 bucket.
type Store struct {
	opts    Options
	client  *s3.Client
	presign *s3.PresignClient
}

var (
	_ blob.Store             = (*Store)(nil)
	_ blob.ConditionalPutter = (*Store)(nil)
	_ blob.Presigner         = (*Store)(nil)
)

// New returns a Store. It does not contact the server.
func New(o Options) (*Store, error) {
	if o.Bucket == "" {
		return nil, fmt.Errorf("s3 %s: bucket is required", o.Name)
	}
	if o.Prefix != "" {
		if err := blob.ValidatePrefix(o.Prefix); err != nil || !strings.HasSuffix(o.Prefix, "/") {
			return nil, fmt.Errorf("s3 %s: prefix %q must be a valid key ending with '/'", o.Name, o.Prefix)
		}
	}
	if o.Region == "" {
		o.Region = "us-east-1"
	}
	if o.MultipartThreshold <= 0 {
		o.MultipartThreshold = DefaultMultipartThreshold
	}
	if o.PartSize <= 0 {
		o.PartSize = DefaultPartSize
	}
	if o.PartSize < MinPartSize {
		return nil, fmt.Errorf("s3 %s: part size must be at least %d", o.Name, MinPartSize)
	}
	if o.ListPageSize <= 0 || o.ListPageSize > 1000 {
		o.ListPageSize = 1000
	}
	so := s3.Options{
		Region:       o.Region,
		UsePathStyle: o.PathStyle,
		// Many S3-compatible services reject the SDK's default trailing
		// checksums; integrity is checked by this driver instead.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	if o.Endpoint != "" {
		so.BaseEndpoint = aws.String(o.Endpoint)
	}
	if o.AccessKeyID != "" {
		so.Credentials = aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(o.AccessKeyID, o.SecretAccessKey, o.SessionToken))
	}
	if o.HTTPClient != nil {
		so.HTTPClient = o.HTTPClient
	}
	client := s3.New(so)
	return &Store{opts: o, client: client, presign: s3.NewPresignClient(client)}, nil
}

// ParseCredentials reads a credentials file with "access_key_id" and
// "secret_access_key" (the aws_ prefixed spellings are accepted too) as
// key = value lines; '#' starts a comment and [sections] are ignored.
func ParseCredentials(data []byte) (accessKeyID, secretAccessKey string, err error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' || line[0] == '[' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(k)), "aws_")
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "access_key_id":
			accessKeyID = v
		case "secret_access_key":
			secretAccessKey = v
		}
	}
	if accessKeyID == "" || secretAccessKey == "" {
		return "", "", errors.New("credentials file: access_key_id and secret_access_key are required")
	}
	return accessKeyID, secretAccessKey, nil
}

// Name implements blob.Store.
func (s *Store) Name() string { return s.opts.Name }

// Caps implements blob.Store.
func (s *Store) Caps() blob.Caps {
	return blob.Caps{Range: true, Presign: true, Multipart: true, AtomicRename: true, ConditionalPut: true}
}

func (s *Store) objKey(key string) *string { return aws.String(s.opts.Prefix + key) }

// Put implements blob.Store.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, o blob.PutOpts) (blob.Info, error) {
	return s.put(ctx, key, r, size, blob.Condition{}, o)
}

// PutConditional implements blob.ConditionalPutter with If-Match and
// If-None-Match.
func (s *Store) PutConditional(ctx context.Context, key string, r io.Reader, size int64, c blob.Condition, o blob.PutOpts) (blob.Info, error) {
	return s.put(ctx, key, r, size, c, o)
}

func condHeaders(c blob.Condition) (ifMatch, ifNoneMatch *string) {
	if c.IfMatch != "" {
		ifMatch = aws.String(c.IfMatch)
	}
	if c.IfNoneMatch {
		ifNoneMatch = aws.String("*")
	}
	return ifMatch, ifNoneMatch
}

func (s *Store) put(ctx context.Context, key string, r io.Reader, size int64, c blob.Condition, o blob.PutOpts) (blob.Info, error) {
	if err := blob.ValidateKey(key); err != nil {
		return blob.Info{}, err
	}
	if c.IfMatch != "" {
		// Some implementations (MinIO) accept If-Match on a missing object
		// instead of answering 404/412. Checking first closes that gap except
		// for a concurrent delete, which the journal manifest never does.
		if _, err := s.Stat(ctx, key); errors.Is(err, blob.ErrNotFound) {
			return blob.Info{}, fmt.Errorf("s3 %s: put %s: %w", s.opts.Name, key, blob.ErrPreconditionFailed)
		} else if err != nil {
			return blob.Info{}, err
		}
	}
	vr, err := blob.NewVerifyReader(ctx, r, size, o.SHA256)
	if err != nil {
		return blob.Info{}, err
	}
	if size > s.opts.MultipartThreshold {
		return s.putMultipart(ctx, key, vr, nil, size, c, o)
	}
	// Buffer up to the threshold: the SDK needs a seekable body to sign it,
	// and nothing is sent until the body has been read and verified.
	var buf bytes.Buffer
	if size > 0 {
		buf.Grow(int(size))
	}
	if _, err := buf.ReadFrom(io.LimitReader(vr, s.opts.MultipartThreshold+1)); err != nil {
		return blob.Info{}, s.mapErr("put", key, err)
	}
	if int64(buf.Len()) > s.opts.MultipartThreshold {
		return s.putMultipart(ctx, key, vr, buf.Bytes(), size, c, o)
	}
	ifMatch, ifNoneMatch := condHeaders(c)
	in := &s3.PutObjectInput{
		Bucket:        aws.String(s.opts.Bucket),
		Key:           s.objKey(key),
		Body:          bytes.NewReader(buf.Bytes()),
		ContentLength: aws.Int64(int64(buf.Len())),
		IfMatch:       ifMatch,
		IfNoneMatch:   ifNoneMatch,
	}
	if o.ContentType != "" {
		in.ContentType = aws.String(o.ContentType)
	}
	out, err := s.client.PutObject(ctx, in)
	if err != nil {
		return blob.Info{}, s.mapCondErr("put", key, err, c)
	}
	return blob.Info{Key: key, Size: int64(buf.Len()), ETag: aws.ToString(out.ETag), ModTime: time.Now()}, nil
}

func (s *Store) partSize(size int64) int64 {
	ps := s.opts.PartSize
	if size < 0 {
		return max(ps, DefaultMultipartThreshold)
	}
	if need := (size + maxParts - 1) / maxParts; need > ps {
		const mib = 1 << 20
		ps = (need + mib - 1) / mib * mib
	}
	return ps
}

// putMultipart uploads head followed by the rest of vr in parts.
func (s *Store) putMultipart(ctx context.Context, key string, vr *blob.VerifyReader, head []byte, size int64, c blob.Condition, o blob.PutOpts) (info blob.Info, err error) {
	create := &s3.CreateMultipartUploadInput{Bucket: aws.String(s.opts.Bucket), Key: s.objKey(key)}
	if o.ContentType != "" {
		create.ContentType = aws.String(o.ContentType)
	}
	cr, err := s.client.CreateMultipartUpload(ctx, create)
	if err != nil {
		return blob.Info{}, s.mapErr("put: create multipart", key, err)
	}
	uploadID := cr.UploadId
	defer func() {
		if err != nil {
			actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			_, _ = s.client.AbortMultipartUpload(actx, &s3.AbortMultipartUploadInput{
				Bucket: aws.String(s.opts.Bucket), Key: s.objKey(key), UploadId: uploadID,
			})
		}
	}()

	src := io.MultiReader(bytes.NewReader(head), vr)
	buf := make([]byte, s.partSize(size))
	var parts []types.CompletedPart
	var total int64
	for num := int32(1); ; num++ {
		n, rerr := fill(src, buf)
		if rerr != nil && rerr != io.EOF {
			return blob.Info{}, s.mapErr("put: read", key, rerr)
		}
		if n > 0 || num == 1 {
			if num > maxParts {
				return blob.Info{}, fmt.Errorf("s3 %s: put %s: more than %d parts", s.opts.Name, key, maxParts)
			}
			up, err := s.client.UploadPart(ctx, &s3.UploadPartInput{
				Bucket: aws.String(s.opts.Bucket), Key: s.objKey(key), UploadId: uploadID,
				PartNumber: aws.Int32(num), Body: bytes.NewReader(buf[:n]), ContentLength: aws.Int64(int64(n)),
			})
			if err != nil {
				return blob.Info{}, s.mapErr("put: upload part", key, err)
			}
			parts = append(parts, types.CompletedPart{ETag: up.ETag, PartNumber: aws.Int32(num)})
			total += int64(n)
		}
		if rerr == io.EOF {
			break
		}
	}
	ifMatch, ifNoneMatch := condHeaders(c)
	out, err := s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: aws.String(s.opts.Bucket), Key: s.objKey(key), UploadId: uploadID,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
		IfMatch:         ifMatch,
		IfNoneMatch:     ifNoneMatch,
	})
	if err != nil {
		return blob.Info{}, s.mapCondErr("put: complete multipart", key, err, c)
	}
	return blob.Info{Key: key, Size: total, ETag: aws.ToString(out.ETag), ModTime: time.Now()}, nil
}

// fill reads into buf until it is full or r fails. It returns io.EOF only
// when r reported io.EOF (and thus, for a VerifyReader, verified the body).
func fill(r io.Reader, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := r.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// Get implements blob.Store.
func (s *Store) Get(ctx context.Context, key string, rng *blob.Range) (io.ReadCloser, blob.Info, error) {
	if err := blob.ValidateKey(key); err != nil {
		return nil, blob.Info{}, err
	}
	in := &s3.GetObjectInput{Bucket: aws.String(s.opts.Bucket), Key: s.objKey(key)}
	if rng != nil {
		if rng.Offset < 0 {
			return nil, blob.Info{}, fmt.Errorf("%w: negative offset", blob.ErrInvalidRange)
		}
		if rng.Length == 0 {
			return s.emptyRange(ctx, key, rng)
		}
		if rng.Length < 0 {
			in.Range = aws.String(fmt.Sprintf("bytes=%d-", rng.Offset))
		} else {
			in.Range = aws.String(fmt.Sprintf("bytes=%d-%d", rng.Offset, rng.Offset+rng.Length-1))
		}
	}
	out, err := s.client.GetObject(ctx, in)
	if err != nil {
		err = s.mapErr("get", key, err)
		if rng != nil && errors.Is(err, blob.ErrInvalidRange) {
			return s.emptyRange(ctx, key, rng)
		}
		return nil, blob.Info{}, err
	}
	info := blob.Info{Key: key, Size: aws.ToInt64(out.ContentLength), ETag: aws.ToString(out.ETag), ModTime: aws.ToTime(out.LastModified)}
	if cr := aws.ToString(out.ContentRange); cr != "" {
		if i := strings.LastIndexByte(cr, '/'); i >= 0 {
			if total, perr := strconv.ParseInt(cr[i+1:], 10, 64); perr == nil {
				info.Size = total
			}
		}
	}
	return &body{rc: out.Body, s: s, key: key}, info, nil
}

// emptyRange serves ranges that select no bytes, which S3 rejects with 416.
func (s *Store) emptyRange(ctx context.Context, key string, rng *blob.Range) (io.ReadCloser, blob.Info, error) {
	info, err := s.Stat(ctx, key)
	if err != nil {
		return nil, blob.Info{}, err
	}
	if _, _, err := blob.ResolveRange(rng, info.Size); err != nil {
		return nil, blob.Info{}, err
	}
	return io.NopCloser(bytes.NewReader(nil)), info, nil
}

// body marks read failures of a response body as transient.
type body struct {
	rc  io.ReadCloser
	s   *Store
	key string
}

func (b *body) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if err != nil && err != io.EOF {
		err = blob.MarkTransient(fmt.Errorf("s3 %s: read %s: %w", b.s.opts.Name, b.key, err))
	}
	return n, err
}

func (b *body) Close() error { return b.rc.Close() }

// Stat implements blob.Store.
func (s *Store) Stat(ctx context.Context, key string) (blob.Info, error) {
	if err := blob.ValidateKey(key); err != nil {
		return blob.Info{}, err
	}
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.opts.Bucket), Key: s.objKey(key)})
	if err != nil {
		return blob.Info{}, s.mapErr("stat", key, err)
	}
	return blob.Info{Key: key, Size: aws.ToInt64(out.ContentLength), ETag: aws.ToString(out.ETag), ModTime: aws.ToTime(out.LastModified)}, nil
}

// Delete implements blob.Store.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := blob.ValidateKey(key); err != nil {
		return err
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.opts.Bucket), Key: s.objKey(key)})
	if err != nil {
		if err = s.mapErr("delete", key, err); errors.Is(err, blob.ErrNotFound) {
			return nil
		}
		return err
	}
	return nil
}

// List implements blob.Store. Keys that are not valid blob keys (written by
// other tools) are skipped.
func (s *Store) List(ctx context.Context, prefix string, fn func(blob.Info) error) error {
	if err := blob.ValidatePrefix(prefix); err != nil {
		return err
	}
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket:  aws.String(s.opts.Bucket),
		Prefix:  aws.String(s.opts.Prefix + prefix),
		MaxKeys: aws.Int32(s.opts.ListPageSize),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return s.mapErr("list", prefix, err)
		}
		for _, obj := range page.Contents {
			key := strings.TrimPrefix(aws.ToString(obj.Key), s.opts.Prefix)
			if blob.ValidateKey(key) != nil {
				continue
			}
			info := blob.Info{Key: key, Size: aws.ToInt64(obj.Size), ETag: aws.ToString(obj.ETag), ModTime: aws.ToTime(obj.LastModified)}
			if err := fn(info); err != nil {
				return err
			}
		}
	}
	return nil
}

// PresignGet implements blob.Presigner.
func (s *Store) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if err := blob.ValidateKey(key); err != nil {
		return "", err
	}
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.opts.Bucket), Key: s.objKey(key)},
		s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("s3 %s: presign get %s: %w", s.opts.Name, key, err)
	}
	return req.URL, nil
}

// PresignPut implements blob.Presigner. With sha256 set the client must send
// the returned x-amz-checksum-sha256 header and the server rejects any other
// content.
func (s *Store) PresignPut(ctx context.Context, key string, size int64, sha256Hex string, ttl time.Duration) (string, http.Header, error) {
	if err := blob.ValidateKey(key); err != nil {
		return "", nil, err
	}
	in := &s3.PutObjectInput{Bucket: aws.String(s.opts.Bucket), Key: s.objKey(key), ContentLength: aws.Int64(size)}
	if sha256Hex != "" {
		sum, err := hex.DecodeString(sha256Hex)
		if err != nil || len(sum) != 32 {
			return "", nil, fmt.Errorf("%w: malformed sha256 %q", blob.ErrChecksumMismatch, sha256Hex)
		}
		in.ChecksumSHA256 = aws.String(base64.StdEncoding.EncodeToString(sum))
	}
	req, err := s.presign.PresignPutObject(ctx, in, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", nil, fmt.Errorf("s3 %s: presign put %s: %w", s.opts.Name, key, err)
	}
	hdr := req.SignedHeader.Clone()
	hdr.Del("Host")
	return req.URL, hdr, nil
}

func (s *Store) mapCondErr(op, key string, err error, c blob.Condition) error {
	err = s.mapErr(op, key, err)
	// If-Match on a missing object answers 404 on some implementations.
	if c.IfMatch != "" && errors.Is(err, blob.ErrNotFound) {
		return fmt.Errorf("s3 %s: %s %s: %w", s.opts.Name, op, key, blob.ErrPreconditionFailed)
	}
	return err
}

// mapErr translates SDK errors into blob sentinels and marks the transient ones.
func (s *Store) mapErr(op, key string, err error) error {
	wrap := func(sentinel error) error {
		return fmt.Errorf("s3 %s: %s %s: %w (%v)", s.opts.Name, op, key, sentinel, err)
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "NoSuchKey", "NotFound", "NoSuchUpload":
			return wrap(blob.ErrNotFound)
		case "PreconditionFailed", "ConditionalRequestConflict":
			return wrap(blob.ErrPreconditionFailed)
		case "InvalidRange":
			return wrap(blob.ErrInvalidRange)
		case "SlowDown", "Throttling", "ThrottlingException", "RequestTimeout", "InternalError", "ServiceUnavailable":
			return blob.MarkTransient(fmt.Errorf("s3 %s: %s %s: %w", s.opts.Name, op, key, err))
		}
	}
	var re *smithyhttp.ResponseError
	if errors.As(err, &re) {
		switch code := re.HTTPStatusCode(); {
		case code == http.StatusNotFound:
			return wrap(blob.ErrNotFound)
		case code == http.StatusPreconditionFailed || code == http.StatusConflict:
			return wrap(blob.ErrPreconditionFailed)
		case code == http.StatusRequestedRangeNotSatisfiable:
			return wrap(blob.ErrInvalidRange)
		case code == http.StatusTooManyRequests || code >= 500:
			return blob.MarkTransient(fmt.Errorf("s3 %s: %s %s: %w", s.opts.Name, op, key, err))
		}
	}
	return fmt.Errorf("s3 %s: %s %s: %w", s.opts.Name, op, key, err)
}
