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

package websocket

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRuntimeDatastoreInitializerWorksWithoutDDLPrivileges(t *testing.T) {
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to the isolated PostgreSQL test service")

	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	schema := "runtime_no_ddl_" + suffix
	role := "aig_runtime_" + suffix
	password := "runtime-test-password"
	require.NoError(t, adminDB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)).Error)
	require.NoError(t, adminDB.Exec(fmt.Sprintf(
		"CREATE ROLE %s LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEDB NOCREATEROLE",
		role,
		password,
	)).Error)
	t.Cleanup(func() {
		_ = adminDB.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema)).Error
		_ = adminDB.Exec(fmt.Sprintf("DROP OWNED BY %s", role)).Error
		_ = adminDB.Exec(fmt.Sprintf("DROP ROLE IF EXISTS %s", role)).Error
	})

	adminSchemaDSN := postgresDSNWithSchemaAndUser(t, dsn, schema, "", "")
	schemaDB, err := gorm.Open(postgres.Open(adminSchemaDSN), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(schemaDB))

	require.NoError(t, adminDB.Exec(fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s", schema, role)).Error)
	require.NoError(t, adminDB.Exec(fmt.Sprintf("GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA %s TO %s", schema, role)).Error)
	require.NoError(t, adminDB.Exec(fmt.Sprintf("GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %s TO %s", schema, role)).Error)

	runtimeDSN := postgresDSNWithSchemaAndUser(t, dsn, schema, role, password)
	runtimeDB, err := gorm.Open(postgres.Open(runtimeDSN), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.Error(t, runtimeDB.Exec("CREATE TABLE runtime_ddl_probe (id integer)").Error, "fixture runtime role must not have DDL privileges")

	stores, err := initializeRuntimeDatastores(runtimeDB)
	require.NoError(t, err)
	require.NotNil(t, stores.identityRepository)
	require.NotNil(t, stores.auditRepository)
	require.NotNil(t, stores.platformModelRepository)
	require.NotNil(t, stores.platformTaskRepository)
	require.NotNil(t, stores.taskStore)
	require.NotNil(t, stores.modelStore)
	require.NotNil(t, stores.agentStore)

	var versions []int64
	require.NoError(t, runtimeDB.Table("schema_migrations").Order("version ASC").Pluck("version", &versions).Error)
	require.Equal(t, []int64{1, 2, 3, 4, 5, 6}, versions)
}

func TestRuntimeDatastoreInitializerRejectsLegacyPlaintextModels(t *testing.T) {
	db := openLegacyModelCompatibilityDB(t)
	now := time.Now().UTC().UnixMilli()
	require.NoError(t, db.Create(&database.Model{
		ModelID: "runtime-legacy", Username: "public_user", ModelName: "gpt-legacy",
		Token: "runtime-plaintext-secret", BaseURL: "https://legacy.invalid", CreatedAt: now, UpdatedAt: now,
	}).Error)

	_, err := initializeRuntimeDatastores(db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "aig migrate")
	assert.NotContains(t, err.Error(), "runtime-plaintext-secret")
	var count int64
	require.NoError(t, db.Model(&database.Model{}).Count(&count).Error)
	assert.Equal(t, int64(1), count, "runtime validation must never mutate legacy rows")
}

func postgresDSNWithSchemaAndUser(t *testing.T, dsn, schema, username, password string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	if username != "" {
		parsed.User = url.UserPassword(username, password)
	}
	return parsed.String()
}
