package s3

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/blobtest"
)

func TestConformanceMinIO(t *testing.T) {
	m := blobtest.StartMinIO(t)
	blobtest.Run(t, func(t *testing.T) blob.Store {
		s, err := New(Options{
			Name: "s3", Endpoint: m.Endpoint, Bucket: m.Bucket, PathStyle: true,
			AccessKeyID: m.AccessKey, SecretAccessKey: m.SecretKey,
			Prefix:             "erepo/",
			MultipartThreshold: 8 << 20,
			PartSize:           MinPartSize,
			ListPageSize:       7,
		})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}, blobtest.Options{})
}

func TestNewValidation(t *testing.T) {
	cases := []Options{
		{},
		{Bucket: "b", Prefix: "no-slash"},
		{Bucket: "b", Prefix: "../x/"},
		{Bucket: "b", PartSize: 1 << 20},
	}
	for i, o := range cases {
		if _, err := New(o); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
	if _, err := New(Options{Bucket: "b", Prefix: "p/q/"}); err != nil {
		t.Fatal(err)
	}
}

func TestPartSize(t *testing.T) {
	s, err := New(Options{Bucket: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.partSize(100 << 20); got != DefaultPartSize {
		t.Fatalf("partSize(100MiB) = %d", got)
	}
	big := int64(5) << 40 // 5 TiB needs parts larger than 16 MiB
	ps := s.partSize(big)
	if (big+ps-1)/ps > maxParts || ps%(1<<20) != 0 {
		t.Fatalf("partSize(5TiB) = %d", ps)
	}
}

func TestParseCredentials(t *testing.T) {
	id, secret, err := ParseCredentials([]byte("# hetzner\n[default]\naws_access_key_id = AKID\nsecret_access_key=\"s3cr3t\"\n"))
	if err != nil || id != "AKID" || secret != "s3cr3t" {
		t.Fatalf("got %q %q %v", id, secret, err)
	}
	if _, _, err := ParseCredentials([]byte("access_key_id=x\n")); err == nil {
		t.Fatal("expected error for missing secret")
	}
}

func TestInvalidKeysOffline(t *testing.T) {
	s, err := New(Options{Bucket: "b", Endpoint: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, k := range []string{"", "../a", "a//b", ".erepo-x"} {
		if _, err := s.Put(ctx, k, strings.NewReader(""), 0, blob.PutOpts{}); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("Put(%q): %v", k, err)
		}
		if _, err := s.PresignGet(ctx, k, 0); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("PresignGet(%q): %v", k, err)
		}
	}
}
