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
	"strings"

	"gorm.io/gorm"
)

// LatestSchemaVersion is the schema version required by the running server.
// Schema changes are applied only by the explicit `aig migrate` command.
const LatestSchemaVersion int64 = 10

var requiredRuntimeTables = []string{
	"users",
	"sessions",
	"task_messages",
	"models",
	"agents",
	"identity_users",
	"identity_sessions",
	"identity_password_resets",
	"audit_events",
	"audit_completion_outbox",
	"platform_models",
	"platform_tasks",
	"platform_attachments",
	"report_snapshots",
	"report_brand_settings",
	"platform_mcp_connection_configs",
	"platform_mcp_connection_versions",
	"platform_mcp_task_bindings",
	"platform_mcp_runtime_capabilities",
	"platform_idempotency_records",
}

var requiredRuntimeColumns = map[string][]string{
	"report_snapshots": {
		"task_id", "owner_user_id", "task_type", "completed_at", "created_at",
		"raw_result", "risk_summary", "render_data", "brand_snapshot",
	},
	"report_brand_settings": {
		"product_name", "primary_color", "logo", "logo_mime", "watermark", "updated_by", "updated_at",
	},
	"platform_mcp_connection_configs": {
		"id", "owner_user_id", "scope", "name", "description", "current_version", "resource_revision", "enabled", "created_at", "updated_at",
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
}

type runtimeIndexRequirement struct {
	model  any
	name   string
	table  string
	unique bool
}

var requiredRuntimeIndexes = []runtimeIndexRequirement{
	{model: &Session{}, name: "idx_sessions_username_created"},
	{model: &Session{}, name: "idx_sessions_username_tasktype"},
	{model: &Session{}, name: "idx_sessions_status"},
	{model: &TaskMessage{}, name: "idx_taskmessages_session_timestamp"},
	{model: &TaskMessage{}, name: "idx_taskmessages_session_type"},
	{model: &Model{}, name: "idx_models_username_created"},
	{model: &governanceAuditCompletionMigration{}, name: "idx_audit_completion_outbox_request_id", table: "audit_completion_outbox", unique: true},
	{model: &governanceAuditCompletionMigration{}, name: "idx_audit_completion_outbox_state"},
	{model: &governanceAuditCompletionMigration{}, name: "idx_audit_completion_outbox_ready_at"},
	{model: &platformTaskMigration{}, name: "idx_platform_tasks_owner_idempotency", table: "platform_tasks", unique: true},
	{model: &platformTaskMigration{}, name: "idx_platform_tasks_engine_session", table: "platform_tasks", unique: true},
	{model: &platformTaskMigration{}, name: "idx_platform_tasks_owner_created"},
	{model: &platformTaskMigration{}, name: "idx_platform_tasks_status"},
	{model: &platformTaskMigration{}, name: "idx_platform_tasks_updated_at"},
	{model: &platformTaskMigration{}, name: "idx_platform_tasks_owner_updated_at"},
	{model: &platformAttachmentMigration{}, name: "idx_platform_attachments_owner_created"},
	{model: &platformAttachmentMigration{}, name: "idx_platform_attachments_storage_name", table: "platform_attachments", unique: true},
	{model: &reportSnapshotMigration{}, name: "ux_report_snapshots_task_id", table: "report_snapshots", unique: true},
	{model: &reportSnapshotMigration{}, name: "idx_report_snapshots_completed_at"},
	{model: &reportSnapshotMigration{}, name: "idx_report_snapshots_owner_completed_at"},
	{model: &platformMCPConnectionConfigMigration{}, name: "idx_platform_mcp_connection_configs_owner_scope"},
	{model: &platformMCPConnectionVersionMigration{}, name: "ux_platform_mcp_connection_versions_config_version", table: "platform_mcp_connection_versions", unique: true},
	{model: &platformMCPTaskBindingMigration{}, name: "ux_platform_mcp_task_bindings_task_id", table: "platform_mcp_task_bindings", unique: true},
	{model: &platformMCPTaskBindingMigration{}, name: "idx_platform_mcp_task_bindings_config_version"},
	{model: &platformMCPRuntimeCapabilityMigration{}, name: "ux_platform_mcp_runtime_capabilities_task_rotation", table: "platform_mcp_runtime_capabilities", unique: true},
	{model: &platformMCPRuntimeCapabilityMigration{}, name: "ux_platform_mcp_runtime_capabilities_hash", table: "platform_mcp_runtime_capabilities", unique: true},
	{model: &platformMCPRuntimeCapabilityMigration{}, name: "idx_platform_mcp_runtime_capabilities_expires_at"},
	{model: &platformIdempotencyRecordMigration{}, name: "ux_platform_idempotency_records_scope", table: "platform_idempotency_records", unique: true},
	{model: &platformIdempotencyRecordMigration{}, name: "idx_platform_idempotency_records_expires_at"},
}

// ValidateRuntimeSchema performs read-only validation of the complete schema
// expected by the server. It never creates tables, indexes, or migration rows.
func ValidateRuntimeSchema(db *gorm.DB) error {
	if db == nil {
		return runtimeMigrationRequiredError("数据库连接为空")
	}
	if !db.Migrator().HasTable(&SchemaMigration{}) {
		return runtimeMigrationRequiredError("缺少 schema_migrations 表")
	}

	var versions []int64
	if err := db.Model(&SchemaMigration{}).Order("version ASC").Pluck("version", &versions).Error; err != nil {
		return runtimeMigrationRequiredError(fmt.Sprintf("读取 schema_migrations 失败: %v", err))
	}
	if len(versions) != int(LatestSchemaVersion) {
		return runtimeMigrationRequiredError(fmt.Sprintf("当前迁移版本为 %v，需要 1-%d", versions, LatestSchemaVersion))
	}
	for index, version := range versions {
		if expected := int64(index + 1); version != expected {
			return runtimeMigrationRequiredError(fmt.Sprintf("当前迁移版本为 %v，需要 1-%d", versions, LatestSchemaVersion))
		}
	}

	missingTables := make([]string, 0)
	for _, table := range requiredRuntimeTables {
		if !db.Migrator().HasTable(table) {
			missingTables = append(missingTables, table)
		}
	}
	if len(missingTables) > 0 {
		return runtimeMigrationRequiredError("缺少表: " + strings.Join(missingTables, ", "))
	}
	if !db.Migrator().HasColumn("platform_tasks", "dispatch_claim_token") {
		return runtimeMigrationRequiredError("platform_tasks 缺少 dispatch_claim_token")
	}
	for table, columns := range requiredRuntimeColumns {
		for _, column := range columns {
			if !db.Migrator().HasColumn(table, column) {
				return runtimeMigrationRequiredError(fmt.Sprintf("缺少列 %s.%s", table, column))
			}
		}
	}

	missingIndexes := make([]string, 0)
	for _, requirement := range requiredRuntimeIndexes {
		if requirement.unique {
			valid, err := postgresRuntimeUniqueIndexValid(db, requirement.table, requirement.name)
			if err != nil {
				return runtimeMigrationRequiredError(fmt.Sprintf("读取索引 %s 失败: %v", requirement.name, err))
			}
			if !valid {
				missingIndexes = append(missingIndexes, requirement.name)
			}
			continue
		}
		if !db.Migrator().HasIndex(requirement.model, requirement.name) {
			missingIndexes = append(missingIndexes, requirement.name)
		}
	}
	if len(missingIndexes) > 0 {
		return runtimeMigrationRequiredError("缺少索引: " + strings.Join(missingIndexes, ", "))
	}
	var legacyModelCount int64
	if err := db.Model(&Model{}).Count(&legacyModelCount).Error; err != nil {
		return runtimeMigrationRequiredError("无法检查旧版模型配置")
	}
	if legacyModelCount != 0 {
		return runtimeMigrationRequiredError("检测到旧版明文模型配置")
	}

	return nil
}

func postgresRuntimeUniqueIndexValid(db *gorm.DB, table, index string) (bool, error) {
	const query = `
SELECT count(*) > 0
FROM pg_catalog.pg_class AS table_definition
JOIN pg_catalog.pg_namespace AS table_namespace ON table_namespace.oid = table_definition.relnamespace
JOIN pg_catalog.pg_index AS index_definition ON index_definition.indrelid = table_definition.oid
JOIN pg_catalog.pg_class AS index_name ON index_name.oid = index_definition.indexrelid
WHERE table_namespace.nspname = current_schema()
  AND table_definition.relname = ?
  AND index_name.relname = ?
  AND index_definition.indisunique
  AND index_definition.indisvalid
  AND index_definition.indisready`
	var valid bool
	if err := db.Raw(query, table, index).Scan(&valid).Error; err != nil {
		return false, err
	}
	return valid, nil
}

func runtimeMigrationRequiredError(reason string) error {
	return fmt.Errorf("数据库架构未就绪（%s），请先运行 aig migrate", reason)
}
