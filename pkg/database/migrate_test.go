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
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type governanceAuditCompletionV3Fixture struct {
	ID            string `gorm:"primaryKey;column:id"`
	EventID       string `gorm:"uniqueIndex;not null"`
	RequestID     string `gorm:"index;not null"`
	ActorUserID   string `gorm:"index"`
	ActorUsername string `gorm:"index"`
	ActorRole     string `gorm:"index"`
	Action        string `gorm:"index;not null"`
	ResourceType  string `gorm:"index"`
	ResourceID    string `gorm:"index"`
	Outcome       string `gorm:"index;not null"`
	ClientIP      string
	Metadata      json.RawMessage `gorm:"type:jsonb;not null"`
	CreatedAt     time.Time       `gorm:"index;not null"`
	Attempts      int             `gorm:"not null;default:0"`
	DeliveredAt   *time.Time      `gorm:"index"`
}

func (governanceAuditCompletionV3Fixture) TableName() string { return "audit_completion_outbox" }

func TestMigrationAppliesIdentitySchemaAsVersionTwo(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)

	require.NoError(t, Migrate(db))
	assert.True(t, db.Migrator().HasTable(&identity.User{}))
	assert.True(t, db.Migrator().HasTable(&identity.Session{}))
	assert.True(t, db.Migrator().HasTable(&identity.PasswordReset{}))

	var versions []SchemaMigration
	require.NoError(t, db.Order("version ASC").Find(&versions).Error)
	require.Len(t, versions, 11)
	assert.Equal(t, int64(1), versions[0].Version)
	assert.Equal(t, int64(2), versions[1].Version)
	assert.Equal(t, int64(3), versions[2].Version)
	assert.Equal(t, int64(4), versions[3].Version)
	assert.Equal(t, int64(5), versions[4].Version)
	assert.Equal(t, int64(6), versions[5].Version)
	assert.Equal(t, int64(7), versions[6].Version)
	assert.Equal(t, int64(8), versions[7].Version)
	assert.Equal(t, int64(9), versions[8].Version)
	assert.Equal(t, int64(10), versions[9].Version)
	assert.Equal(t, int64(11), versions[10].Version)
}

func TestMigrationAppliesGovernanceAndPlatformTaskSchemaThroughVersionEleven(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)

	require.NoError(t, Migrate(db))
	assert.True(t, db.Migrator().HasTable("audit_events"))
	assert.True(t, db.Migrator().HasTable("audit_completion_outbox"))
	assert.True(t, db.Migrator().HasTable("platform_models"))
	assert.True(t, db.Migrator().HasTable("platform_tasks"))
	assert.True(t, db.Migrator().HasTable("platform_attachments"))
	assert.True(t, db.Migrator().HasTable("report_snapshots"))
	assert.True(t, db.Migrator().HasTable("report_brand_settings"))
	assertReportTrendIndexDefinitions(t, db)
	assertPlatformTaskDashboardIndexDefinitions(t, db)

	var versions []SchemaMigration
	require.NoError(t, db.Order("version ASC").Find(&versions).Error)
	require.Len(t, versions, 11)
	assert.Equal(t, int64(3), versions[2].Version)
	assert.Equal(t, int64(4), versions[3].Version)
	assert.Equal(t, int64(5), versions[4].Version)
	assert.Equal(t, int64(6), versions[5].Version)
	assert.Equal(t, int64(7), versions[6].Version)
	assert.Equal(t, int64(8), versions[7].Version)
	assert.Equal(t, int64(9), versions[8].Version)
	assert.Equal(t, int64(10), versions[9].Version)
	assert.Equal(t, int64(11), versions[10].Version)
}

func TestMigrationAppliesMCPConnectionSchemaThroughVersionEleven(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)
	dropMCPConnectionSchemaTables(t, db)
	for table, present := range mcpConnectionRuntimeTablePresence(db) {
		assert.Falsef(t, present, "empty fixture must not include %s", table)
	}

	require.NoError(t, Migrate(db))
	assertMCPConnectionSchema(t, db)
	assert.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, migrationVersions(t, db))

	require.NoError(t, Migrate(db), "v11 migration must remain idempotent")
	assertMCPConnectionSchema(t, db)
}

