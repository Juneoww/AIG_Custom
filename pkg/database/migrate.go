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
	"time"

	"gorm.io/gorm"
)

// SchemaMigration 记录已经成功执行的数据库迁移版本。
type SchemaMigration struct {
	Version   int64     `gorm:"primaryKey"`
	AppliedAt time.Time `gorm:"not null"`
}

func (SchemaMigration) TableName() string {
	return "schema_migrations"
}

type migration struct {
	version int64
	apply   func(*gorm.DB) error
}

var migrations = []migration{
	{version: 1, apply: migrateInitialSchema},
	{version: 2, apply: migrateIdentitySchema},
	{version: 3, apply: migrateGovernanceSchema},
	{version: 4, apply: migrateAuditCompletionSchema},
	{version: 5, apply: migratePlatformTaskSchema},
	{version: 6, apply: migratePlatformTaskDispatchClaimSchema},
	{version: 7, apply: migrateReportSchema},
	{version: 8, apply: migratePlatformTaskDashboardIndexes},
	{version: 9, apply: migratePlatformAttachmentLifecycle},
	{version: 10, apply: migratePlatformTaskRemarkAndTargetCount},
	{version: 11, apply: migratePlatformMCPConnectionSchema},
	{version: 12, apply: migratePlatformMCPCompatibilitySchema},
}

const migrationAdvisoryLockKey int64 = 301237729

// Migrate 按版本顺序执行尚未完成的数据库迁移。
func Migrate(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("数据库连接不能为空")
	}
	return db.Connection(func(lockedDB *gorm.DB) (err error) {
		if err := lockedDB.Exec("SELECT pg_advisory_lock(?)", migrationAdvisoryLockKey).Error; err != nil {
			return fmt.Errorf("获取数据库迁移锁失败: %w", err)
		}
		defer func() {
			if unlockErr := lockedDB.Exec("SELECT pg_advisory_unlock(?)", migrationAdvisoryLockKey).Error; unlockErr != nil && err == nil {
				err = fmt.Errorf("释放数据库迁移锁失败: %w", unlockErr)
			}
		}()

		return migrateLocked(lockedDB)
	})
}

func migrateLocked(db *gorm.DB) error {
	if !db.Migrator().HasTable(&SchemaMigration{}) {
		if err := db.AutoMigrate(&SchemaMigration{}); err != nil {
			return fmt.Errorf("创建迁移版本表失败: %w", err)
		}
	}

	for _, migration := range migrations {
		if err := db.Transaction(func(tx *gorm.DB) error {
			var count int64
			if err := tx.Model(&SchemaMigration{}).Where("version = ?", migration.version).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return nil
			}
			if err := migration.apply(tx); err != nil {
				return err
			}
			return tx.Create(&SchemaMigration{Version: migration.version, AppliedAt: time.Now().UTC()}).Error
		}); err != nil {
			return fmt.Errorf("执行数据库迁移版本 %d 失败: %w", migration.version, err)
		}
	}

	return nil
}

func migrateInitialSchema(db *gorm.DB) error {
	if err := db.AutoMigrate(&User{}, &Session{}, &TaskMessage{}, &Model{}, &Agent{}); err != nil {
		return err
	}
	if err := NewTaskStore(db).createIndexes(); err != nil {
		return err
	}
	return db.Exec("CREATE INDEX IF NOT EXISTS idx_models_username_created ON models(username, created_at DESC)").Error
}

// Migration-local models keep pkg/database independent from the identity
// repository while creating the exact tables consumed by that package.
type identityUserMigration struct {
	ID                 string    `gorm:"primaryKey;column:id"`
	Username           string    `gorm:"uniqueIndex;not null"`
	PasswordHash       string    `gorm:"not null"`
	Role               string    `gorm:"not null"`
	Active             bool      `gorm:"not null;default:true"`
	MustChangePassword bool      `gorm:"not null;default:true"`
	CreatedAt          time.Time `gorm:"not null"`
	UpdatedAt          time.Time `gorm:"not null"`
}

