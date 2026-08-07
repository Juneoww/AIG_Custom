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
	"sync"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMigrationAppliesIdentitySchemaAsVersionTwo(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)

	require.NoError(t, Migrate(db))
	assert.True(t, db.Migrator().HasTable(&identity.User{}))
	assert.True(t, db.Migrator().HasTable(&identity.Session{}))
	assert.True(t, db.Migrator().HasTable(&identity.PasswordReset{}))

	var versions []SchemaMigration
	require.NoError(t, db.Order("version ASC").Find(&versions).Error)
	require.Len(t, versions, 2)
	assert.Equal(t, int64(1), versions[0].Version)
	assert.Equal(t, int64(2), versions[1].Version)
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
	require.Len(t, versions, 2)
	assert.Equal(t, int64(1), versions[0].Version)
	assert.Equal(t, int64(2), versions[1].Version)
	assert.NotZero(t, versions[0].AppliedAt)
	assert.NotZero(t, versions[1].AppliedAt)
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
	require.Len(t, versions, 2)
	assert.Equal(t, int64(1), versions[0].Version)
	assert.Equal(t, int64(2), versions[1].Version)
	assert.True(t, first.Migrator().HasTable(&User{}))
	assert.True(t, first.Migrator().HasTable(&Session{}))
	assert.True(t, first.Migrator().HasTable(&TaskMessage{}))
	assert.True(t, first.Migrator().HasTable(&Model{}))
	assert.True(t, first.Migrator().HasTable(&Agent{}))
	assert.True(t, first.Migrator().HasTable(&identity.User{}))
	assert.True(t, first.Migrator().HasTable(&identity.Session{}))
	assert.True(t, first.Migrator().HasTable(&identity.PasswordReset{}))
	assert.True(t, first.Migrator().HasIndex(&Model{}, "idx_models_username_created"))
}
