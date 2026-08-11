// Copyright (c) 2024-2026 Tencent Zhuque Lab. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
// You may not use this file except in compliance with the License.
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestReconcileReadyCompletionIsNotStarvedByPreparedRows(t *testing.T) {
	tests := []struct {
		name       string
		repository func(*testing.T) auditCompletionTestRepository
	}{
		{name: "memory", repository: func(*testing.T) auditCompletionTestRepository { return NewMemoryRepository() }},
		{name: "postgres", repository: func(t *testing.T) auditCompletionTestRepository {
			db := openReadyQueuePostgres(t)
			repository := NewGormRepository(db)
			require.NoError(t, repository.Init())
			return repository
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			repository := test.repository(t)
			base := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
			for index := 0; index < 500; index++ {
				require.NoError(t, repository.EnqueueCompletion(ctx, &CompletionOutbox{
					ID: fmt.Sprintf("prepared-%03d", index), EventID: fmt.Sprintf("prepared-event-%03d", index),
					RequestID: fmt.Sprintf("prepared-request-%03d", index), Action: ActionKnowledgeChanged,
					Outcome: OutcomeSuccess, Metadata: json.RawMessage(`{}`), State: CompletionStatePrepared,
					CreatedAt: base.Add(time.Duration(index) * time.Microsecond),
				}))
			}
			readyAt := base.Add(time.Second)
			require.NoError(t, repository.EnqueueCompletion(ctx, &CompletionOutbox{
				ID: "ready-after-prepared", EventID: "ready-event", RequestID: "ready-request",
				Action: ActionModelUpdated, Outcome: OutcomeSuccess, Metadata: json.RawMessage(`{}`),
				State: CompletionStateReady, CreatedAt: readyAt, ReadyAt: &readyAt,
			}))

			service := NewService(repository)
			admin := identity.Subject{UserID: "admin-id", Role: identity.RoleAdmin}
			delivered, err := service.Reconcile(ctx, admin, 100)
			require.NoError(t, err)
			assert.Equal(t, 1, delivered, "ready work must be selected before the queue limit is applied")
			ready, err := repository.Completion(ctx, "ready-after-prepared")
			require.NoError(t, err)
			assert.NotNil(t, ready.DeliveredAt)

			visible, err := service.PendingCompletions(ctx, identity.Subject{Role: identity.RoleAuditor}, 10)
			require.NoError(t, err)
			require.NotEmpty(t, visible)
			assert.Equal(t, CompletionStatePrepared, visible[0].State, "operator pending list still exposes prepared recovery intents")
		})
	}
}

type auditCompletionTestRepository interface {
	Repository
	CompletionRepository
}

func openReadyQueuePostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to isolated PostgreSQL")
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	schema := "audit_ready_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, adminDB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)).Error)
	t.Cleanup(func() { require.NoError(t, adminDB.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)).Error) })

	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))
	return db
}
