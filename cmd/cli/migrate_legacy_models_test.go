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

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	platformmodels "github.com/Juneoww/AIG_Custom/internal/platform/models"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRunMigrateEncryptsLegacyModelsAndDeletesPlaintextRowsAtomically(t *testing.T) {
	fixture := newLegacyMigrationFixture(t)
	identityService := identity.NewService(identity.NewGormRepository(fixture.db))
	user, err := identityService.CreateUser(context.Background(), identity.CreateUserInput{
		Username: "legacy-user", Password: "password", Role: identity.RoleUser,
	})
	require.NoError(t, err)
	admin, err := identityService.CreateUser(context.Background(), identity.CreateUserInput{
		Username: "legacy-admin", Password: "password", Role: identity.RoleAdmin,
	})
	require.NoError(t, err)
	now := time.Now().UTC().UnixMilli()
	legacyRows := []database.Model{
		{ModelID: "legacy-user-model", Username: user.Username, ModelName: "gpt-user", Token: "user-plaintext-token", BaseURL: "https://user.invalid/v1", Note: "user", Limit: 11, CreatedAt: now, UpdatedAt: now},
		{ModelID: "legacy-admin-model", Username: admin.Username, ModelName: "gpt-admin", Token: "admin-plaintext-token", BaseURL: "https://admin.invalid/v1", Note: "admin", Limit: 22, CreatedAt: now + 1, UpdatedAt: now + 1},
		{ModelID: "legacy-public-model", Username: "public_user", ModelName: "gpt-public", Token: "public-plaintext-token", BaseURL: "https://public.invalid/v1", Note: "public", Limit: 33, CreatedAt: now + 2, UpdatedAt: now + 2},
	}
	require.NoError(t, fixture.db.Create(&legacyRows).Error)
	keyring := fixture.configureMigrateEnvironment(t, true)

	require.NoError(t, runMigrate())
	assert.Equal(t, int64(0), fixture.legacyCount(t))

	repository := platformmodels.NewGormRepository(fixture.db)
	for _, expected := range []struct {
		id, ownerID, token string
		scope              platformmodels.Scope
	}{
		{id: "legacy-user-model", ownerID: user.ID, token: "user-plaintext-token", scope: platformmodels.ScopePrivate},
		{id: "legacy-admin-model", token: "admin-plaintext-token", scope: platformmodels.ScopeGlobal},
		{id: "legacy-public-model", token: "public-plaintext-token", scope: platformmodels.ScopeGlobal},
	} {
		stored, err := repository.Get(context.Background(), expected.id)
		require.NoError(t, err)
		assert.Equal(t, expected.id, stored.ID)
		assert.Equal(t, expected.ownerID, stored.OwnerUserID)
		assert.Equal(t, expected.scope, stored.Scope)
		assert.NotContains(t, string(stored.EncryptedToken), expected.token)
		plaintext, err := keyring.OpenToken(stored)
		require.NoError(t, err)
		assert.Equal(t, expected.token, plaintext)
	}
	require.NoError(t, runMigrate(), "post-schema import is idempotent once legacy rows are gone")
}

func TestRunMigrateDoesNotRequireModelKeyForEmptyLegacyTable(t *testing.T) {
	fixture := newLegacyMigrationFixture(t)
	fixture.configureMigrateEnvironment(t, false)
	require.NoError(t, runMigrate())
	assert.Equal(t, int64(0), fixture.legacyCount(t))
}

