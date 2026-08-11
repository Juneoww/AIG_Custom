// Copyright (c) 2024-2026 Tencent Zhuque Lab. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package models

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"gorm.io/gorm"
)

// MigrateLegacyModelsFromEnvironment is an idempotent post-schema migration.
// The keyring is loaded only when plaintext legacy rows actually exist, so a
// fresh empty database does not gain an external-key dependency.
func MigrateLegacyModelsFromEnvironment(ctx context.Context, db *gorm.DB) error {
	count, err := legacyModelCount(ctx, db)
	if err != nil {
		return fmt.Errorf("检查旧版模型配置失败")
	}
	if count == 0 {
		return nil
	}
	keyring, err := LoadKeyringFromEnv()
	if err != nil {
		return fmt.Errorf("旧版模型配置需要有效的模型主密钥")
	}
	return MigrateLegacyModels(ctx, db, keyring)
}

// MigrateLegacyModels encrypts and removes every legacy row in one database
// transaction. Any mapping, collision, or encryption error rolls back the
// entire batch, leaving the plaintext source rows available for correction.
func MigrateLegacyModels(ctx context.Context, db *gorm.DB, keyring *Keyring) error {
	if db == nil || keyring == nil {
		return errors.New("旧版模型迁移配置无效")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var legacyModels []database.Model
		if err := tx.Order("created_at ASC, model_id ASC").Find(&legacyModels).Error; err != nil {
			return errors.New("读取旧版模型配置失败")
		}
		if len(legacyModels) == 0 {
			return nil
		}

		ids := make([]string, 0, len(legacyModels))
		for index := range legacyModels {
			legacy := &legacyModels[index]
			if !validCompatibilityModelID(legacy.ModelID) || strings.TrimSpace(legacy.ModelName) == "" || legacy.Token == "" {
				return errors.New("旧版模型配置包含无效字段")
			}
			ownerID, scope, err := legacyModelOwnership(tx, legacy.Username)
			if err != nil {
				return err
			}
			var conflictCount int64
			if err := tx.Model(&Model{}).Where("id = ?", legacy.ModelID).Count(&conflictCount).Error; err != nil {
				return errors.New("检查旧版模型 ID 冲突失败")
			}
			if conflictCount != 0 {
				return errors.New("旧版模型 ID 与加密模型配置冲突")
			}

			now := time.Now().UTC()
			model := &Model{
				ID: legacy.ModelID, OwnerUserID: ownerID, Scope: scope,
				Name: legacy.ModelID, ProviderModel: legacy.ModelName, BaseURL: legacy.BaseURL,
				Note: legacy.Note, Limit: legacy.Limit,
				CreatedAt: legacyModelTime(legacy.CreatedAt, now), UpdatedAt: legacyModelTime(legacy.UpdatedAt, now),
			}
			if err := keyring.SealToken(model, legacy.Token); err != nil {
				return errors.New("加密旧版模型 token 失败")
			}
			if err := tx.Create(model).Error; err != nil {
				return errors.New("写入加密模型配置失败")
			}
			ids = append(ids, legacy.ModelID)
		}

		result := tx.Delete(&database.Model{}, "model_id IN ?", ids)
		if result.Error != nil || result.RowsAffected != int64(len(ids)) {
			return errors.New("删除旧版明文模型配置失败")
		}
		return nil
	})
}

func legacyModelOwnership(tx *gorm.DB, username string) (string, Scope, error) {
	username = strings.TrimSpace(username)
	if username == "" || username == "public_user" {
		return "", ScopeGlobal, nil
	}
	var user identity.User
	if err := tx.Where("username = ?", username).First(&user).Error; err != nil {
		return "", "", errors.New("旧版模型配置无法映射到身份账号")
	}
	switch user.Role {
	case identity.RoleAdmin:
		return "", ScopeGlobal, nil
	case identity.RoleUser:
		return user.ID, ScopePrivate, nil
	default:
		return "", "", errors.New("旧版模型配置的账号角色不允许管理模型")
	}
}

func validCompatibilityModelID(id string) bool {
	id = strings.TrimSpace(id)
	return len(id) > 0 && len(id) <= maxCompatibilityModelIDLength && compatibilityModelIDPattern.MatchString(id)
}

func legacyModelTime(milliseconds int64, fallback time.Time) time.Time {
	if milliseconds <= 0 {
		return fallback
	}
	return time.UnixMilli(milliseconds).UTC()
}

func legacyModelCount(ctx context.Context, db *gorm.DB) (int64, error) {
	if db == nil {
		return 0, errors.New("数据库连接为空")
	}
	var count int64
	err := db.WithContext(ctx).Model(&database.Model{}).Count(&count).Error
	return count, err
}