func TestMigrationVersionElevenAddsProbeTimestampToExistingVersionTenSchema(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)
	require.NoError(t, Migrate(db))
	require.NoError(t, db.Exec("ALTER TABLE platform_mcp_connection_configs DROP COLUMN last_probe_started_at").Error)
	require.NoError(t, db.Where("version = ?", int64(11)).Delete(&SchemaMigration{}).Error)
	assert.False(t, db.Migrator().HasColumn("platform_mcp_connection_configs", "last_probe_started_at"))

	// v10 only validates its released table layout. It must not secretly recreate the v11 column.
	require.NoError(t, migratePlatformMCPConnectionSchema(db))
	assert.False(t, db.Migrator().HasColumn("platform_mcp_connection_configs", "last_probe_started_at"))

	require.NoError(t, Migrate(db))
	require.NoError(t, Migrate(db), "v11 probe timestamp migration must remain idempotent")
	assert.True(t, db.Migrator().HasColumn("platform_mcp_connection_configs", "last_probe_started_at"))
	assert.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, migrationVersions(t, db))
}

func TestMigrationVersionTenRejectsIncompatibleExistingMCPIndexes(t *testing.T) {
	db := openPostgresTestDB(t)
	for _, requirement := range mcpConnectionSchemaTestIndexRequirements {
		t.Run(requirement.name, func(t *testing.T) {
			resetPostgresTestDB(t, db)
			dropMCPConnectionSchemaTables(t, db)
			require.NoError(t, Migrate(db))
			require.NoError(t, db.Exec("DROP INDEX "+requirement.name).Error)
			require.NoError(t, db.Exec(requirement.incompatibleCreateStatement()).Error)
			require.NoError(t, db.Where("version >= ?", int64(10)).Delete(&SchemaMigration{}).Error)

			err := Migrate(db)
			require.Error(t, err)
			assert.Contains(t, err.Error(), requirement.name)
			assert.NotContains(t, migrationVersions(t, db), int64(10))
		})
	}
}

func TestMigrationVersionTenRejectsDeferrableUniqueMCPIndexes(t *testing.T) {
	db := openPostgresTestDB(t)
	for _, requirement := range mcpConnectionSchemaTestIndexRequirements {
		if !requirement.unique {
			continue
		}
		t.Run(requirement.name, func(t *testing.T) {
			resetPostgresTestDB(t, db)
			dropMCPConnectionSchemaTables(t, db)
			t.Cleanup(func() { dropMCPConnectionSchemaTables(t, db) })
			require.NoError(t, Migrate(db))
			replaceMCPIndexWithDeferrableUniqueConstraint(t, db, requirement)
			require.NoError(t, db.Where("version >= ?", int64(10)).Delete(&SchemaMigration{}).Error)

			err := Migrate(db)
			require.Error(t, err)
			assert.Contains(t, err.Error(), requirement.name)
			assert.NotContains(t, migrationVersions(t, db), int64(10))
		})
	}
}

func TestMigrationUpgradesExistingVersionThreeWithoutRewritingIt(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)

	require.NoError(t, db.AutoMigrate(&SchemaMigration{}))
	require.NoError(t, migrateInitialSchema(db))
	require.NoError(t, migrateIdentitySchema(db))
	require.NoError(t, db.AutoMigrate(&governanceAuditEventMigration{}, &governanceModelMigration{}), "fixture reproduces the released v3 schema without an outbox")
	for _, version := range []int64{1, 2, 3} {
		require.NoError(t, db.Create(&SchemaMigration{Version: version}).Error)
	}
	require.False(t, db.Migrator().HasTable("audit_completion_outbox"))

	require.NoError(t, Migrate(db))
	require.NoError(t, Migrate(db), "v4 upgrade remains idempotent")
	require.True(t, db.Migrator().HasTable("audit_completion_outbox"))
	var versions []SchemaMigration
	require.NoError(t, db.Order("version ASC").Find(&versions).Error)
	require.Len(t, versions, 11)
	assert.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, []int64{versions[0].Version, versions[1].Version, versions[2].Version, versions[3].Version, versions[4].Version, versions[5].Version, versions[6].Version, versions[7].Version, versions[8].Version, versions[9].Version, versions[10].Version})
}

