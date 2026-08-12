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
	"bytes"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestMigrationCLIIsIdempotent(t *testing.T) {
	db := openPostgresTestDB(t)
	resetPostgresTestDB(t, db)

	binary := os.Getenv("AIG_TEST_CLI_BINARY")
	require.NotEmpty(t, binary, "AIG_TEST_CLI_BINARY must point to the Docker-built aig binary")

	for range 2 {
		command := exec.Command(binary, "migrate")
		command.Env = append(os.Environ(), "DB_DRIVER=postgres", "DB_DSN="+testPostgresDSN(t))
		require.NoError(t, command.Run())
	}

	var versions []SchemaMigration
	require.NoError(t, db.Order("version ASC").Find(&versions).Error)
	require.Len(t, versions, int(LatestSchemaVersion))
	for index, version := range versions {
		require.Equal(t, int64(index+1), version.Version)
	}
	require.True(t, db.Migrator().HasTable(&identity.User{}))
	require.True(t, db.Migrator().HasTable(&identity.Session{}))
	require.True(t, db.Migrator().HasTable(&identity.PasswordReset{}))
	require.True(t, db.Migrator().HasTable("audit_events"))
	require.True(t, db.Migrator().HasTable("audit_completion_outbox"))
	require.True(t, db.Migrator().HasTable("platform_models"))
	require.True(t, db.Migrator().HasTable("platform_tasks"))
	require.True(t, db.Migrator().HasTable("platform_attachments"))
	require.True(t, db.Migrator().HasTable("report_snapshots"))
	require.True(t, db.Migrator().HasTable("report_brand_settings"))

	role := "aig_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := "runtime-test-password"
	require.NoError(t, db.Exec(fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEDB NOCREATEROLE", role, password)).Error)
	t.Cleanup(func() {
		_ = db.Exec(fmt.Sprintf("DROP OWNED BY %s", role)).Error
		_ = db.Exec(fmt.Sprintf("DROP ROLE IF EXISTS %s", role)).Error
	})
	require.NoError(t, db.Exec(fmt.Sprintf("GRANT USAGE ON SCHEMA public TO %s", role)).Error)
	require.NoError(t, db.Exec(fmt.Sprintf("GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %s", role)).Error)

	runtimeURL, err := url.Parse(testPostgresDSN(t))
	require.NoError(t, err)
	runtimeURL.User = url.UserPassword(role, password)
	runtimeDSN := runtimeURL.String()

	bootstrap := exec.Command(binary, "bootstrap-admin")
	bootstrap.Env = append(os.Environ(),
		"DB_DRIVER=postgres",
		"DB_DSN="+runtimeDSN,
		"AIG_BOOTSTRAP_ADMIN_USERNAME=runtime-admin",
		"AIG_BOOTSTRAP_ADMIN_PASSWORD=temporary-password",
	)
	require.NoError(t, bootstrap.Run())

	var stdout bytes.Buffer
	reset := exec.Command(binary, "create-password-reset", "--username", "runtime-admin")
	reset.Env = append(os.Environ(), "DB_DRIVER=postgres", "DB_DSN="+runtimeDSN)
	reset.Stdout = &stdout
	require.NoError(t, reset.Run())
	require.Contains(t, stdout.String(), "SENSITIVE")
}
