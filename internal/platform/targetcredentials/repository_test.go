package targetcredentials

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCredentialPostgresMigrationAndCompareAndSwap(t *testing.T) {
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	schema := "target_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, db.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() {
		_ = db.Exec("DROP SCHEMA " + schema + " CASCADE").Error
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	isolated, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB, _ := isolated.DB(); _ = sqlDB.Close() })
	require.NoError(t, database.Migrate(isolated))
	require.NoError(t, database.ValidateRuntimeSchema(isolated))
	s, _, owner := fixture(t)
	repo := NewGormRepository(isolated)
	require.NoError(t, repo.Init())
	s.repository = repo
	s.audits = audit.NewService(audit.NewGormRepository(isolated))
	created, err := s.Create(context.Background(), owner, sample())
	require.NoError(t, err)
	stored, err := repo.Get(context.Background(), owner.UserID, created.ID)
	require.NoError(t, err)
	stale := clone(stored)
	stored.Name = "updated"
	stored.Revision++
	require.NoError(t, repo.Replace(context.Background(), stored, 1))
	require.ErrorIs(t, repo.Replace(context.Background(), stale, 1), ErrConflict)
	require.NoError(t, database.Migrate(isolated))
	_, err = repo.Get(context.Background(), "other", created.ID)
	require.ErrorIs(t, err, ErrNotFound)
}
