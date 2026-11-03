package tier

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

func blobOpts() blob.PutOpts { return blob.PutOpts{} }

func TestVerifyReader(t *testing.T) {
	data := []byte("hello, world")
	sum := sha256.Sum256(data)
	wrong := sha256.Sum256([]byte("nope"))

	read := func(r io.Reader, size int64, want []byte) ([]byte, error) {
		return io.ReadAll(newVerifyReader(r, size, want))
	}
	if got, err := read(bytes.NewReader(data), 12, sum[:]); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("ok: %q %v", got, err)
	}
	if _, err := read(bytes.NewReader(data), 12, nil); err != nil {
		t.Fatalf("no digest: %v", err)
	}
	got, err := read(bytes.NewReader(data), 12, wrong[:])
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("mismatch: %v", err)
	}
	if len(got) == len(data) {
		t.Fatal("a mismatching stream must withhold its last bytes")
	}
	if _, err := read(bytes.NewReader(data[:5]), 12, nil); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short: %v", err)
	}
	if _, err := read(bytes.NewReader(data), 5, nil); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("long: %v", err)
	}
	empty := sha256.Sum256(nil)
	if _, err := read(bytes.NewReader(nil), 0, empty[:]); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if _, err := read(io.MultiReader(bytes.NewReader(data[:3]), &errReader{errors.New("boom")}), 12, nil); err == nil || err.Error() != "boom" {
		t.Fatalf("source error: %v", err)
	}
	v := newVerifyReader(iotest1(data), 12, sum[:])
	if _, err := io.ReadAll(v); err != nil || !v.Verified() || !bytes.Equal(v.Sum(), sum[:]) {
		t.Fatalf("byte-by-byte: %v", err)
	}
}

// iotest1 returns a reader that yields one byte per Read.
func iotest1(b []byte) io.Reader { return &oneByte{b: b} }

type oneByte struct{ b []byte }

