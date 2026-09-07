package mcpegress

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestGormCapabilityRepositoryRotatesAndStoresOnlyDigest(t *testing.T) {
	ctx := context.Background()
	databaseConnection := openMCPEgressPostgresDB(t)
	require.NoError(t, database.Migrate(databaseConnection))
	repository := NewGormRepository(databaseConnection)
	require.NoError(t, repository.Init())

	issuedAt := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	firstRaw := "first-capability-sentinel"
	secondRaw := "second-capability-sentinel"
	firstDigest := sha256.Sum256([]byte(firstRaw))
	secondDigest := sha256.Sum256([]byte(secondRaw))
	first, err := repository.Issue(ctx, "capability-task-sentinel", firstDigest[:], issuedAt, issuedAt.Add(time.Minute))
	require.NoError(t, err)
	second, err := repository.Issue(ctx, "capability-task-sentinel", secondDigest[:], issuedAt.Add(time.Second), issuedAt.Add(2*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 1, first.Rotation)
	assert.Equal(t, 2, second.Rotation)

	latest, err := repository.Latest(ctx, "capability-task-sentinel")
	require.NoError(t, err)
	assert.Equal(t, second.Rotation, latest.Rotation)
	assert.Equal(t, secondDigest[:], latest.CapabilityHash)

	var persisted []RuntimeCapability
	require.NoError(t, databaseConnection.Order("rotation ASC").Find(&persisted).Error)
	require.Len(t, persisted, 2)
	assert.NotContains(t, string(persisted[0].CapabilityHash), firstRaw)
	assert.NotContains(t, string(persisted[1].CapabilityHash), secondRaw)
}

func TestGormCapabilityRepositorySerializesConcurrentRotation(t *testing.T) {
	ctx := context.Background()
	databaseConnection := openMCPEgressPostgresDB(t)
	require.NoError(t, database.Migrate(databaseConnection))
	repository := NewGormRepository(databaseConnection)
	require.NoError(t, repository.Init())
	issuedAt := time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC)
	digests := [2][32]byte{sha256.Sum256([]byte("concurrent-capability-one")), sha256.Sum256([]byte("concurrent-capability-two"))}

	started := make(chan struct{})
	errorsByCaller := make(chan error, len(digests))
	var callers sync.WaitGroup
	for index := range digests {
		index := index
		callers.Add(1)
		go func() {
			defer callers.Done()
			<-started
			_, err := repository.Issue(ctx, "concurrent-capability-task", digests[index][:], issuedAt.Add(time.Duration(index)*time.Second), issuedAt.Add(time.Minute))
			errorsByCaller <- err
		}()
	}
	close(started)
	callers.Wait()
	close(errorsByCaller)
	for err := range errorsByCaller {
		require.NoError(t, err)
	}

	var persisted []RuntimeCapability
	require.NoError(t, databaseConnection.Where("task_id = ?", "concurrent-capability-task").Order("rotation ASC").Find(&persisted).Error)
	require.Len(t, persisted, 2)
	assert.Equal(t, 1, persisted[0].Rotation)
	assert.Equal(t, 2, persisted[1].Rotation)
}

func openMCPEgressPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to the isolated PostgreSQL test service")
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	schema := "mcp_egress_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, adminDB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)).Error)
	t.Cleanup(func() { require.NoError(t, adminDB.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)).Error) })
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	databaseConnection, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	return databaseConnection
}
