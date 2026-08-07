package identity

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
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
