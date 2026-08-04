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
	"os/exec"
	"testing"

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
	require.Len(t, versions, 1)
	require.Equal(t, int64(1), versions[0].Version)
}
