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

// releasedV9PlatformTaskMigration 固定已发布 v9 的 platform_tasks 结构，不能随当前迁移模型演进。
type releasedV9PlatformTaskMigration struct {
	ID                 string          `gorm:"primaryKey;column:id"`
	OwnerUserID        string          `gorm:"not null;column:owner_user_id"`
	OwnerUsername      string          `gorm:"not null;column:owner_username"`
	IdempotencyKey     string          `gorm:"not null;column:idempotency_key"`
	EngineSessionID    string          `gorm:"not null;column:engine_session_id"`
	TaskType           string          `gorm:"not null;column:task_type"`
	Content            string          `gorm:"not null;column:content"`
	Params             json.RawMessage `gorm:"type:jsonb;not null;column:params"`
	AttachmentRefs     json.RawMessage `gorm:"type:jsonb;not null;column:attachment_refs"`
	CountryIsoCode     string          `gorm:"column:country_iso_code"`
	Status             string          `gorm:"not null;column:status"`
	DispatchError      string          `gorm:"not null;column:dispatch_error"`
	DispatchAttempts   int             `gorm:"not null;column:dispatch_attempts"`
	DispatchClaimToken string          `gorm:"not null;default:'';column:dispatch_claim_token"`
	DispatchLeaseUntil *time.Time      `gorm:"column:dispatch_lease_until"`
	CreatedAt          time.Time       `gorm:"not null;column:created_at"`
	UpdatedAt          time.Time       `gorm:"not null;column:updated_at"`
}

func (releasedV9PlatformTaskMigration) TableName() string { return "platform_tasks" }

func TestMigrationAppliesIdentitySchemaAsVersionTwo(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)

	require.NoError(t, Migrate(db))
	assert.True(t, db.Migrator().HasTable(&identity.User{}))
	assert.True(t, db.Migrator().HasTable(&identity.Session{}))
	assert.True(t, db.Migrator().HasTable(&identity.PasswordReset{}))

	var versions []SchemaMigration
	require.NoError(t, db.Order("version ASC").Find(&versions).Error)
	require.Len(t, versions, 10)
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
}

func TestMigrationAppliesGovernanceAndPlatformTaskSchemaThroughVersionTen(t *testing.T) {
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
	assert.True(t, db.Migrator().HasColumn("platform_tasks", "remark"))
	assert.True(t, db.Migrator().HasColumn("platform_tasks", "target_count"))
	assertReportTrendIndexDefinitions(t, db)
	assertPlatformTaskDashboardIndexDefinitions(t, db)

	var versions []SchemaMigration
	require.NoError(t, db.Order("version ASC").Find(&versions).Error)
	require.Len(t, versions, 10)
	assert.Equal(t, int64(3), versions[2].Version)
	assert.Equal(t, int64(4), versions[3].Version)
	assert.Equal(t, int64(5), versions[4].Version)
	assert.Equal(t, int64(6), versions[5].Version)
	assert.Equal(t, int64(7), versions[6].Version)
	assert.Equal(t, int64(8), versions[7].Version)
	assert.Equal(t, int64(9), versions[8].Version)
	assert.Equal(t, int64(10), versions[9].Version)
}

func TestMigrationVersionFiveLeavesRemarkAndTargetCountForVersionTen(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)
	require.NoError(t, migratePlatformTaskSchema(db))

	assert.False(t, db.Migrator().HasColumn("platform_tasks", "remark"))
	assert.False(t, db.Migrator().HasColumn("platform_tasks", "target_count"))
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
	require.Len(t, versions, 10)
	assert.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, []int64{versions[0].Version, versions[1].Version, versions[2].Version, versions[3].Version, versions[4].Version, versions[5].Version, versions[6].Version, versions[7].Version, versions[8].Version, versions[9].Version})
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
	require.Len(t, versions, 10)
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
	require.Len(t, versions, 10)
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
	assert.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, migrationVersions(t, db))
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
	require.NoError(t, Migrate(db), "v7 through v10 upgrades must remain idempotent")
	assert.True(t, db.Migrator().HasTable("report_snapshots"))
	assert.True(t, db.Migrator().HasTable("report_brand_settings"))
	assertReportTrendIndexDefinitions(t, db)
	assertPlatformTaskDashboardIndexDefinitions(t, db)
	assert.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, migrationVersions(t, db))
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
	require.NoError(t, Migrate(db), "v8 through v10 upgrades must remain idempotent")
	assertPlatformTaskDashboardIndexDefinitions(t, db)
	assert.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, migrationVersions(t, db))
}

