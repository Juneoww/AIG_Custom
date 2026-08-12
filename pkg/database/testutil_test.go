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
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func openPostgresTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to the isolated PostgreSQL test service")

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)

	return db
}

func resetPostgresTestDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Migrator().DropTable(
		"platform_attachments",
		"platform_tasks",
		"platform_models",
		"audit_completion_outbox",
		"audit_events",
		&identityPasswordResetMigration{},
		&identitySessionMigration{},
		&identityUserMigration{},
		&SchemaMigration{},
		&TaskMessage{},
		&Session{},
		&Model{},
		&User{},
		&Agent{},
	))
}

func testPostgresDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to the isolated PostgreSQL test service")
	return dsn
}
