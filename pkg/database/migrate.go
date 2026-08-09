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
		&governanceAuditCompletionMigration{},
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

func (governanceAuditCompletionMigration) TableName() string { return "audit_completion_outbox" }

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
