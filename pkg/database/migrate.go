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
}

// Migrate 按版本顺序执行尚未完成的数据库迁移。
func Migrate(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("数据库连接不能为空")
	}
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
