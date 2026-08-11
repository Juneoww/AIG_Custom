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

package models

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestScannerResolverUsesEncryptedModelsAndFreshIdentityAuthorization(t *testing.T) {
	db := openScannerResolverTestDB(t)
	ctx := context.Background()
	identityRepository := identity.NewGormRepository(db)
	require.NoError(t, identityRepository.Init())
	identityService := identity.NewService(identityRepository)
	auditRepository := audit.NewGormRepository(db)
	require.NoError(t, auditRepository.Init())
	modelRepository := NewGormRepository(db)
	require.NoError(t, modelRepository.Init())
	keyring, err := NewKeyring("resolver-key", bytes.Repeat([]byte{0x31}, 32), nil)
	require.NoError(t, err)
	service := NewService(modelRepository, keyring, audit.NewService(auditRepository))

	admin := createResolverUser(t, identityService, "resolver-admin", identity.RoleAdmin)
	alice := createResolverUser(t, identityService, "resolver-alice", identity.RoleUser)
	bob := createResolverUser(t, identityService, "resolver-bob", identity.RoleUser)
	auditor := createResolverUser(t, identityService, "resolver-auditor", identity.RoleAuditor)
	aliceSubject := subjectOfResolverUser(alice)
	bobSubject := subjectOfResolverUser(bob)
	adminSubject := subjectOfResolverUser(admin)

	aliceToken := "alice-scanner-plaintext"
	aliceModel, err := service.CreateWithCompatibilityID(ctx, aliceSubject, "alice-private", CreateInput{
		Name: "alice-private", ProviderModel: "gpt-alice", BaseURL: "https://alice.invalid/v1", Token: aliceToken, Scope: ScopePrivate, Limit: 73,
	})
	require.NoError(t, err)
	bobModel, err := service.CreateWithCompatibilityID(ctx, bobSubject, "bob-private", CreateInput{
		Name: "bob-private", ProviderModel: "gpt-bob", BaseURL: "https://bob.invalid/v1", Token: "bob-scanner-plaintext", Scope: ScopePrivate,
	})
	require.NoError(t, err)
	globalModel, err := service.CreateWithCompatibilityID(ctx, adminSubject, "admin-global", CreateInput{
		Name: "admin-global", ProviderModel: "gpt-global", BaseURL: "https://global.invalid/v1", Token: "global-scanner-plaintext", Scope: ScopeGlobal,
	})
	require.NoError(t, err)

	resolver := NewScannerResolver(modelRepository, identityRepository, keyring)
	resolved, err := resolver.Resolve(ctx, alice.Username, aliceModel.ID)
	require.NoError(t, err)
	assert.Equal(t, "gpt-alice", resolved.ProviderModel())
	assert.Equal(t, aliceToken, resolved.Token())
	assert.Equal(t, "https://alice.invalid/v1", resolved.BaseURL())
	assert.Equal(t, 73, resolved.Limit())
	assert.NotContains(t, fmt.Sprintf("%+v", resolved), aliceToken)
	encoded, err := json.Marshal(resolved)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), aliceToken)
	assert.Contains(t, string(encoded), MaskedToken)

	resolvedGlobal, err := resolver.Resolve(ctx, alice.Username, globalModel.ID)
	require.NoError(t, err, "active users may scan with administrator-managed global models")
	assert.Equal(t, "global-scanner-plaintext", resolvedGlobal.Token())
	_, err = resolver.Resolve(ctx, alice.Username, bobModel.ID)
	assert.ErrorIs(t, err, ErrForbidden)
	_, err = resolver.Resolve(ctx, admin.Username, aliceModel.ID)
	assert.ErrorIs(t, err, ErrForbidden, "administrators manage and resolve global models, not another user's private key")
	_, err = resolver.Resolve(ctx, auditor.Username, globalModel.ID)
	assert.ErrorIs(t, err, ErrForbidden)
	_, err = resolver.Resolve(ctx, "missing-user", globalModel.ID)
	assert.ErrorIs(t, err, ErrForbidden)

	require.NoError(t, identityService.SetActiveByID(ctx, alice.ID, false))
	_, err = resolver.Resolve(ctx, alice.Username, aliceModel.ID)
	assert.ErrorIs(t, err, ErrForbidden)
	require.NoError(t, identityService.SetActiveByID(ctx, alice.ID, true))

	disabled := true
	_, err = service.Update(ctx, aliceSubject, aliceModel.ID, UpdateInput{Disabled: &disabled})
	require.NoError(t, err)
	_, err = resolver.Resolve(ctx, alice.Username, aliceModel.ID)
	assert.ErrorIs(t, err, ErrForbidden)
	_, err = resolver.Resolve(ctx, alice.Username, "missing-model")
	assert.ErrorIs(t, err, ErrNotFound)
}

func createResolverUser(t *testing.T, service *identity.Service, username string, role identity.Role) *identity.User {
	t.Helper()
	user, err := service.CreateUser(context.Background(), identity.CreateUserInput{Username: username, Password: "password", Role: role})
	require.NoError(t, err)
	return user
}

func subjectOfResolverUser(user *identity.User) identity.Subject {
	return identity.Subject{UserID: user.ID, Username: user.Username, Role: user.Role}
}

func openScannerResolverTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to isolated PostgreSQL")
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	schema := "scanner_resolver_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
