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

package identity

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestConcurrentIdentityMutationsDoNotOverwriteUnrelatedFields(t *testing.T) {
	controlDB, barrierDB := openConcurrentIdentityTestDBs(t)
	control := NewService(NewGormRepository(controlDB))

	t.Run("password change preserves concurrent disable", func(t *testing.T) {
		user := createConcurrentTestUser(t, control, "password-change")
		barrier := installIdentityUserUpdateBarrier(t, barrierDB)
		mutation := NewService(NewGormRepository(barrierDB))
		errCh := make(chan error, 1)
		go func() {
			errCh <- mutation.ChangePassword(context.Background(), user.ID, "old-password", "new-password")
		}()
		barrier.wait(t)
		require.NoError(t, control.SetActiveByID(context.Background(), user.ID, false))
		barrier.release()
		require.NoError(t, <-errCh)

		stored, err := NewGormRepository(controlDB).UserByID(context.Background(), user.ID)
		require.NoError(t, err)
		assert.False(t, stored.Active)
		assert.True(t, VerifyPassword(stored.PasswordHash, "new-password"))
	})

	t.Run("password reset preserves concurrent disable", func(t *testing.T) {
		user := createConcurrentTestUser(t, control, "password-reset")
		token, err := control.CreatePasswordReset(context.Background(), user.ID)
		require.NoError(t, err)
		barrier := installIdentityUserUpdateBarrier(t, barrierDB)
		mutation := NewService(NewGormRepository(barrierDB))
		errCh := make(chan error, 1)
		go func() { errCh <- mutation.ResetPassword(context.Background(), token, "temporary-password") }()
		barrier.wait(t)
		require.NoError(t, control.SetActiveByID(context.Background(), user.ID, false))
		barrier.release()
		require.NoError(t, <-errCh)

		stored, err := NewGormRepository(controlDB).UserByID(context.Background(), user.ID)
		require.NoError(t, err)
		assert.False(t, stored.Active)
		assert.True(t, stored.MustChangePassword)
		assert.True(t, VerifyPassword(stored.PasswordHash, "temporary-password"))
	})

	t.Run("role change preserves concurrent disable", func(t *testing.T) {
		user := createConcurrentTestUser(t, control, "role")
		barrier := installIdentityUserUpdateBarrier(t, barrierDB)
		mutation := NewService(NewGormRepository(barrierDB))
		errCh := make(chan error, 1)
		go func() { errCh <- mutation.SetRole(context.Background(), user.ID, RoleAdmin) }()
		barrier.wait(t)
		require.NoError(t, control.SetActiveByID(context.Background(), user.ID, false))
		barrier.release()
		require.NoError(t, <-errCh)

		stored, err := NewGormRepository(controlDB).UserByID(context.Background(), user.ID)
		require.NoError(t, err)
		assert.Equal(t, RoleAdmin, stored.Role)
		assert.False(t, stored.Active)
	})

	t.Run("disable preserves concurrent role change", func(t *testing.T) {
		user := createConcurrentTestUser(t, control, "active")
		barrier := installIdentityUserUpdateBarrier(t, barrierDB)
		mutation := NewService(NewGormRepository(barrierDB))
		errCh := make(chan error, 1)
		go func() { errCh <- mutation.SetActiveByID(context.Background(), user.ID, false) }()
		barrier.wait(t)
		require.NoError(t, control.SetRole(context.Background(), user.ID, RoleAuditor))
		barrier.release()
		require.NoError(t, <-errCh)

		stored, err := NewGormRepository(controlDB).UserByID(context.Background(), user.ID)
		require.NoError(t, err)
		assert.Equal(t, RoleAuditor, stored.Role)
		assert.False(t, stored.Active)
	})
}

type userUpdateBarrier struct {
	entered chan struct{}
	proceed chan struct{}
}

func installIdentityUserUpdateBarrier(t *testing.T, db *gorm.DB) *userUpdateBarrier {
	t.Helper()
	barrier := &userUpdateBarrier{entered: make(chan struct{}), proceed: make(chan struct{})}
	callbackName := "test:identity-user-update-barrier:" + uuid.NewString()
	var once sync.Once
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Table != "identity_users" {
			return
		}
		once.Do(func() {
			close(barrier.entered)
			<-barrier.proceed
		})
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callbackName) })
	return barrier
}

func (barrier *userUpdateBarrier) wait(t *testing.T) {
	t.Helper()
	select {
	case <-barrier.entered:
	case <-time.After(15 * time.Second):
		t.Fatal("identity update did not reach the PostgreSQL barrier")
	}
}

func (barrier *userUpdateBarrier) release() { close(barrier.proceed) }

func createConcurrentTestUser(t *testing.T, service *Service, suffix string) *User {
	t.Helper()
	user, err := service.CreateUser(context.Background(), CreateUserInput{
		ID:       "concurrent-" + suffix,
		Username: "concurrent-" + suffix,
		Password: "old-password",
		Role:     RoleUser,
	})
	require.NoError(t, err)
	return user
}

func openConcurrentIdentityTestDBs(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to isolated PostgreSQL")
	adminDB, err := database.InitDB(database.NewConfig(dsn))
	require.NoError(t, err)
	schema := "identity_concurrency_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, adminDB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)).Error)
	t.Cleanup(func() { require.NoError(t, adminDB.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)).Error) })

	schemaDSN := dsn + "&search_path=" + schema
	controlDB, err := database.InitDB(database.NewConfig(schemaDSN))
	require.NoError(t, err)
	require.NoError(t, database.Migrate(controlDB))
	barrierDB, err := database.InitDB(database.NewConfig(schemaDSN))
	require.NoError(t, err)
	return controlDB, barrierDB
}
