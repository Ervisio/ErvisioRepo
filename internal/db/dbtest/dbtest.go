// Package dbtest starts a throwaway PostgreSQL for integration tests.
//
// Tests are skipped when Docker is not reachable, unless EREPO_REQUIRE_DOCKER=1
// (as in CI), in which case they fail.
package dbtest

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Image is the PostgreSQL image used by integration tests.
const Image = "postgres:16-alpine"

// URL starts a fresh PostgreSQL container and returns its connection string.
// The container is removed when the test ends.
func URL(t testing.TB) string {
	t.Helper()
	requireDocker(t)

	ctx := context.Background()
	ctr, err := postgres.Run(ctx, Image,
		postgres.WithDatabase("ervisio_repo"),
		postgres.WithUsername("ervisio_repo"),
		postgres.WithPassword("ervisio_repo"),
		postgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres connection string: %v", err)
	}
	return url
}

func requireDocker(t testing.TB) {
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
