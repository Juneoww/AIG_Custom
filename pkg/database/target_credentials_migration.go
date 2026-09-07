package database

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 独立迁移实体保持数据库包不依赖平台服务。
type targetCredentialMigration struct {
	ID              string    `gorm:"primaryKey"`
	OwnerUserID     string    `gorm:"not null;index:idx_target_credentials_owner"`
	Name            string    `gorm:"not null"`
	Origin          string    `gorm:"not null"`
	AuthType        string    `gorm:"not null"`
	HeaderName      string    `gorm:"not null"`
	Disabled        bool      `gorm:"not null"`
	Revision        int64     `gorm:"not null"`
	EncryptedSecret []byte    `gorm:"not null"`
	SecretNonce     []byte    `gorm:"not null"`
	KeyID           string    `gorm:"not null"`
	CreatedAt       time.Time `gorm:"not null"`
	UpdatedAt       time.Time `gorm:"not null"`
}

func (targetCredentialMigration) TableName() string { return "platform_target_credentials" }
func migrateTargetCredentialSchema(db *gorm.DB) error {
	const table = "platform_target_credentials"
	if !db.Migrator().HasTable(table) {
		if err := db.Migrator().CreateTable(&targetCredentialMigration{}); err != nil {
			return err
		}
	}
	// 重放迁移时验证现有表，不通过 AutoMigrate 推断或改写已保存的密文列。
	for _, column := range []string{"id", "owner_user_id", "name", "origin", "auth_type", "header_name", "disabled", "revision", "encrypted_secret", "secret_nonce", "key_id", "created_at", "updated_at"} {
		if !db.Migrator().HasColumn(table, column) {
			return fmt.Errorf("%s 缺少列 %s", table, column)
		}
	}
	return db.Exec("CREATE INDEX IF NOT EXISTS idx_target_credentials_owner ON platform_target_credentials (owner_user_id)").Error
}
