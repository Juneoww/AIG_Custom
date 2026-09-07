// Copyright (c) 2024-2026 Tencent Zhuque Lab. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package database

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func openPostgresTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to the isolated PostgreSQL test service")

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)

	return db
}

func resetPostgresTestDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Migrator().DropTable(
		"platform_idempotency_records",
		"platform_mcp_runtime_capabilities",
		"platform_mcp_task_bindings",
		"platform_mcp_connection_versions",
		"platform_mcp_connection_configs",
		"report_brand_settings",
		"report_snapshots",
		"platform_attachments",
		"platform_tasks",
		"platform_models",
		"audit_completion_outbox",
		"audit_events",
		&identityPasswordResetMigration{},
		&identitySessionMigration{},
		&identityUserMigration{},
		&SchemaMigration{},
		&TaskMessage{},
		&Session{},
		&Model{},
		&User{},
		&Agent{},
	))
}

func TestResetPostgresTestDBClearsMCPConnectionSchemaFixtures(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)
	t.Cleanup(func() { dropMCPConnectionSchemaTables(t, db) })
	require.NoError(t, Migrate(db))

	const indexName = "ux_platform_mcp_connection_versions_config_version"
	require.NoError(t, db.Exec("DROP INDEX "+indexName).Error)
	require.NoError(t, db.Exec("CREATE UNIQUE INDEX "+indexName+" ON platform_mcp_connection_versions(id)").Error)
	require.NoError(t, db.Where("version >= ?", int64(10)).Delete(&SchemaMigration{}).Error)
	require.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9}, migrationVersions(t, db))

	resetPostgresTestDB(t, db)
	for table, present := range mcpConnectionRuntimeTablePresence(db) {
		assert.Falsef(t, present, "reset must remove stale %s table", table)
	}
	assert.False(t, db.Migrator().HasTable(&SchemaMigration{}), "reset must remove a v9 migration history")
	assert.False(t, db.Migrator().HasIndex("platform_mcp_connection_versions", indexName), "reset must remove a stale same-name index")

	require.NoError(t, Migrate(db), "the next test run must not inherit stale v10 objects")
	assertMCPConnectionSchema(t, db)
	require.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, migrationVersions(t, db))
}

func testPostgresDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to the isolated PostgreSQL test service")
	return dsn
}
