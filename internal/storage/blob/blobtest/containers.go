package blobtest

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Images used by the integration tests (same as deploy/docker/compose.dev.yaml).
const (
	MinIOImage = "bitnamilegacy/minio:2025.7.23-debian-12-r3"
	SFTPImage  = "atmoz/sftp:alpine"
)

// MinIO describes a running MinIO container.
type MinIO struct {
	Endpoint  string // http://host:port
	AccessKey string
	SecretKey string
	Bucket    string
}

// StartMinIO starts a throwaway MinIO with one bucket. The container is
// removed when the test ends. Skips without Docker (see RequireDocker).
func StartMinIO(t testing.TB) MinIO {
	t.Helper()
	RequireDocker(t)
	m := MinIO{ //nolint:gosec // throwaway test container credentials
		AccessKey: "ervisio_repo", SecretKey: "ervisio_repo_secret", Bucket: "ervisio-repo"}
	ctx := context.Background()
	ctr, err := testcontainers.Run(ctx, MinIOImage,
		testcontainers.WithEnv(map[string]string{
			"MINIO_ROOT_USER":       m.AccessKey,
			"MINIO_ROOT_PASSWORD":   m.SecretKey,
			"MINIO_DEFAULT_BUCKETS": m.Bucket,
		}),
		testcontainers.WithExposedPorts("9000/tcp"),
		testcontainers.WithWaitStrategyAndDeadline(2*time.Minute,
			wait.ForHTTP("/minio/health/ready").WithPort("9000/tcp"),
			// The bucket is created after the server starts.
			wait.ForLog("** MinIO setup finished! **"),
		),
	)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	ep, err := ctr.PortEndpoint(ctx, "9000/tcp", "http")
	if err != nil {
		t.Fatalf("minio endpoint: %v", err)
	}
	m.Endpoint = ep
	return m
}

// SFTP describes a running atmoz/sftp container. The user can write only
// inside Dir.
type SFTP struct {
	Addr     string // host:port
	User     string
	Password string
	Dir      string
}

// StartSFTP starts a throwaway OpenSSH SFTP server (atmoz/sftp, the stand-in
// for a Hetzner StorageBox). Skips without Docker (see RequireDocker).
func StartSFTP(t testing.TB) SFTP {
	t.Helper()
	return startSFTP(t, nil)
}

// StartSFTPWithKey is StartSFTP with authorizedKey (OpenSSH
// authorized_keys format) also accepted for the user.
func StartSFTPWithKey(t testing.TB, authorizedKey []byte) SFTP {
	t.Helper()
	return startSFTP(t, authorizedKey)
}

func startSFTP(t testing.TB, authorizedKey []byte) SFTP {
	t.Helper()
	RequireDocker(t)
	s := SFTP{User: "dev", Password: "dev", Dir: "upload"}
	ctx := context.Background()
	opts := []testcontainers.ContainerCustomizer{
		testcontainers.WithCmd(fmt.Sprintf("%s:%s:1001:100:%s", s.User, s.Password, s.Dir)),
		testcontainers.WithExposedPorts("22/tcp"),
		testcontainers.WithWaitStrategyAndDeadline(time.Minute,
			wait.ForLog("Server listening on"),
			wait.ForListeningPort("22/tcp"),
		),
	}
	if authorizedKey != nil {
		opts = append(opts, testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader:            bytes.NewReader(authorizedKey),
			ContainerFilePath: "/home/" + s.User + "/.ssh/keys/test.pub",
			FileMode:          0o644,
		}))
	}
	ctr, err := testcontainers.Run(ctx, SFTPImage, opts...)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start sftp: %v", err)
	}
	ep, err := ctr.PortEndpoint(ctx, "22/tcp", "")
	if err != nil {
		t.Fatalf("sftp endpoint: %v", err)
	}
	s.Addr = ep
	return s
}

// RequireDocker skips the test when Docker is not reachable, or fails it when
// EREPO_REQUIRE_DOCKER=1 (as in CI).
func RequireDocker(t testing.TB) {
	t.Helper()
	err := dockerHealth()
	if err == nil {
		return
	}
	if os.Getenv("EREPO_REQUIRE_DOCKER") == "1" {
		t.Fatalf("docker is required (EREPO_REQUIRE_DOCKER=1) but not reachable: %v", err)
	}
	t.Skipf("docker not reachable, skipping integration test: %v", err)
}

func dockerHealth() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("docker provider panicked: %v", r)
		}
	}()
	provider, err := testcontainers.ProviderDocker.GetProvider()
	if err != nil {
		return err
	}
	defer provider.Close()
	return provider.Health(context.Background())
}
