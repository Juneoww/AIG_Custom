package identity

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestGormRepositoryInitRequiresMigrationWithoutCreatingTables(t *testing.T) {
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to isolated PostgreSQL")
	adminDB, err := database.InitDB(database.NewConfig(dsn))
	require.NoError(t, err)
	schema := "identity_no_ddl_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, adminDB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)).Error)
	t.Cleanup(func() {
		require.NoError(t, adminDB.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)).Error)
	})

	db, err := database.InitDB(database.NewConfig(dsn + "&search_path=" + schema))
	require.NoError(t, err)
	err = NewGormRepository(db).Init()
	require.Error(t, err)
	require.Contains(t, err.Error(), "aig migrate")
	require.False(t, db.Migrator().HasTable(&User{}))
	require.False(t, db.Migrator().HasTable(&Session{}))
	require.False(t, db.Migrator().HasTable(&PasswordReset{}))
}

func TestGormIdentityPaginationUsesCountStableOrderAndSafeProjection(t *testing.T) {
	db := openIdentityPaginationPostgres(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	createdAt := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	for _, id := range []string{"user-a", "user-b", "user-c"} {
		require.NoError(t, repository.CreateUser(context.Background(), &User{
			ID: id, Username: id, PasswordHash: "hash-sentinel-" + id, Role: RoleUser,
			Active: true, MustChangePassword: true, CreatedAt: createdAt, UpdatedAt: createdAt,
		}))
	}

	users, total, err := repository.ListUsers(context.Background(), UserListQuery{Limit: 1, Offset: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, users, 1)
	assert.Equal(t, "user-b", users[0].ID)
	assert.Empty(t, users[0].PasswordHash)
}

func openIdentityPaginationPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to isolated PostgreSQL")
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	schema := "identity_page_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, adminDB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)).Error)
	t.Cleanup(func() { require.NoError(t, adminDB.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)).Error) })

	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	return db
}
