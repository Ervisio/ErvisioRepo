package quota

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

func TestStatusCheck(t *testing.T) {
	s := Status{MaxRepos: 2, MaxStorage: 100, Repos: 1, StorageBytes: 60}
	cases := []struct {
		d    Delta
		want Resource
	}{
		{Delta{Repos: 1}, ""},
		{Delta{Repos: 2}, Repos},
		{Delta{Bytes: 40}, ""},
		{Delta{Bytes: 41}, Storage},
		{Delta{Repos: -5, Bytes: -1000}, ""},
	}
	for _, c := range cases {
		err := s.Check(c.d)
		var e *ExceededError
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%+v: unexpected %v", c.d, err)
		case c.want != "" && (!errors.As(err, &e) || e.Resource != c.want):
			t.Errorf("%+v: got %v, want %s", c.d, err, c.want)
		}
	}
	u := Status{MaxRepos: Unlimited, MaxStorage: Unlimited, Repos: 1 << 30}
	if err := u.Check(Delta{Repos: 1 << 20, Bytes: 1 << 60}); err != nil {
		t.Fatal(err)
	}
	if !IsExceeded(s.Check(Delta{Repos: 5})) || IsExceeded(errors.New("x")) {
		t.Fatal("IsExceeded")
	}
}

type fakeQ struct {
	row      dbgen.GetEffectiveQuotaRow
	err      error
	reserved int64
	got      []dbgen.ReserveUsageParams
}

func (f *fakeQ) GetEffectiveQuota(context.Context, uuid.UUID) (dbgen.GetEffectiveQuotaRow, error) {
	return f.row, f.err
}
func (f *fakeQ) EnsureUsageCounter(context.Context, uuid.UUID) error { return nil }
func (f *fakeQ) ReserveUsage(_ context.Context, a dbgen.ReserveUsageParams) (int64, error) {
	f.got = append(f.got, a)
	return f.reserved, nil
}

func TestReserveFake(t *testing.T) {
	ctx := context.Background()
	f := &fakeQ{row: dbgen.GetEffectiveQuotaRow{MaxRepos: 1, MaxStorageBytes: -1, Repos: 1}}
	if err := Reserve(ctx, f, uuid.New(), Delta{Repos: 1}); !IsExceeded(err) {
		t.Fatalf("got %v", err)
	}
	f.reserved = 1
	if err := Reserve(ctx, f, uuid.New(), Delta{Repos: 1}); err != nil {
		t.Fatal(err)
	}
	if err := Release(ctx, f, uuid.New(), Delta{Repos: 3, Bytes: 7}); err != nil {
		t.Fatal(err)
	}
	if last := f.got[len(f.got)-1]; last.Repos != -3 || last.Bytes != -7 {
		t.Fatalf("release %+v", last)
	}
	f.err = pgx.ErrNoRows
	if _, err := Get(ctx, f, uuid.New()); !errors.Is(err, ErrNoNamespace) {
		t.Fatal(err)
	}
}