func TestMigrationVersionFourRepairsDuplicateLegacyCompletionOutboxRows(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)

	require.NoError(t, db.AutoMigrate(&SchemaMigration{}))
	require.NoError(t, migrateInitialSchema(db))
	require.NoError(t, migrateIdentitySchema(db))
	require.NoError(t, db.AutoMigrate(
		&governanceAuditEventMigration{}, &governanceModelMigration{}, &governanceAuditCompletionV3Fixture{},
	), "fixture reproduces the 0c6b v3 outbox with a non-unique request_id index")
	for _, version := range []int64{1, 2, 3} {
		require.NoError(t, db.Create(&SchemaMigration{Version: version}).Error)
	}

	base := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	deliveredAt := base.Add(3 * time.Minute)
	rows := []governanceAuditCompletionV3Fixture{
		{ID: "delivered-old", EventID: "event-delivered-old", RequestID: "delivered-request", Action: "knowledge.changed", Outcome: "success", Metadata: json.RawMessage(`{}`), CreatedAt: base},
		{ID: "delivered-keep", EventID: "event-delivered-keep", RequestID: "delivered-request", Action: "knowledge.changed", Outcome: "success", Metadata: json.RawMessage(`{}`), CreatedAt: base.Add(time.Minute), DeliveredAt: &deliveredAt},
		{ID: "pending-old", EventID: "event-pending-old", RequestID: "pending-request", Action: "knowledge.changed", Outcome: "success", Metadata: json.RawMessage(`{}`), CreatedAt: base},
		{ID: "pending-tie-a", EventID: "event-pending-tie-a", RequestID: "pending-request", Action: "knowledge.changed", Outcome: "success", Metadata: json.RawMessage(`{}`), CreatedAt: base.Add(2 * time.Minute)},
		{ID: "pending-tie-z", EventID: "event-pending-tie-z", RequestID: "pending-request", Action: "knowledge.changed", Outcome: "success", Metadata: json.RawMessage(`{}`), CreatedAt: base.Add(2 * time.Minute)},
	}
	require.NoError(t, db.Create(&rows).Error)
	require.NoError(t, db.Create(&governanceAuditEventMigration{
		ID: "immutable-audit-event", OccurredAt: base, Action: "knowledge.changed", Outcome: "success", Metadata: json.RawMessage(`{}`),
	}).Error)

	require.NoError(t, Migrate(db))
	require.NoError(t, Migrate(db), "the repaired v4 migration remains idempotent")
	var kept []struct {
		ID        string
		RequestID string
		State     string
	}
	require.NoError(t, db.Table("audit_completion_outbox").Order("request_id ASC").Find(&kept).Error)
	require.Len(t, kept, 2)
	assert.Equal(t, []string{"delivered-keep", "pending-tie-z"}, []string{kept[0].ID, kept[1].ID})
	assert.Equal(t, []string{"ready", "ready"}, []string{kept[0].State, kept[1].State})

	var uniqueRequestIndex bool
	require.NoError(t, db.Raw(`
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE schemaname = current_schema()
			  AND tablename = 'audit_completion_outbox'
			  AND indexname = 'idx_audit_completion_outbox_request_id'
			  AND indexdef LIKE 'CREATE UNIQUE INDEX%'
		)`).Scan(&uniqueRequestIndex).Error)
	assert.True(t, uniqueRequestIndex, "v4 explicitly replaces the legacy non-unique request_id index")
	duplicate := governanceAuditCompletionV3Fixture{
		ID: "duplicate-rejected", EventID: "event-duplicate-rejected", RequestID: "pending-request",
		Action: "knowledge.changed", Outcome: "success", Metadata: json.RawMessage(`{}`), CreatedAt: base.Add(4 * time.Minute),
	}
	assert.Error(t, db.Create(&duplicate).Error)
	var auditEventCount int64
	require.NoError(t, db.Table("audit_events").Where("id = ?", "immutable-audit-event").Count(&auditEventCount).Error)
	assert.Equal(t, int64(1), auditEventCount, "v4 never deletes append-only audit events")
}

