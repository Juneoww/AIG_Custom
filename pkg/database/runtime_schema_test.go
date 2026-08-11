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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRuntimeStoreInitializationRejectsOldSchemaWithoutDDL(t *testing.T) {
	db := openPostgresTestDB(t)

	stores := []struct {
		name string
		init func(*gorm.DB) error
	}{
		{name: "task", init: func(db *gorm.DB) error { return NewTaskStore(db).Init() }},
		{name: "model", init: func(db *gorm.DB) error { return NewModelStore(db).Init() }},
		{name: "agent", init: func(db *gorm.DB) error { return NewAgentStore(db).Init() }},
	}

	for _, schemaVersion := range []int64{2, 3} {
		for _, store := range stores {
			t.Run(fmt.Sprintf("v%d/%s", schemaVersion, store.name), func(t *testing.T) {
				resetPostgresTestDB(t, db)
				prepareRuntimeSchemaFixture(t, db, schemaVersion)

				beforeVersions := migrationVersions(t, db)
				beforeTables := runtimeTablePresence(db)

				err := store.init(db)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "aig migrate")
				assert.Equal(t, beforeVersions, migrationVersions(t, db), "runtime initialization must not update schema_migrations")
				assert.Equal(t, beforeTables, runtimeTablePresence(db), "runtime initialization must not create or remove tables")
			})
		}
	}
}

func TestRuntimeStoreInitializationValidatesRequiredObjectsWithoutDDL(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)
	require.NoError(t, Migrate(db))

	for name, init := range map[string]func() error{
		"task":  NewTaskStore(db).Init,
		"model": NewModelStore(db).Init,
		"agent": NewAgentStore(db).Init,
	} {
		t.Run("current/"+name, func(t *testing.T) {
			require.NoError(t, init())
		})
	}

	require.NoError(t, db.Exec("DROP INDEX idx_sessions_status").Error)
	err := NewTaskStore(db).Init()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "aig migrate")
	assert.False(t, db.Migrator().HasIndex(&Session{}, "idx_sessions_status"), "runtime initialization must not recreate indexes")
}

func TestRuntimeSchemaRequiresUniqueCompletionRequestIndexWithoutDDL(t *testing.T) {
	db := openPostgresTestDB(t)
	for _, fixture := range []struct {
		name             string
		createPlainIndex bool
	}{
		{name: "same-name ordinary index", createPlainIndex: true},
		{name: "missing index"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			resetPostgresTestDB(t, db)
			require.NoError(t, Migrate(db))
			require.NoError(t, db.Exec("DROP INDEX idx_audit_completion_outbox_request_id").Error)
			if fixture.createPlainIndex {
				require.NoError(t, db.Exec("CREATE INDEX idx_audit_completion_outbox_request_id ON audit_completion_outbox(request_id)").Error)
			}

			beforeVersions := migrationVersions(t, db)
			beforeIndexCount, beforeUniqueCount := completionRequestIndexCatalogState(t, db)
			err := ValidateRuntimeSchema(db)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "idx_audit_completion_outbox_request_id")
			assert.Contains(t, err.Error(), "aig migrate")
			assert.Equal(t, beforeVersions, migrationVersions(t, db), "runtime validation must not update migration history")
			afterIndexCount, afterUniqueCount := completionRequestIndexCatalogState(t, db)
			assert.Equal(t, beforeIndexCount, afterIndexCount, "runtime validation must not create or replace the index")
			assert.Equal(t, beforeUniqueCount, afterUniqueCount, "runtime validation must not change index uniqueness")
			if fixture.createPlainIndex {
				assert.Equal(t, int64(1), afterIndexCount)
				assert.Zero(t, afterUniqueCount)
			} else {
				assert.Zero(t, afterIndexCount)
			}
		})
	}
}

func completionRequestIndexCatalogState(t *testing.T, db *gorm.DB) (int64, int64) {
	t.Helper()
	const catalogQuery = `
SELECT count(*) AS index_count,
       count(*) FILTER (WHERE index_definition.indisunique) AS unique_count
FROM pg_catalog.pg_class AS table_definition
JOIN pg_catalog.pg_namespace AS table_namespace ON table_namespace.oid = table_definition.relnamespace
JOIN pg_catalog.pg_index AS index_definition ON index_definition.indrelid = table_definition.oid
JOIN pg_catalog.pg_class AS index_name ON index_name.oid = index_definition.indexrelid
WHERE table_namespace.nspname = current_schema()
  AND table_definition.relname = 'audit_completion_outbox'
  AND index_name.relname = 'idx_audit_completion_outbox_request_id'`
	var state struct {
		IndexCount  int64 `gorm:"column:index_count"`
		UniqueCount int64 `gorm:"column:unique_count"`
	}
	require.NoError(t, db.Raw(catalogQuery).Scan(&state).Error)
	return state.IndexCount, state.UniqueCount
}

func prepareRuntimeSchemaFixture(t *testing.T, db *gorm.DB, version int64) {
	t.Helper()
	require.Contains(t, []int64{2, 3}, version)
	require.NoError(t, db.AutoMigrate(&SchemaMigration{}))
	require.NoError(t, migrateInitialSchema(db))
	require.NoError(t, migrateIdentitySchema(db))
	for _, applied := range []int64{1, 2} {
		require.NoError(t, db.Create(&SchemaMigration{Version: applied}).Error)
	}
	if version == 3 {
		require.NoError(t, migrateGovernanceSchema(db))
		require.NoError(t, db.Create(&SchemaMigration{Version: 3}).Error)
	}
}

func migrationVersions(t *testing.T, db *gorm.DB) []int64 {
	t.Helper()
	var versions []int64
	require.NoError(t, db.Model(&SchemaMigration{}).Order("version ASC").Pluck("version", &versions).Error)
	return versions
}

func runtimeTablePresence(db *gorm.DB) map[string]bool {
	tables := []string{
		"users", "sessions", "task_messages", "models", "agents",
		"identity_users", "identity_sessions", "identity_password_resets",
		"audit_events", "audit_completion_outbox", "platform_models",
	}
	presence := make(map[string]bool, len(tables))
	for _, table := range tables {
		presence[table] = db.Migrator().HasTable(table)
	}
	return presence
}
