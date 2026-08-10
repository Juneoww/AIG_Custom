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

func TestMigrationAppliesIdentitySchemaAsVersionTwo(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)

	require.NoError(t, Migrate(db))
	assert.True(t, db.Migrator().HasTable(&identity.User{}))
	assert.True(t, db.Migrator().HasTable(&identity.Session{}))
	assert.True(t, db.Migrator().HasTable(&identity.PasswordReset{}))

	var versions []SchemaMigration
	require.NoError(t, db.Order("version ASC").Find(&versions).Error)
	require.Len(t, versions, 4)
	assert.Equal(t, int64(1), versions[0].Version)
	assert.Equal(t, int64(2), versions[1].Version)
	assert.Equal(t, int64(3), versions[2].Version)
	assert.Equal(t, int64(4), versions[3].Version)
}

func TestMigrationAppliesGovernanceSchemaThroughVersionFour(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)

	require.NoError(t, Migrate(db))
	assert.True(t, db.Migrator().HasTable("audit_events"))
	assert.True(t, db.Migrator().HasTable("audit_completion_outbox"))
	assert.True(t, db.Migrator().HasTable("platform_models"))

	var versions []SchemaMigration
	require.NoError(t, db.Order("version ASC").Find(&versions).Error)
	require.Len(t, versions, 4)
	assert.Equal(t, int64(3), versions[2].Version)
	assert.Equal(t, int64(4), versions[3].Version)
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
	require.Len(t, versions, 4)
	assert.Equal(t, []int64{1, 2, 3, 4}, []int64{versions[0].Version, versions[1].Version, versions[2].Version, versions[3].Version})
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
	require.Len(t, versions, 4)
	assert.Equal(t, int64(1), versions[0].Version)
	assert.Equal(t, int64(2), versions[1].Version)
	assert.Equal(t, int64(3), versions[2].Version)
	assert.Equal(t, int64(4), versions[3].Version)
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
	require.Len(t, versions, 4)
	assert.Equal(t, int64(1), versions[0].Version)
	assert.Equal(t, int64(2), versions[1].Version)
	assert.Equal(t, int64(3), versions[2].Version)
	assert.Equal(t, int64(4), versions[3].Version)
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
	assert.True(t, first.Migrator().HasIndex(&Model{}, "idx_models_username_created"))
}