func TestDatabaseConfigRejectsMissingDSN(t *testing.T) {
	cfg := &Config{Driver: "postgres"}
	_, err := InitDB(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DB_DSN")
}

func TestDatabaseConfigRejectsUnknownDriver(t *testing.T) {
	cfg := &Config{Driver: "sqlite", DSN: "file:tasks.db"}
	_, err := InitDB(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres")
}

func TestDatabaseConfigAcceptsPostgresDSN(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)

	cfg := &Config{Driver: "postgres", DSN: testPostgresDSN(t), MaxIdleConns: 2, MaxOpenConns: 4}
	opened, err := InitDB(cfg)
	require.NoError(t, err)

	sqlDB, err := opened.DB()
	require.NoError(t, err)
	assert.NoError(t, sqlDB.Ping())
	require.NoError(t, sqlDB.Close())
}

func TestMigrationAppliesInitialSchemaToEmptyDatabase(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)

	require.NoError(t, Migrate(db))
	assert.True(t, db.Migrator().HasTable(&SchemaMigration{}))
	assert.True(t, db.Migrator().HasTable(&User{}))
	assert.True(t, db.Migrator().HasTable(&Session{}))
	assert.True(t, db.Migrator().HasTable(&TaskMessage{}))
	assert.True(t, db.Migrator().HasTable(&Model{}))
	assert.True(t, db.Migrator().HasTable(&Agent{}))
}

func TestMigrationIsIdempotentAndRecordsVersion(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)

	require.NoError(t, Migrate(db))
	require.NoError(t, Migrate(db))

	var versions []SchemaMigration
	require.NoError(t, db.Order("version ASC").Find(&versions).Error)
	require.Len(t, versions, 11)
	assert.Equal(t, int64(1), versions[0].Version)
	assert.Equal(t, int64(2), versions[1].Version)
	assert.Equal(t, int64(3), versions[2].Version)
	assert.Equal(t, int64(4), versions[3].Version)
	assert.Equal(t, int64(5), versions[4].Version)
	assert.Equal(t, int64(6), versions[5].Version)
	assert.Equal(t, int64(7), versions[6].Version)
	assert.Equal(t, int64(8), versions[7].Version)
	assert.Equal(t, int64(9), versions[8].Version)
	assert.Equal(t, int64(10), versions[9].Version)
	assert.Equal(t, int64(11), versions[10].Version)
	assert.NotZero(t, versions[0].AppliedAt)
	assert.NotZero(t, versions[1].AppliedAt)
	assert.NotZero(t, versions[2].AppliedAt)
	assert.NotZero(t, versions[3].AppliedAt)
}

func TestMigrationSerializesConcurrentPostgresCalls(t *testing.T) {
	first := openPostgresTestDB(t)
	resetPostgresTestDB(t, first)
	second := openPostgresTestDB(t)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var calls sync.WaitGroup
	for _, db := range []*gorm.DB{first, second} {
		calls.Add(1)
		go func(db *gorm.DB) {
			defer calls.Done()
			<-start
			errs <- Migrate(db)
		}(db)
	}

	close(start)
	calls.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	var versions []SchemaMigration
	require.NoError(t, first.Order("version ASC").Find(&versions).Error)
	require.Len(t, versions, 11)
	assert.Equal(t, int64(1), versions[0].Version)
	assert.Equal(t, int64(2), versions[1].Version)
	assert.Equal(t, int64(3), versions[2].Version)
	assert.Equal(t, int64(4), versions[3].Version)
	assert.Equal(t, int64(5), versions[4].Version)
	assert.Equal(t, int64(6), versions[5].Version)
	assert.Equal(t, int64(7), versions[6].Version)
	assert.Equal(t, int64(8), versions[7].Version)
	assert.Equal(t, int64(9), versions[8].Version)
	assert.Equal(t, int64(10), versions[9].Version)
	assert.Equal(t, int64(11), versions[10].Version)
	assert.True(t, first.Migrator().HasTable(&User{}))
	assert.True(t, first.Migrator().HasTable(&Session{}))
	assert.True(t, first.Migrator().HasTable(&TaskMessage{}))
	assert.True(t, first.Migrator().HasTable(&Model{}))
	assert.True(t, first.Migrator().HasTable(&Agent{}))
	assert.True(t, first.Migrator().HasTable(&identity.User{}))
	assert.True(t, first.Migrator().HasTable(&identity.Session{}))
	assert.True(t, first.Migrator().HasTable(&identity.PasswordReset{}))
	assert.True(t, first.Migrator().HasTable("audit_events"))
	assert.True(t, first.Migrator().HasTable("audit_completion_outbox"))
	assert.True(t, first.Migrator().HasTable("platform_models"))
	assert.True(t, first.Migrator().HasTable("platform_tasks"))
	assert.True(t, first.Migrator().HasTable("platform_attachments"))
	assert.True(t, first.Migrator().HasTable("report_snapshots"))
	assert.True(t, first.Migrator().HasTable("report_brand_settings"))
	assert.True(t, first.Migrator().HasIndex(&Model{}, "idx_models_username_created"))
}