func (o *oneByte) Read(p []byte) (int, error) {
	if len(o.b) == 0 {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = o.b[0]
	o.b = o.b[1:]
	return 1, nil
}

func FuzzVerifyReader(f *testing.F) {
	f.Add([]byte("abc"), int64(3), true)
	f.Add([]byte(""), int64(0), false)
	f.Add([]byte("abcdef"), int64(2), true)
	f.Fuzz(func(t *testing.T, data []byte, size int64, withSum bool) {
		if size < 0 || size > 1<<16 {
			return
		}
		var want []byte
		if withSum {
			s := sha256.Sum256(data)
			want = s[:]
		}
		got, err := io.ReadAll(newVerifyReader(bytes.NewReader(data), size, want))
		ok := int64(len(data)) == size
		if ok != (err == nil) {
			t.Fatalf("len %d size %d: err %v", len(data), size, err)
		}
		if err == nil && !bytes.Equal(got, data) {
			t.Fatal("data changed")
		}
		if int64(len(got)) >= size && size > 0 && err != nil {
			t.Fatal("a failing stream must not deliver size bytes")
		}
	})
}

func TestParseSHA256(t *testing.T) {
	s := strings.Repeat("ab", 32)
	if b, err := ParseSHA256(s); err != nil || len(b) != 32 {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "ab", strings.Repeat("zz", 32), s + "00"} {
		if _, err := ParseSHA256(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestWarnings(t *testing.T) {
	targets := []config.Target{{Name: "disk", Kind: "local"}, {Name: "box", Kind: "sftp"}, {Name: "s3", Kind: "s3"}}
	cases := []struct {
		name   string
		policy map[string]config.Policy
		want   []string
	}{
		{"none", nil, []string{WarnNotConfigured}},
		{"all local", map[string]config.Policy{
			"git": {Primary: "disk"}, "lfs": {Primary: "disk", Replicas: []string{"disk"}},
		}, []string{WarnLocalOnly}},
		{"one local", map[string]config.Policy{
			"git": {Primary: "disk", Replicas: []string{"box"}}, "logs": {Primary: "disk"},
		}, []string{WarnPolicyLocal + ":logs"}},
		{"all remote", map[string]config.Policy{
			"git": {Primary: "s3", Cache: "disk"}, "lfs": {Primary: "disk", Replicas: []string{"s3"}},
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, w := range Warnings(config.Storage{Targets: targets, Policy: c.policy}) {
				s := w.Code
				if w.Policy != "" {
					s += ":" + w.Policy
				}
				if w.Message == "" {
					t.Error("empty message")
				}
				got = append(got, s)
			}
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestStatus(t *testing.T) {
	e := newEnv(t, nil)
	data, sum := content(100, 1)
	put(t, e.class(t, "lfs"), "o", data, sum)
	st, err := e.tier.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Targets) != 3 || st.Targets[0].Name != "cache" || !st.Targets[1].Encrypted {
		t.Fatalf("targets = %+v", st.Targets)
	}
	if strings.Join(st.Targets[1].Roles, ",") != "primary:git,primary:lfs" || st.Targets[0].Roles[0] != "cache:lfs" {
		t.Fatalf("roles = %v / %v", st.Targets[1].Roles, st.Targets[0].Roles)
	}
	if len(st.Policies) != 2 || st.Policies[1].Name != "lfs" || st.Policies[1].Objects != 1 || st.Policies[1].PendingReplicas != 1 {
		t.Fatalf("policies = %+v", st.Policies)
	}
	if len(st.Caches) != 1 || st.Caches[0].Used != 100 || len(st.Warnings) != 0 {
		t.Fatalf("caches = %+v warnings = %+v", st.Caches, st.Warnings)
	}
}

func TestCheckStore(t *testing.T) {
	s := newMemStore("ok")
	res := CheckStore(context.Background(), s)
	if !res.OK || len(res.Steps) != 6 {
		t.Fatalf("result = %+v", res)
	}
	if s.len() != 0 {
		t.Fatal("probe left behind")
	}

	bad := newMemStore("bad")
	bad.setPutErr(func(string) error { return errors.New("permission denied") })
	res = CheckStore(context.Background(), bad)
	if res.OK || len(res.Steps) != 2 || res.Steps[0].Name != "put" || res.Steps[0].Error == "" || res.Steps[1].Name != "delete" {
		t.Fatalf("result = %+v", res)
	}

	corrupt := newMemStore("corrupt")
	corrupt.setGetErr(func(string) error { return errors.New("read timeout") })
	res = CheckStore(context.Background(), corrupt)
	if res.OK || corrupt.len() != 0 {
		t.Fatalf("result = %+v", res)
	}
}

func TestFactory(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	stores, err := BuildStores(ctx, config.Storage{Targets: []config.Target{{Name: "disk", Kind: "local", Path: dir}}}, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if stores["disk"] == nil || stores["disk"].Name() != "disk" {
		t.Fatalf("stores = %v", stores)
	}
	if res := CheckStore(ctx, stores["disk"]); !res.OK {
		t.Fatalf("local check: %+v", res)
	}
	CloseStores(stores)

	if _, err := BuildStores(ctx, config.Storage{Targets: []config.Target{{Name: "x", Kind: "carrier-pigeon"}}}, BuildOptions{}); !errors.Is(err, ErrNoDriver) {
		t.Fatalf("unknown kind: %v", err)
	}

	// Registered wrappers apply in order; encryption needs the master key.
	var order []string
	wrap := func(name string) WrapFunc {
		return func(s blob.Store, _ config.Target, _ BuildOptions) (blob.Store, error) {
			order = append(order, name)
			return s, nil
		}
	}
	RegisterDriver("fake", func(context.Context, config.Target, BuildOptions) (blob.Store, error) { return newMemStore("f"), nil })
	registryMu.Lock()
	oldR, oldC, oldM := wrapRetry, wrapCrypt, wrapMetered
	registryMu.Unlock()
	t.Cleanup(func() {
		registryMu.Lock()
		wrapRetry, wrapCrypt, wrapMetered = oldR, oldC, oldM
		delete(drivers, "fake")
		registryMu.Unlock()
	})
	RegisterWrappers(wrap("retry"), wrap("crypt"), wrap("metered"))
	tgt := config.Target{Name: "f", Kind: "fake", Encrypt: true}
	if _, err := BuildStore(ctx, tgt, BuildOptions{}); err == nil {
		t.Fatal("encryption without master key accepted")
	}
	order = nil
	if _, err := BuildStore(ctx, tgt, BuildOptions{MasterKey: make([]byte, 32)}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "retry,crypt,metered" {
		t.Fatalf("wrapper order = %v", order)
	}
}

func TestCheckTarget(t *testing.T) {
	e := newEnv(t, nil)
	if res, err := e.tier.CheckTarget(context.Background(), "cache"); err != nil || !res.OK || res.Target != "cache" {
		t.Fatalf("check = %+v, %v", res, err)
	}
	if _, err := e.tier.CheckTarget(context.Background(), "nope"); !errors.Is(err, ErrUnknownTarget) {
		t.Fatalf("unknown target: %v", err)
	}
}