func (identityUserMigration) TableName() string { return "identity_users" }

type identitySessionMigration struct {
	ID        string     `gorm:"primaryKey;column:id"`
	UserID    string     `gorm:"index;not null"`
	TokenHash string     `gorm:"uniqueIndex;not null"`
	ExpiresAt time.Time  `gorm:"index;not null"`
	RevokedAt *time.Time `gorm:"index"`
	CreatedAt time.Time  `gorm:"not null"`
}

func (identitySessionMigration) TableName() string { return "identity_sessions" }

type identityPasswordResetMigration struct {
	ID        string     `gorm:"primaryKey;column:id"`
	UserID    string     `gorm:"index;not null"`
	TokenHash string     `gorm:"uniqueIndex;not null"`
	ExpiresAt time.Time  `gorm:"index;not null"`
	UsedAt    *time.Time `gorm:"index"`
	CreatedAt time.Time  `gorm:"not null"`
}

func (identityPasswordResetMigration) TableName() string { return "identity_password_resets" }

func migrateIdentitySchema(db *gorm.DB) error {
	return db.AutoMigrate(
		&identityUserMigration{},
		&identitySessionMigration{},
		&identityPasswordResetMigration{},
	)
}

func migrateGovernanceSchema(db *gorm.DB) error {
	return db.AutoMigrate(
		&governanceAuditEventMigration{},
		&governanceModelMigration{},
	)
}

type governanceAuditEventMigration struct {
	ID            string    `gorm:"primaryKey;column:id"`
	OccurredAt    time.Time `gorm:"index;not null"`
	ActorUserID   string    `gorm:"index"`
	ActorUsername string    `gorm:"index"`
	ActorRole     string    `gorm:"index"`
	Action        string    `gorm:"index;not null"`
	ResourceType  string    `gorm:"index"`
	ResourceID    string    `gorm:"index"`
	Outcome       string    `gorm:"index;not null"`
	ClientIP      string
	RequestID     string          `gorm:"index"`
	Metadata      json.RawMessage `gorm:"type:jsonb;not null"`
}

func (governanceAuditEventMigration) TableName() string { return "audit_events" }

type governanceAuditCompletionMigration struct {
	ID            string `gorm:"primaryKey;column:id"`
	EventID       string `gorm:"uniqueIndex;not null"`
	RequestID     string `gorm:"uniqueIndex;not null"`
	ActorUserID   string `gorm:"index"`
	ActorUsername string `gorm:"index"`
	ActorRole     string `gorm:"index"`
	Action        string `gorm:"index;not null"`
	ResourceType  string `gorm:"index"`
	ResourceID    string `gorm:"index"`
	Outcome       string `gorm:"index;not null"`
	ClientIP      string
	Metadata      json.RawMessage `gorm:"type:jsonb;not null"`
	State         string          `gorm:"index;not null;default:ready"`
	CreatedAt     time.Time       `gorm:"index;not null"`
	ReadyAt       *time.Time      `gorm:"index"`
	Attempts      int             `gorm:"not null;default:0"`
	DeliveredAt   *time.Time      `gorm:"index"`
}

func (governanceAuditCompletionMigration) TableName() string { return "audit_completion_outbox" }