func TestMigrationVersionFiveEnforcesOwnerIdempotencyAndEngineMappingUniqueness(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)
	require.NoError(t, Migrate(db))

	row := map[string]any{
		"id": "platform-task-1", "owner_user_id": "owner-1", "owner_username": "alice",
		"idempotency_key": "same-key", "engine_session_id": "engine-1", "task_type": "mcp_scan",
		"content": "scan", "params": json.RawMessage(`{}`), "attachment_refs": json.RawMessage(`[]`),
		"status": "pending", "dispatch_error": "", "dispatch_attempts": 0,
		"created_at": time.Now().UTC(), "updated_at": time.Now().UTC(),
	}
	require.NoError(t, db.Table("platform_tasks").Create(row).Error)

	duplicateKey := make(map[string]any, len(row))
	for key, value := range row {
		duplicateKey[key] = value
	}
	duplicateKey["id"] = "platform-task-2"
	duplicateKey["engine_session_id"] = "engine-2"
	require.Error(t, db.Table("platform_tasks").Create(duplicateKey).Error)

	duplicateEngine := make(map[string]any, len(row))
	for key, value := range row {
		duplicateEngine[key] = value
	}
	duplicateEngine["id"] = "platform-task-3"
	duplicateEngine["idempotency_key"] = "another-key"
	require.Error(t, db.Table("platform_tasks").Create(duplicateEngine).Error)
}

func TestMigrationUpgradesReleasedVersionFiveWithDispatchClaimColumn(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)
	require.NoError(t, db.AutoMigrate(&SchemaMigration{}))
	require.NoError(t, migrateInitialSchema(db))
	require.NoError(t, migrateIdentitySchema(db))
	require.NoError(t, migrateGovernanceSchema(db))
	require.NoError(t, migrateAuditCompletionSchema(db))
	require.NoError(t, migratePlatformTaskSchema(db))
	for version := int64(1); version <= 5; version++ {
		require.NoError(t, db.Create(&SchemaMigration{Version: version}).Error)
	}
	assert.False(t, db.Migrator().HasColumn("platform_tasks", "dispatch_claim_token"))

	require.NoError(t, Migrate(db))
	require.NoError(t, Migrate(db), "v6 upgrade must remain idempotent")
	assert.True(t, db.Migrator().HasColumn("platform_tasks", "dispatch_claim_token"))
	assert.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, migrationVersions(t, db))
}

func TestMigrationUpgradesReleasedVersionSixWithReportTables(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)
	require.NoError(t, db.AutoMigrate(&SchemaMigration{}))
	require.NoError(t, migrateInitialSchema(db))
	require.NoError(t, migrateIdentitySchema(db))
	require.NoError(t, migrateGovernanceSchema(db))
	require.NoError(t, migrateAuditCompletionSchema(db))
	require.NoError(t, migratePlatformTaskSchema(db))
	require.NoError(t, migratePlatformTaskDispatchClaimSchema(db))
	for version := int64(1); version <= 6; version++ {
		require.NoError(t, db.Create(&SchemaMigration{Version: version}).Error)
	}
	assert.False(t, db.Migrator().HasTable("report_snapshots"))
	assert.False(t, db.Migrator().HasTable("report_brand_settings"))

	require.NoError(t, Migrate(db))
	require.NoError(t, Migrate(db), "v7 through v11 upgrades must remain idempotent")
	assert.True(t, db.Migrator().HasTable("report_snapshots"))
	assert.True(t, db.Migrator().HasTable("report_brand_settings"))
	assertReportTrendIndexDefinitions(t, db)
	assertPlatformTaskDashboardIndexDefinitions(t, db)
	assert.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, migrationVersions(t, db))
}

