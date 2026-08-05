package identity

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestBootstrapAdminCreatesOnlyFirstAdministrator(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()
	require.NoError(t, BootstrapAdmin(ctx, service, "root", "one-time-password"))
	login, err := service.Authenticate(ctx, "root", "one-time-password")
	require.NoError(t, err)
	require.Equal(t, RoleAdmin, login.Subject.Role)
	require.ErrorIs(t, BootstrapAdmin(ctx, service, "another", "another-password"), ErrAdminAlreadyBootstrapped)
}

func TestBootstrapAdminPersistsOnlyOneAdministratorInPostgres(t *testing.T) {
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("AIG_TEST_DB_DSN must point to the isolated PostgreSQL test service")
	}
	adminDB, err := database.InitDB(database.NewConfig(dsn))
	require.NoError(t, err)
	schema := "identity_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, adminDB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)).Error)
	t.Cleanup(func() {
		require.NoError(t, adminDB.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)).Error)
		sqlDB, sqlErr := adminDB.DB()
		require.NoError(t, sqlErr)
		require.NoError(t, sqlDB.Close())
	})

	db, err := database.InitDB(database.NewConfig(dsn + "&search_path=" + schema))
	require.NoError(t, err)
	repo := NewGormRepository(db)
	require.NoError(t, repo.Init())
	service := NewService(repo)

	require.NoError(t, BootstrapAdmin(context.Background(), service, "root", "one-time-password"))
	require.ErrorIs(t, BootstrapAdmin(context.Background(), service, "second", "another-password"), ErrAdminAlreadyBootstrapped)
	login, err := service.Authenticate(context.Background(), "root", "one-time-password")
	require.NoError(t, err)
	require.Equal(t, RoleAdmin, login.Subject.Role)
}
