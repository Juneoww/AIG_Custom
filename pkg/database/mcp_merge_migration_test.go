package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrationReconcilesMCPDevelopmentHistoryWithoutLosingBindings(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)
	require.NoError(t, Migrate(db))
	// 开发分支的 v10/v11 已有 MCP，但还没有 develop v10 的两个字段。
	require.NoError(t, db.Where("version = ?", 12).Delete(&SchemaMigration{}).Error)
	require.NoError(t, db.Exec("ALTER TABLE platform_tasks DROP COLUMN remark, DROP COLUMN target_count").Error)
	now := time.Now().UTC()
	binding := platformMCPTaskBindingMigration{ID: "preserved-binding", TaskID: "preserved-task", SourceKind: "repository", EncryptedRepositoryURL: []byte("sealed-source-sentinel"), CreatedAt: now, UpdatedAt: now}
	require.NoError(t, db.Create(&binding).Error)

	require.NoError(t, Migrate(db))
	require.NoError(t, Migrate(db))
	require.NoError(t, ValidateRuntimeSchema(db))
	assert.True(t, db.Migrator().HasColumn("platform_tasks", "remark"))
	assert.True(t, db.Migrator().HasColumn("platform_tasks", "target_count"))
	var preserved platformMCPTaskBindingMigration
	require.NoError(t, db.First(&preserved, "id = ?", binding.ID).Error)
	assert.Equal(t, binding.EncryptedRepositoryURL, preserved.EncryptedRepositoryURL)
	assert.Equal(t, binding.TaskID, preserved.TaskID)
}

func TestMigrationUpgradesReleasedDevelopTenWithMCP(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)
	require.NoError(t, Migrate(db))
	dropMCPConnectionSchemaTables(t, db)
	require.NoError(t, db.Where("version > ?", 10).Delete(&SchemaMigration{}).Error)

	require.NoError(t, Migrate(db))
	require.NoError(t, ValidateRuntimeSchema(db))
	assertMCPConnectionSchema(t, db)
	assert.True(t, db.Migrator().HasColumn("platform_tasks", "remark"))
	assert.True(t, db.Migrator().HasColumn("platform_tasks", "target_count"))
}