func TestMigrationUpgradesReleasedVersionSevenWithDashboardTaskIndexes(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)
	require.NoError(t, db.AutoMigrate(&SchemaMigration{}))
	for _, apply := range []func(*gorm.DB) error{
		migrateInitialSchema,
		migrateIdentitySchema,
		migrateGovernanceSchema,
		migrateAuditCompletionSchema,
		migratePlatformTaskSchema,
		migratePlatformTaskDispatchClaimSchema,
		migrateReportSchema,
	} {
		require.NoError(t, apply(db))
	}
	for version := int64(1); version <= 7; version++ {
		require.NoError(t, db.Create(&SchemaMigration{Version: version}).Error)
	}
	for _, index := range []string{"idx_platform_tasks_updated_at", "idx_platform_tasks_owner_updated_at"} {
		assert.False(t, db.Migrator().HasIndex(&platformTaskMigration{}, index))
	}

	require.NoError(t, Migrate(db))
	require.NoError(t, Migrate(db), "v8 through v11 upgrades must remain idempotent")
	assertPlatformTaskDashboardIndexDefinitions(t, db)
	assert.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, migrationVersions(t, db))
}

func TestMigrationVersionNineBindsOnlyReadyAttachmentsReferencedByTasks(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)
	require.NoError(t, db.AutoMigrate(&SchemaMigration{}))
	for _, apply := range []func(*gorm.DB) error{
		migrateInitialSchema,
		migrateIdentitySchema,
		migrateGovernanceSchema,
		migrateAuditCompletionSchema,
		migratePlatformTaskSchema,
		migratePlatformTaskDispatchClaimSchema,
		migrateReportSchema,
		migratePlatformTaskDashboardIndexes,
	} {
		require.NoError(t, apply(db))
	}
	for version := int64(1); version <= 8; version++ {
		require.NoError(t, db.Create(&SchemaMigration{Version: version}).Error)
	}
	now := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	for _, attachment := range []map[string]any{
		{"id": "referenced-ready", "owner_user_id": "owner", "original_name": "bound.txt", "storage_name": "bound", "size": 4, "chunk_bytes": 4, "state": "ready", "created_at": now, "updated_at": now},
		{"id": "unbound-ready", "owner_user_id": "owner", "original_name": "unbound.txt", "storage_name": "unbound", "size": 4, "chunk_bytes": 4, "state": "ready", "created_at": now, "updated_at": now},
	} {
		require.NoError(t, db.Table("platform_attachments").Create(attachment).Error)
	}
	require.NoError(t, db.Table("platform_tasks").Create(map[string]any{
		"id": "task-with-attachment", "owner_user_id": "owner", "owner_username": "owner", "idempotency_key": "migration-v9",
		"engine_session_id": "engine-v9", "task_type": "ai_infra_scan", "content": "scan", "params": json.RawMessage(`{}`),
		"attachment_refs": json.RawMessage(`["referenced-ready"]`), "status": "pending", "dispatch_error": "", "dispatch_attempts": 0,
		"dispatch_claim_token": "", "created_at": now, "updated_at": now,
	}).Error)

	require.NoError(t, Migrate(db))
	require.NoError(t, migratePlatformAttachmentLifecycle(db), "回填本身也必须可安全重跑")
	var states []struct {
		ID    string
		State string
	}
	require.NoError(t, db.Table("platform_attachments").Order("id ASC").Find(&states).Error)
	require.Equal(t, []struct {
		ID    string
		State string
	}{{ID: "referenced-ready", State: "attached"}, {ID: "unbound-ready", State: "ready"}}, states)
	assert.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, migrationVersions(t, db))
}