func TestRunMigrateRollsBackEveryLegacyRowOnMappingConflictOrEncryptionFailure(t *testing.T) {
	t.Run("missing identity", func(t *testing.T) {
		fixture := newLegacyMigrationFixture(t)
		service := identity.NewService(identity.NewGormRepository(fixture.db))
		valid, err := service.CreateUser(context.Background(), identity.CreateUserInput{Username: "valid-user", Password: "password", Role: identity.RoleUser})
		require.NoError(t, err)
		now := time.Now().UTC().UnixMilli()
		require.NoError(t, fixture.db.Create(&[]database.Model{
			{ModelID: "a-valid-first", Username: valid.Username, ModelName: "gpt-valid", Token: "valid-secret", BaseURL: "https://valid.invalid", CreatedAt: now, UpdatedAt: now},
			{ModelID: "z-missing", Username: "no-such-user", ModelName: "gpt-missing", Token: "missing-secret", BaseURL: "https://missing.invalid", CreatedAt: now + 1, UpdatedAt: now + 1},
		}).Error)
		fixture.configureMigrateEnvironment(t, true)
		err = runMigrate()
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "valid-secret")
		assert.NotContains(t, err.Error(), "missing-secret")
		assert.NotContains(t, err.Error(), "https://valid.invalid")
		assert.NotContains(t, err.Error(), "https://missing.invalid")
		assert.Equal(t, int64(2), fixture.legacyCount(t))
		assert.Equal(t, int64(0), fixture.platformCount(t))
	})

	t.Run("platform ID conflict", func(t *testing.T) {
		fixture := newLegacyMigrationFixture(t)
		service := identity.NewService(identity.NewGormRepository(fixture.db))
		user, err := service.CreateUser(context.Background(), identity.CreateUserInput{Username: "conflict-user", Password: "password", Role: identity.RoleUser})
		require.NoError(t, err)
		now := time.Now().UTC().UnixMilli()
		require.NoError(t, fixture.db.Create(&database.Model{
			ModelID: "conflicting-id", Username: user.Username, ModelName: "gpt-legacy", Token: "legacy-conflict-secret", BaseURL: "https://legacy.invalid", CreatedAt: now, UpdatedAt: now,
		}).Error)
		keyring := fixture.configureMigrateEnvironment(t, true)
		existing := &platformmodels.Model{
			ID: "conflicting-id", Scope: platformmodels.ScopePrivate, OwnerUserID: user.ID,
			Name: "existing", ProviderModel: "gpt-existing", BaseURL: "https://existing.invalid", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		require.NoError(t, keyring.SealToken(existing, "existing-secret"))
		require.NoError(t, platformmodels.NewGormRepository(fixture.db).Create(context.Background(), existing))

		err = runMigrate()
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "legacy-conflict-secret")
		assert.NotContains(t, err.Error(), "https://legacy.invalid")
		assert.Equal(t, int64(1), fixture.legacyCount(t))
		assert.Equal(t, int64(1), fixture.platformCount(t))
	})

	t.Run("empty legacy token", func(t *testing.T) {
		fixture := newLegacyMigrationFixture(t)
		service := identity.NewService(identity.NewGormRepository(fixture.db))
		user, err := service.CreateUser(context.Background(), identity.CreateUserInput{Username: "empty-token-user", Password: "password", Role: identity.RoleUser})
		require.NoError(t, err)
		now := time.Now().UTC().UnixMilli()
		require.NoError(t, fixture.db.Create(&database.Model{
			ModelID: "empty-token", Username: user.Username, ModelName: "gpt-empty", Token: "", BaseURL: "https://empty.invalid", CreatedAt: now, UpdatedAt: now,
		}).Error)
		fixture.configureMigrateEnvironment(t, true)

		err = runMigrate()
		require.Error(t, err)
		assert.Equal(t, int64(1), fixture.legacyCount(t))
		assert.Equal(t, int64(0), fixture.platformCount(t))
	})

	t.Run("missing keyring", func(t *testing.T) {
		fixture := newLegacyMigrationFixture(t)
		now := time.Now().UTC().UnixMilli()
		require.NoError(t, fixture.db.Create(&database.Model{
			ModelID: "key-required", Username: "public_user", ModelName: "gpt-key",
			Token: "key-required-secret", BaseURL: "https://key.invalid", CreatedAt: now, UpdatedAt: now,
		}).Error)
		fixture.configureMigrateEnvironment(t, false)

		err := runMigrate()
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "key-required-secret")
		assert.NotContains(t, err.Error(), "https://key.invalid")
		assert.Equal(t, int64(1), fixture.legacyCount(t))
		assert.Equal(t, int64(0), fixture.platformCount(t))
	})
}

type legacyMigrationFixture struct {
	db  *gorm.DB
	dsn string
}

func newLegacyMigrationFixture(t *testing.T) *legacyMigrationFixture {
	t.Helper()
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to isolated PostgreSQL")
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	schema := "legacy_import_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, adminDB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)).Error)
	t.Cleanup(func() { require.NoError(t, adminDB.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)).Error) })

	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	scopedDSN := parsed.String()
	db, err := gorm.Open(postgres.Open(scopedDSN), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))
	return &legacyMigrationFixture{db: db, dsn: scopedDSN}
}

func (fixture *legacyMigrationFixture) configureMigrateEnvironment(t *testing.T, withKey bool) *platformmodels.Keyring {
	t.Helper()
	t.Setenv("DB_DRIVER", "postgres")
	t.Setenv("DB_DSN", fixture.dsn)
	t.Setenv(platformmodels.EnvPreviousMasterKeys, "")
	if !withKey {
		t.Setenv(platformmodels.EnvMasterKeyID, "")
		t.Setenv(platformmodels.EnvMasterKey, "")
		return nil
	}
	key := bytes.Repeat([]byte{0x5a}, 32)
	t.Setenv(platformmodels.EnvMasterKeyID, "legacy-test-key")
	t.Setenv(platformmodels.EnvMasterKey, base64.StdEncoding.EncodeToString(key))
	keyring, err := platformmodels.NewKeyring("legacy-test-key", key, nil)
	require.NoError(t, err)
	return keyring
}

func (fixture *legacyMigrationFixture) legacyCount(t *testing.T) int64 {
	t.Helper()
	var count int64
	require.NoError(t, fixture.db.Model(&database.Model{}).Count(&count).Error)
	return count
}

func (fixture *legacyMigrationFixture) platformCount(t *testing.T) int64 {
	t.Helper()
	var count int64
	require.NoError(t, fixture.db.Model(&platformmodels.Model{}).Count(&count).Error)
	return count
}