func TestMigrationUpgradesReleasedVersionNineWithRemarkAndTargetCount(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)
	require.NoError(t, db.AutoMigrate(&SchemaMigration{}))
	for _, apply := range []func(*gorm.DB) error{
		migrateInitialSchema,
		migrateIdentitySchema,
		migrateGovernanceSchema,
		migrateAuditCompletionSchema,
	} {
		require.NoError(t, apply(db))
	}
	require.NoError(t, db.AutoMigrate(&releasedV9PlatformTaskMigration{}, &platformAttachmentMigration{}))
	for _, statement := range []string{
		`CREATE UNIQUE INDEX idx_platform_tasks_owner_idempotency ON platform_tasks(owner_user_id, idempotency_key)`,
		`CREATE UNIQUE INDEX idx_platform_tasks_engine_session ON platform_tasks(engine_session_id)`,
		`CREATE INDEX idx_platform_tasks_owner_created ON platform_tasks(owner_user_id, created_at DESC)`,
		`CREATE INDEX idx_platform_tasks_status ON platform_tasks(status)`,
		`CREATE INDEX idx_platform_attachments_owner_created ON platform_attachments(owner_user_id, created_at DESC)`,
		`CREATE UNIQUE INDEX idx_platform_attachments_storage_name ON platform_attachments(storage_name)`,
	} {
		require.NoError(t, db.Exec(statement).Error)
	}
	for _, apply := range []func(*gorm.DB) error{
		migratePlatformTaskDispatchClaimSchema,
		migrateReportSchema,
		migratePlatformTaskDashboardIndexes,
		migratePlatformAttachmentLifecycle,
	} {
		require.NoError(t, apply(db))
	}
	for version := int64(1); version <= 9; version++ {
		require.NoError(t, db.Create(&SchemaMigration{Version: version}).Error)
	}
	var dispatchClaimDefault string
	require.NoError(t, db.Raw(`
SELECT column_default
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'platform_tasks'
  AND column_name = 'dispatch_claim_token'`).Scan(&dispatchClaimDefault).Error)
	assert.Contains(t, dispatchClaimDefault, "''", "released v9 dispatch_claim_token must retain its empty-string default")
	now := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	require.NoError(t, db.Table("platform_tasks").Create(map[string]any{
		"id": "released-v9-task", "owner_user_id": "owner", "owner_username": "owner", "idempotency_key": "released-v9",
		"engine_session_id": "engine-v9", "task_type": "ai_infra_scan", "content": "scan", "params": json.RawMessage(`{}`),
		"attachment_refs": json.RawMessage(`[]`), "status": "pending", "dispatch_error": "", "dispatch_attempts": 0,
		"dispatch_claim_token": "", "created_at": now, "updated_at": now,
	}).Error)
	assert.False(t, db.Migrator().HasColumn("platform_tasks", "remark"))
	assert.False(t, db.Migrator().HasColumn("platform_tasks", "target_count"))

	require.NoError(t, Migrate(db))
	require.NoError(t, Migrate(db), "v10 upgrade must remain idempotent")
	assert.True(t, db.Migrator().HasColumn("platform_tasks", "remark"))
	assert.True(t, db.Migrator().HasColumn("platform_tasks", "target_count"))
	var columns []struct {
		Name          string `gorm:"column:column_name"`
		IsNullable    string `gorm:"column:is_nullable"`
		ColumnDefault string `gorm:"column:column_default"`
	}
	require.NoError(t, db.Raw(`
SELECT column_name, is_nullable, column_default
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'platform_tasks'
  AND column_name IN ('remark', 'target_count')
ORDER BY column_name ASC`).Scan(&columns).Error)
	require.Len(t, columns, 2)
	assert.Equal(t, "NO", columns[0].IsNullable)
	assert.Equal(t, "remark", columns[0].Name)
	assert.Contains(t, columns[0].ColumnDefault, "''")
	assert.Equal(t, "NO", columns[1].IsNullable)
	assert.Equal(t, "target_count", columns[1].Name)
	assert.Equal(t, "0", columns[1].ColumnDefault)
	var row struct {
		Remark      string `gorm:"column:remark"`
		TargetCount int    `gorm:"column:target_count"`
	}
	require.NoError(t, db.Table("platform_tasks").Select("remark", "target_count").Where("id = ?", "released-v9-task").Scan(&row).Error)
	assert.Equal(t, "", row.Remark)
	assert.Equal(t, 0, row.TargetCount)
	assert.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, migrationVersions(t, db))
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
	assert.Equal(t, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, migrationVersions(t, db))
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