func assertReportTrendIndexDefinitions(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, requirement := range []struct {
		name, columns string
	}{
		{name: "idx_report_snapshots_completed_at", columns: "(completed_at DESC)"},
		{name: "idx_report_snapshots_owner_completed_at", columns: "(owner_user_id, completed_at DESC)"},
	} {
		t.Run(requirement.name, func(t *testing.T) {
			var definition string
			require.NoError(t, db.Raw(`
SELECT indexdef
FROM pg_catalog.pg_indexes
WHERE schemaname = current_schema() AND tablename = 'report_snapshots' AND indexname = ?`, requirement.name).Scan(&definition).Error)
			assert.Contains(t, definition, requirement.columns)
		})
	}
}

func assertPlatformTaskDashboardIndexDefinitions(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, requirement := range []struct {
		name, columns string
	}{
		{name: "idx_platform_tasks_updated_at", columns: "(updated_at DESC, id DESC)"},
		{name: "idx_platform_tasks_owner_updated_at", columns: "(owner_user_id, updated_at DESC, id DESC)"},
	} {
		t.Run(requirement.name, func(t *testing.T) {
			var definition string
			require.NoError(t, db.Raw(`
SELECT indexdef
FROM pg_catalog.pg_indexes
WHERE schemaname = current_schema() AND tablename = 'platform_tasks' AND indexname = ?`, requirement.name).Scan(&definition).Error)
			assert.Contains(t, definition, requirement.columns)
		})
	}
}

type mcpConnectionSchemaTestIndexRequirement struct {
	table               string
	name                string
	columns             []string
	unique              bool
	incompatibleColumns []string
}

func (requirement mcpConnectionSchemaTestIndexRequirement) incompatibleCreateStatement() string {
	statement := "CREATE INDEX"
	if requirement.unique {
		statement = "CREATE UNIQUE INDEX"
	}
	return fmt.Sprintf(
		"%s %s ON %s(%s)",
		statement,
		requirement.name,
		requirement.table,
		strings.Join(requirement.incompatibleColumns, ", "),
	)
}

func replaceMCPIndexWithDeferrableUniqueConstraint(t *testing.T, db *gorm.DB, requirement mcpConnectionSchemaTestIndexRequirement) {
	t.Helper()
	require.True(t, requirement.unique)
	require.NoError(t, db.Exec("DROP INDEX "+requirement.name).Error)
	require.NoError(t, db.Exec(fmt.Sprintf(
		"ALTER TABLE %s ADD CONSTRAINT %s UNIQUE (%s) DEFERRABLE INITIALLY DEFERRED",
		requirement.table,
		requirement.name,
		strings.Join(requirement.columns, ", "),
	)).Error)
	require.True(t, db.Migrator().HasIndex(requirement.table, requirement.name))

	var immediate bool
	require.NoError(t, db.Raw(`
SELECT index_definition.indimmediate
FROM pg_catalog.pg_index AS index_definition
JOIN pg_catalog.pg_class AS index_name ON index_name.oid = index_definition.indexrelid
JOIN pg_catalog.pg_class AS table_definition ON table_definition.oid = index_definition.indrelid
JOIN pg_catalog.pg_namespace AS table_namespace ON table_namespace.oid = table_definition.relnamespace
WHERE table_namespace.nspname = current_schema()
  AND table_definition.relname = ?
  AND index_name.relname = ?`, requirement.table, requirement.name).Scan(&immediate).Error)
	require.False(t, immediate, "fixture must use a deferrable unique constraint index")
}