func migrateAuditCompletionSchema(db *gorm.DB) error {
	if !db.Migrator().HasTable(&governanceAuditCompletionMigration{}) {
		if err := db.AutoMigrate(&governanceAuditCompletionMigration{}); err != nil {
			return err
		}
	}
	// Version 0c6b created this table inside v3 with a non-unique request_id
	// index and random completion IDs. Retried writes could therefore leave
	// duplicates. Repair that released shape explicitly before replacing the
	// index; GORM must not infer whether the existing same-named index is unique.
	statements := []string{
		`ALTER TABLE audit_completion_outbox ADD COLUMN IF NOT EXISTS state text`,
		`ALTER TABLE audit_completion_outbox ADD COLUMN IF NOT EXISTS ready_at timestamp with time zone`,
		`UPDATE audit_completion_outbox SET state = 'ready' WHERE state IS NULL OR btrim(state) = ''`,
		`UPDATE audit_completion_outbox SET ready_at = created_at WHERE state = 'ready' AND ready_at IS NULL`,
		`ALTER TABLE audit_completion_outbox ALTER COLUMN state SET DEFAULT 'ready'`,
		`ALTER TABLE audit_completion_outbox ALTER COLUMN state SET NOT NULL`,
		`WITH ranked AS (
			SELECT id, row_number() OVER (
				PARTITION BY request_id
				ORDER BY (delivered_at IS NOT NULL) DESC,
				         delivered_at DESC NULLS LAST,
				         created_at DESC,
				         id DESC
			) AS row_number
			FROM audit_completion_outbox
		)
		DELETE FROM audit_completion_outbox AS completion
		USING ranked
		WHERE completion.id = ranked.id AND ranked.row_number > 1`,
		`DROP INDEX IF EXISTS idx_audit_completion_outbox_request_id`,
		`CREATE UNIQUE INDEX idx_audit_completion_outbox_request_id ON audit_completion_outbox(request_id)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_completion_outbox_state ON audit_completion_outbox(state)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_completion_outbox_ready_at ON audit_completion_outbox(ready_at)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

type governanceModelMigration struct {
	ID             string `gorm:"primaryKey;column:id"`
	OwnerUserID    string `gorm:"index"`
	Scope          string `gorm:"index;not null"`
	Name           string `gorm:"not null"`
	ProviderModel  string `gorm:"not null"`
	BaseURL        string `gorm:"not null"`
	Note           string
	Limit          int
	Disabled       bool      `gorm:"not null;default:false"`
	EncryptedToken []byte    `gorm:"not null"`
	TokenNonce     []byte    `gorm:"not null"`
	KeyID          string    `gorm:"not null"`
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func (governanceModelMigration) TableName() string { return "platform_models" }

type platformTaskMigration struct {
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
	DispatchLeaseUntil *time.Time      `gorm:"column:dispatch_lease_until"`
	CreatedAt          time.Time       `gorm:"not null;column:created_at"`
	UpdatedAt          time.Time       `gorm:"not null;column:updated_at"`
}

func (platformTaskMigration) TableName() string { return "platform_tasks" }

type platformAttachmentMigration struct {
	ID           string    `gorm:"primaryKey;column:id"`
	OwnerUserID  string    `gorm:"not null;column:owner_user_id"`
	OriginalName string    `gorm:"not null;column:original_name"`
	StorageName  string    `gorm:"not null;column:storage_name"`
	Size         int64     `gorm:"not null;column:size"`
	ChunkBytes   int64     `gorm:"not null;column:chunk_bytes"`
	State        string    `gorm:"not null;column:state"`
	CreatedAt    time.Time `gorm:"not null;column:created_at"`
	UpdatedAt    time.Time `gorm:"not null;column:updated_at"`
}

func (platformAttachmentMigration) TableName() string { return "platform_attachments" }

func migratePlatformTaskSchema(db *gorm.DB) error {
	if err := db.AutoMigrate(&platformTaskMigration{}, &platformAttachmentMigration{}); err != nil {
		return err
	}
	statements := []string{
		`CREATE UNIQUE INDEX idx_platform_tasks_owner_idempotency ON platform_tasks(owner_user_id, idempotency_key)`,
		`CREATE UNIQUE INDEX idx_platform_tasks_engine_session ON platform_tasks(engine_session_id)`,
		`CREATE INDEX idx_platform_tasks_owner_created ON platform_tasks(owner_user_id, created_at DESC)`,
		`CREATE INDEX idx_platform_tasks_status ON platform_tasks(status)`,
		`CREATE INDEX idx_platform_attachments_owner_created ON platform_attachments(owner_user_id, created_at DESC)`,
		`CREATE UNIQUE INDEX idx_platform_attachments_storage_name ON platform_attachments(storage_name)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePlatformTaskDispatchClaimSchema(db *gorm.DB) error {
	return db.Exec(`ALTER TABLE platform_tasks ADD COLUMN IF NOT EXISTS dispatch_claim_token text NOT NULL DEFAULT ''`).Error
}

type reportSnapshotMigration struct {
	ID            string          `gorm:"primaryKey;column:id"`
	TaskID        string          `gorm:"not null;uniqueIndex:ux_report_snapshots_task_id;column:task_id"`
	OwnerUserID   string          `gorm:"not null;index;column:owner_user_id"`
	TaskType      string          `gorm:"not null;column:task_type"`
	CompletedAt   time.Time       `gorm:"not null;column:completed_at"`
	CreatedAt     time.Time       `gorm:"not null;column:created_at"`
	RawResult     json.RawMessage `gorm:"type:jsonb;not null;column:raw_result"`
	RiskSummary   json.RawMessage `gorm:"type:jsonb;not null;column:risk_summary"`
	RenderData    json.RawMessage `gorm:"type:jsonb;not null;column:render_data"`
	BrandSnapshot json.RawMessage `gorm:"type:jsonb;not null;column:brand_snapshot"`
}

func (reportSnapshotMigration) TableName() string { return "report_snapshots" }

type reportBrandSettingMigration struct {
	ID           string    `gorm:"primaryKey;column:id"`
	ProductName  string    `gorm:"not null;column:product_name"`
	PrimaryColor string    `gorm:"not null;column:primary_color"`
	Logo         []byte    `gorm:"not null;column:logo"`
	LogoMIME     string    `gorm:"not null;column:logo_mime"`
	Watermark    string    `gorm:"not null;column:watermark"`
	UpdatedBy    string    `gorm:"not null;column:updated_by"`
	UpdatedAt    time.Time `gorm:"not null;column:updated_at"`
}

func (reportBrandSettingMigration) TableName() string { return "report_brand_settings" }

func migrateReportSchema(db *gorm.DB) error {
	if err := db.AutoMigrate(&reportSnapshotMigration{}, &reportBrandSettingMigration{}); err != nil {
		return err
	}
	for _, statement := range []string{
		`CREATE INDEX idx_report_snapshots_completed_at ON report_snapshots(completed_at DESC)`,
		`CREATE INDEX idx_report_snapshots_owner_completed_at ON report_snapshots(owner_user_id, completed_at DESC)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePlatformTaskDashboardIndexes(db *gorm.DB) error {
	for _, statement := range []string{
		`CREATE INDEX idx_platform_tasks_updated_at ON platform_tasks(updated_at DESC, id DESC)`,
		`CREATE INDEX idx_platform_tasks_owner_updated_at ON platform_tasks(owner_user_id, updated_at DESC, id DESC)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePlatformAttachmentLifecycle(db *gorm.DB) error {
	return db.Exec(`
UPDATE platform_attachments AS attachment
SET state = 'attached'
WHERE attachment.state = 'ready'
  AND EXISTS (
    SELECT 1
    FROM platform_tasks AS task
    CROSS JOIN LATERAL jsonb_array_elements_text(
      CASE
        WHEN jsonb_typeof(task.attachment_refs) = 'array' THEN task.attachment_refs
        ELSE '[]'::jsonb
      END
    ) AS reference(value)
    WHERE reference.value = attachment.id
  )`).Error
}

// 以下结构只描述 MCP 迁移所需的物理表，避免 pkg/database 依赖平台业务包。
// 敏感连接信息只允许以密文、nonce 和密钥标识落库，禁止在此处新增明文字段。
type platformMCPConnectionConfigMigration struct {
	ID               string    `gorm:"primaryKey;column:id"`
	OwnerUserID      string    `gorm:"not null;column:owner_user_id"`
	Scope            string    `gorm:"not null;column:scope"`
	Name             string    `gorm:"not null;column:name"`
	Description      string    `gorm:"not null;default:'';column:description"`
	CurrentVersion   int       `gorm:"not null;default:0;column:current_version"`
	ResourceRevision string    `gorm:"not null;default:'';column:resource_revision"`
	Enabled          bool      `gorm:"not null;default:true;column:enabled"`
	CreatedAt        time.Time `gorm:"not null;column:created_at"`
	UpdatedAt        time.Time `gorm:"not null;column:updated_at"`
}

func (platformMCPConnectionConfigMigration) TableName() string {
	return "platform_mcp_connection_configs"
}

type platformMCPConnectionVersionMigration struct {
	ID                 string    `gorm:"primaryKey;column:id"`
	ConnectionConfigID string    `gorm:"not null;column:connection_config_id"`
	Version            int       `gorm:"not null;column:version"`
	EncryptedPayload   []byte    `gorm:"not null;column:encrypted_payload"`
	PayloadNonce       []byte    `gorm:"not null;column:payload_nonce"`
	KeyID              string    `gorm:"not null;column:key_id"`
	Transport          string    `gorm:"not null;column:transport"`
	DetectedTransport  string    `gorm:"not null;default:'';column:detected_transport"`
	ProbeStatus        string    `gorm:"not null;default:'pending';column:probe_status"`
	CreatedAt          time.Time `gorm:"not null;column:created_at"`
}

func (platformMCPConnectionVersionMigration) TableName() string {
	return "platform_mcp_connection_versions"
}

type platformMCPTaskBindingMigration struct {
	ID                      string    `gorm:"primaryKey;column:id"`
	TaskID                  string    `gorm:"not null;column:task_id"`
	SourceKind              string    `gorm:"not null;column:source_kind"`
	ConnectionConfigID      *string   `gorm:"column:connection_config_id"`
	ConnectionConfigVersion *int      `gorm:"column:connection_config_version"`
	EncryptedRepositoryURL  []byte    `gorm:"column:encrypted_repository_url"`
	RepositoryURLNonce      []byte    `gorm:"column:repository_url_nonce"`
	RepositoryURLKeyID      string    `gorm:"column:repository_url_key_id"`
	CreatedAt               time.Time `gorm:"not null;column:created_at"`
	UpdatedAt               time.Time `gorm:"not null;column:updated_at"`
}

func (platformMCPTaskBindingMigration) TableName() string {
	return "platform_mcp_task_bindings"
}

type platformMCPRuntimeCapabilityMigration struct {
	ID             string    `gorm:"primaryKey;column:id"`
	TaskID         string    `gorm:"not null;column:task_id"`
	CapabilityHash []byte    `gorm:"not null;column:capability_hash"`
	IssuedAt       time.Time `gorm:"not null;column:issued_at"`
	ExpiresAt      time.Time `gorm:"not null;column:expires_at"`
	Rotation       int       `gorm:"not null;column:rotation"`
	Version        int       `gorm:"not null;default:1;column:version"`
	CreatedAt      time.Time `gorm:"not null;column:created_at"`
}

func (platformMCPRuntimeCapabilityMigration) TableName() string {
	return "platform_mcp_runtime_capabilities"
}

type platformIdempotencyRecordMigration struct {
	ID             string          `gorm:"primaryKey;column:id"`
	PrincipalID    string          `gorm:"not null;column:principal_id"`
	ScopeKey       string          `gorm:"not null;column:scope_key"`
	Method         string          `gorm:"not null;column:method"`
	Path           string          `gorm:"not null;column:path"`
	IdempotencyKey string          `gorm:"not null;column:idempotency_key"`
	PayloadHash    []byte          `gorm:"not null;column:payload_hash"`
	StatusCode     int             `gorm:"not null;column:status_code"`
	SafeResponse   json.RawMessage `gorm:"type:jsonb;not null;column:safe_response"`
	ExpiresAt      time.Time       `gorm:"not null;column:expires_at"`
	CreatedAt      time.Time       `gorm:"not null;column:created_at"`
}

func (platformIdempotencyRecordMigration) TableName() string {
	return "platform_idempotency_records"
}

// mcpConnectionSchemaIndexRequirement 固定 v10 MCP 表的索引语义。
// 迁移与运行时均以这些列序和唯一性要求验证 catalog，避免同名错误索引被接受。
type mcpConnectionSchemaIndexRequirement struct {
	table     string
	name      string
	columns   []string
	unique    bool
	statement string
}

var mcpConnectionSchemaIndexRequirements = []mcpConnectionSchemaIndexRequirement{
	{
		table: "platform_mcp_connection_configs", name: "idx_platform_mcp_connection_configs_owner_scope",
		columns:   []string{"owner_user_id", "scope"},
		statement: `CREATE INDEX IF NOT EXISTS idx_platform_mcp_connection_configs_owner_scope ON platform_mcp_connection_configs(owner_user_id, scope)`,
	},
	{
		table: "platform_mcp_connection_versions", name: "ux_platform_mcp_connection_versions_config_version", unique: true,
		columns:   []string{"connection_config_id", "version"},
		statement: `CREATE UNIQUE INDEX IF NOT EXISTS ux_platform_mcp_connection_versions_config_version ON platform_mcp_connection_versions(connection_config_id, version)`,
	},
	{
		table: "platform_mcp_task_bindings", name: "ux_platform_mcp_task_bindings_task_id", unique: true,
		columns:   []string{"task_id"},
		statement: `CREATE UNIQUE INDEX IF NOT EXISTS ux_platform_mcp_task_bindings_task_id ON platform_mcp_task_bindings(task_id)`,
	},
	{
		table: "platform_mcp_task_bindings", name: "idx_platform_mcp_task_bindings_config_version",
		columns:   []string{"connection_config_id", "connection_config_version"},
		statement: `CREATE INDEX IF NOT EXISTS idx_platform_mcp_task_bindings_config_version ON platform_mcp_task_bindings(connection_config_id, connection_config_version)`,
	},
	{
		table: "platform_mcp_runtime_capabilities", name: "ux_platform_mcp_runtime_capabilities_task_rotation", unique: true,
		columns:   []string{"task_id", "rotation"},
		statement: `CREATE UNIQUE INDEX IF NOT EXISTS ux_platform_mcp_runtime_capabilities_task_rotation ON platform_mcp_runtime_capabilities(task_id, rotation)`,
	},
	{
		table: "platform_mcp_runtime_capabilities", name: "ux_platform_mcp_runtime_capabilities_hash", unique: true,
		columns:   []string{"capability_hash"},
		statement: `CREATE UNIQUE INDEX IF NOT EXISTS ux_platform_mcp_runtime_capabilities_hash ON platform_mcp_runtime_capabilities(capability_hash)`,
	},
	{
		table: "platform_mcp_runtime_capabilities", name: "idx_platform_mcp_runtime_capabilities_expires_at",
		columns:   []string{"expires_at"},
		statement: `CREATE INDEX IF NOT EXISTS idx_platform_mcp_runtime_capabilities_expires_at ON platform_mcp_runtime_capabilities(expires_at)`,
	},
	{
		table: "platform_idempotency_records", name: "ux_platform_idempotency_records_scope", unique: true,
		columns:   []string{"principal_id", "scope_key", "method", "path", "idempotency_key"},
		statement: `CREATE UNIQUE INDEX IF NOT EXISTS ux_platform_idempotency_records_scope ON platform_idempotency_records(principal_id, scope_key, method, path, idempotency_key)`,
	},
	{
		table: "platform_idempotency_records", name: "idx_platform_idempotency_records_expires_at",
		columns:   []string{"expires_at"},
		statement: `CREATE INDEX IF NOT EXISTS idx_platform_idempotency_records_expires_at ON platform_idempotency_records(expires_at)`,
	},
}

// migratePlatformMCPConnectionSchema 建立 MCP 扫描的专用持久化边界。
// 该迁移只会由显式 Migrate 调用，运行时校验不得补建任何对象。
func migratePlatformMCPConnectionSchema(db *gorm.DB) error {
	for _, target := range []struct {
		table   string
		model   any
		columns []string
	}{
		{
			table: "platform_mcp_connection_configs", model: &platformMCPConnectionConfigMigration{},
			columns: []string{"id", "owner_user_id", "scope", "name", "description", "current_version", "resource_revision", "enabled", "created_at", "updated_at"},
		},
		{
			table: "platform_mcp_connection_versions", model: &platformMCPConnectionVersionMigration{},
			columns: []string{"id", "connection_config_id", "version", "encrypted_payload", "payload_nonce", "key_id", "transport", "detected_transport", "probe_status", "created_at"},
		},
		{
			table: "platform_mcp_task_bindings", model: &platformMCPTaskBindingMigration{},
			columns: []string{"id", "task_id", "source_kind", "connection_config_id", "connection_config_version", "encrypted_repository_url", "repository_url_nonce", "repository_url_key_id", "created_at", "updated_at"},
		},
		{
			table: "platform_mcp_runtime_capabilities", model: &platformMCPRuntimeCapabilityMigration{},
			columns: []string{"id", "task_id", "capability_hash", "issued_at", "expires_at", "rotation", "version", "created_at"},
		},
		{
			table: "platform_idempotency_records", model: &platformIdempotencyRecordMigration{},
			columns: []string{"id", "principal_id", "scope_key", "method", "path", "idempotency_key", "payload_hash", "status_code", "safe_response", "expires_at", "created_at"},
		},
	} {
		if !db.Migrator().HasTable(target.model) {
			if err := db.Migrator().CreateTable(target.model); err != nil {
				return err
			}
		}
		for _, column := range target.columns {
			if !db.Migrator().HasColumn(target.table, column) {
				return fmt.Errorf("%s 缺少列 %s", target.table, column)
			}
		}
	}

	for _, requirement := range mcpConnectionSchemaIndexRequirements {
		if err := db.Exec(requirement.statement).Error; err != nil {
			return err
		}
		valid, err := postgresRuntimeIndexValid(db, requirement.table, requirement.name, requirement.columns, requirement.unique)
		if err != nil {
			return fmt.Errorf("读取索引 %s 失败: %w", requirement.name, err)
		}
		if !valid {
			return fmt.Errorf("索引 %s 与 v10 定义不兼容", requirement.name)
		}
	}
	return nil
}

// migratePlatformMCPProbeRateLimitSchema 仅升级已发布的 v10 MCP 配置表。不能把
// 该列回填到 v10 建表路径，否则已经部署的 v10 数据库无法获得显式升级记录。
func migratePlatformMCPProbeRateLimitSchema(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("MCP 探测限流迁移数据库不能为空")
	}
	const table = "platform_mcp_connection_configs"
	if !db.Migrator().HasTable(table) {
		return fmt.Errorf("v11 MCP 探测限流迁移缺少表 %s", table)
	}
	if err := db.Exec(`ALTER TABLE platform_mcp_connection_configs ADD COLUMN IF NOT EXISTS last_probe_started_at TIMESTAMPTZ`).Error; err != nil {
		return fmt.Errorf("添加 MCP 探测限流列失败: %w", err)
	}
	if !db.Migrator().HasColumn(table, "last_probe_started_at") {
		return fmt.Errorf("%s 缺少列 last_probe_started_at", table)
	}
	return nil
}

// 开发分支曾以 v10/v11 保存 MCP 结构；v12 同时补齐已发布 develop 的字段。
// 迁移函数本身幂等，因此两种历史都保留原数据并收敛到相同结构。
func migratePlatformMCPCompatibilitySchema(db *gorm.DB) error {
	if err := migratePlatformTaskRemarkAndTargetCount(db); err != nil {
		return err
	}
	if err := migratePlatformMCPConnectionSchema(db); err != nil {
		return err
	}
	return migratePlatformMCPProbeRateLimitSchema(db)
}

func migratePlatformTaskRemarkAndTargetCount(db *gorm.DB) error {
	for _, statement := range []string{
		`ALTER TABLE platform_tasks ADD COLUMN IF NOT EXISTS remark text NOT NULL DEFAULT ''`,
		`ALTER TABLE platform_tasks ADD COLUMN IF NOT EXISTS target_count integer NOT NULL DEFAULT 0`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}
