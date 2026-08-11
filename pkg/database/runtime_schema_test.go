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