var mcpConnectionSchemaTestIndexRequirements = []mcpConnectionSchemaTestIndexRequirement{
	{
		table: "platform_mcp_connection_configs", name: "idx_platform_mcp_connection_configs_owner_scope",
		columns: []string{"owner_user_id", "scope"}, incompatibleColumns: []string{"enabled"},
	},
	{
		table: "platform_mcp_connection_versions", name: "ux_platform_mcp_connection_versions_config_version", unique: true,
		columns: []string{"connection_config_id", "version"}, incompatibleColumns: []string{"id"},
	},
	{
		table: "platform_mcp_task_bindings", name: "ux_platform_mcp_task_bindings_task_id", unique: true,
		columns: []string{"task_id"}, incompatibleColumns: []string{"id"},
	},
	{
		table: "platform_mcp_task_bindings", name: "idx_platform_mcp_task_bindings_config_version",
		columns: []string{"connection_config_id", "connection_config_version"}, incompatibleColumns: []string{"task_id"},
	},
	{
		table: "platform_mcp_runtime_capabilities", name: "ux_platform_mcp_runtime_capabilities_task_rotation", unique: true,
		columns: []string{"task_id", "rotation"}, incompatibleColumns: []string{"id"},
	},
	{
		table: "platform_mcp_runtime_capabilities", name: "ux_platform_mcp_runtime_capabilities_hash", unique: true,
		columns: []string{"capability_hash"}, incompatibleColumns: []string{"id"},
	},
	{
		table: "platform_mcp_runtime_capabilities", name: "idx_platform_mcp_runtime_capabilities_expires_at",
		columns: []string{"expires_at"}, incompatibleColumns: []string{"task_id"},
	},
	{
		table: "platform_idempotency_records", name: "ux_platform_idempotency_records_scope", unique: true,
		columns: []string{"principal_id", "scope_key", "method", "path", "idempotency_key"}, incompatibleColumns: []string{"id"},
	},
	{
		table: "platform_idempotency_records", name: "idx_platform_idempotency_records_expires_at",
		columns: []string{"expires_at"}, incompatibleColumns: []string{"principal_id"},
	},
}

func dropMCPConnectionSchemaTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Migrator().DropTable(
		"platform_idempotency_records",
		"platform_mcp_runtime_capabilities",
		"platform_mcp_task_bindings",
		"platform_mcp_connection_versions",
		"platform_mcp_connection_configs",
	))
}

func assertMCPConnectionSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	for table, columns := range map[string][]string{
		"platform_mcp_connection_configs": {
			"id", "owner_user_id", "scope", "name", "description", "current_version", "resource_revision", "enabled", "last_probe_started_at", "created_at", "updated_at",
		},
		"platform_mcp_connection_versions": {
			"id", "connection_config_id", "version", "encrypted_payload", "payload_nonce", "key_id", "transport", "detected_transport", "probe_status", "created_at",
		},
		"platform_mcp_task_bindings": {
			"id", "task_id", "source_kind", "connection_config_id", "connection_config_version", "encrypted_repository_url", "repository_url_nonce", "repository_url_key_id", "created_at", "updated_at",
		},
		"platform_mcp_runtime_capabilities": {
			"id", "task_id", "capability_hash", "issued_at", "expires_at", "rotation", "version", "created_at",
		},
		"platform_idempotency_records": {
			"id", "principal_id", "scope_key", "method", "path", "idempotency_key", "payload_hash", "status_code", "safe_response", "expires_at", "created_at",
		},
	} {
		t.Run(table, func(t *testing.T) {
			assert.True(t, db.Migrator().HasTable(table))
			for _, column := range columns {
				assert.Truef(t, db.Migrator().HasColumn(table, column), "missing column %s.%s", table, column)
			}
		})
	}

	for _, requirement := range mcpConnectionSchemaTestIndexRequirements {
		t.Run(requirement.name, func(t *testing.T) {
			var index struct {
				Unique     bool   `gorm:"column:unique"`
				Valid      bool   `gorm:"column:valid"`
				Ready      bool   `gorm:"column:ready"`
				Definition string `gorm:"column:definition"`
			}
			require.NoError(t, db.Raw(`

SELECT index_definition.indisunique AS unique,
       index_definition.indisvalid AS valid,
       index_definition.indisready AS ready,
       pg_get_indexdef(index_definition.indexrelid) AS definition
FROM pg_catalog.pg_class AS table_definition
JOIN pg_catalog.pg_namespace AS table_namespace ON table_namespace.oid = table_definition.relnamespace
JOIN pg_catalog.pg_index AS index_definition ON index_definition.indrelid = table_definition.oid
JOIN pg_catalog.pg_class AS index_name ON index_name.oid = index_definition.indexrelid
WHERE table_namespace.nspname = current_schema()
  AND table_definition.relname = ?
  AND index_name.relname = ?`, requirement.table, requirement.name).Scan(&index).Error)
			assert.Equal(t, requirement.unique, index.Unique)
			assert.True(t, index.Valid)
			assert.True(t, index.Ready)
			assert.Contains(t, index.Definition, "("+strings.Join(requirement.columns, ", ")+")")
		})
	}
}
