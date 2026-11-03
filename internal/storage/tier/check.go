package tier

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// TestPrefix is where connection tests write their probe object.
const TestPrefix = "erepo-storage-test/"

// testObjectSize is the size of the probe object.
const testObjectSize = 64 << 10

// CheckStep is the outcome of one step of a connection test.
type CheckStep struct {
	Name     string // put, stat, get, range, list, delete
	OK       bool
	Duration time.Duration
	Error    string // empty when OK; never contains credentials
}

// CheckResult is the outcome of a connection test.
type CheckResult struct {
	Target   string
	OK       bool
	Duration time.Duration
	Steps    []CheckStep
}

// CheckStore runs a write/read/list/delete round trip on s with a random
// probe object under TestPrefix, and always tries to delete the probe.
func CheckStore(ctx context.Context, s blob.Store) CheckResult {
	res := CheckResult{Target: s.Name(), OK: true}
	start := time.Now()

	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	key := TestPrefix + hex.EncodeToString(rnd[:])
	data := make([]byte, testObjectSize)
	_, _ = rand.Read(data)
	sum := sha256.Sum256(data)

	step := func(name string, fn func() error) bool {
		t0 := time.Now()
		err := fn()
		st := CheckStep{Name: name, OK: err == nil, Duration: time.Since(t0)}
		if err != nil {
			st.Error = err.Error()
			res.OK = false
		}
		res.Steps = append(res.Steps, st)
		return err == nil
	}

	put := step("put", func() error {
		_, err := s.Put(ctx, key, bytes.NewReader(data), int64(len(data)), blob.PutOpts{SHA256: hex.EncodeToString(sum[:])})
		return err
	})
	if put {
		step("stat", func() error {
			info, err := s.Stat(ctx, key)
			if err == nil && info.Size != int64(len(data)) {
				err = fmt.Errorf("size is %d, expected %d", info.Size, len(data))
			}
			return err
		})
		step("get", func() error {
			rc, _, err := s.Get(ctx, key, nil)
			if err != nil {
				return err
			}
			defer rc.Close()
			_, err = io.Copy(io.Discard, newVerifyReader(rc, int64(len(data)), sum[:]))
			return err
		})
		if s.Caps().Range {
			step("range", func() error {
				rc, _, err := s.Get(ctx, key, &blob.Range{Offset: 100, Length: 10})
				if err != nil {
					return err
				}
				defer rc.Close()
				got, err := io.ReadAll(rc)
				if err == nil && !bytes.Equal(got, data[100:110]) {
					err = errors.New("range returned wrong bytes")
				}
				return err
			})
		}
		step("list", func() error {
			found := false
			err := s.List(ctx, TestPrefix, func(i blob.Info) error {
				if i.Key == key {
					found = true
				}
				return nil
			})
			if err == nil && !found {
				err = errors.New("probe object not listed")
			}
			return err
		})
	}
	// Always clean up, even when the put reported an error.
	step("delete", func() error {
		if err := s.Delete(context.WithoutCancel(ctx), key); err != nil {
			return err
		}
		if !put {
			return nil
		}
		if _, err := s.Stat(ctx, key); !errors.Is(err, blob.ErrNotFound) {
			return fmt.Errorf("object still present after delete (stat: %v)", err)
		}
		return nil
	})
	res.Duration = time.Since(start)
	return res
}

// ErrUnknownTarget is returned by CheckTarget for a name not in the config.
var ErrUnknownTarget = errors.New("storage: unknown target")

// CheckTarget runs CheckStore on the named target.
func (t *Tier) CheckTarget(ctx context.Context, name string) (CheckResult, error) {
	tgt, ok := t.Target(name)
	if !ok {
		return CheckResult{}, fmt.Errorf("%w %q", ErrUnknownTarget, name)
	}
	return CheckStore(ctx, tgt.Store), nil
}
