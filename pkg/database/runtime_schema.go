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
const LatestSchemaVersion int64 = 4

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
