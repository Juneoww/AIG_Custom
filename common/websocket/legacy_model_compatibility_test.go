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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	platformaudit "github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	platformmodels "github.com/Juneoww/AIG_Custom/internal/platform/models"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestLegacyModelHTTPContractUsesEncryptedPlatformStorage(t *testing.T) {
	db := openLegacyModelCompatibilityDB(t)
	ctx := context.Background()
	identityRepository := identity.NewGormRepository(db)
	require.NoError(t, identityRepository.Init())
	identityService := identity.NewService(identityRepository)
	auditRepository := platformaudit.NewGormRepository(db)
	require.NoError(t, auditRepository.Init())
	modelRepository := platformmodels.NewGormRepository(db)
	require.NoError(t, modelRepository.Init())
	keyring, err := platformmodels.NewKeyring("compat-key", bytes.Repeat([]byte{0x42}, 32), nil)
	require.NoError(t, err)
	modelService := platformmodels.NewService(modelRepository, keyring, platformaudit.NewService(auditRepository))
	yamlModelID := "yaml-readonly"
	yamlToken := "yaml-compat-plaintext-token"
	yamlDefaults := []string{"mcp_scan", "model_jailbreak"}
	yamlSource := &stubYAMLModelSource{models: []*database.Model{{
		ModelID: yamlModelID, ModelName: "yaml-provider", Token: yamlToken,
		BaseURL: "https://yaml.invalid/v1", Note: "read only", Limit: 19, Default: yamlDefaults,
	}}}

	user, err := identityService.CreateUser(ctx, identity.CreateUserInput{
		Username: "legacy-user", Password: "temporary-password", Role: identity.RoleUser, MustChangePassword: true,
	})
	require.NoError(t, err)
	require.NoError(t, identityService.ChangePassword(ctx, user.ID, "temporary-password", "ready-password"))
	login, err := identityService.Authenticate(ctx, user.Username, "ready-password")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	models := router.Group("/api/v1/app/models")
	models.Use(
		setupIdentityMiddleware(identityService, identity.CookiePolicy{}),
		identity.RequirePasswordChangeCompleted(),
		identity.RequireCSRF(identity.CookiePolicy{}),
	)
	registerPlatformModelRoutes(models, modelService, yamlSource)

	modelID := "legacy-model-01"
	secondID := "legacy-model-02"
	plaintextToken := "compat-plaintext-token"
	created := governanceRequest(t, router, login.Token, http.MethodPost, "/api/v1/app/models", map[string]any{
		"model_id": modelID,
		"model": map[string]any{
			"model": "gpt-legacy", "token": plaintextToken, "base_url": "https://models.invalid/v1", "note": "initial", "limit": 41,
		},
	})
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())
	assertLegacyEnvelope(t, created.Body.Bytes(), 0, nil)
	assert.NotContains(t, created.Body.String(), plaintextToken)

	stored, err := modelRepository.Get(ctx, modelID)
	require.NoError(t, err)
	assert.Equal(t, platformmodels.ScopePrivate, stored.Scope)
	assert.Equal(t, user.ID, stored.OwnerUserID)
	assert.NotContains(t, string(stored.EncryptedToken), plaintextToken)
	decrypted, err := keyring.OpenToken(stored)
	require.NoError(t, err)
	assert.Equal(t, plaintextToken, decrypted)
	taskManager := NewTaskManager(
		NewAgentManager(), database.NewTaskStore(db), database.NewModelStore(db), nil, NewSSEManager(),
	)
	taskManager.SetModelResolver(platformmodels.NewScannerResolver(modelRepository, identityRepository, keyring))
	scannerParams, err := taskManager.resolveTaskModel(ctx, user.Username, modelID)
	require.NoError(t, err)
	assert.Equal(t, "gpt-legacy", scannerParams.Model)
	assert.Equal(t, plaintextToken, scannerParams.Token)
	assert.Equal(t, "https://models.invalid/v1", scannerParams.BaseUrl)
	assert.Equal(t, 41, scannerParams.Limit)
	var legacyRowCount int64
	require.NoError(t, db.Model(&database.Model{}).Where("model_id = ?", modelID).Count(&legacyRowCount).Error)
	assert.Zero(t, legacyRowCount, "compatibility writes must never persist plaintext legacy rows")

	listed := governanceRequest(t, router, login.Token, http.MethodGet, "/api/v1/app/models", nil)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	var listEnvelope struct {
		Status int `json:"status"`
		Data   []struct {
			ModelID string   `json:"model_id"`
			Default []string `json:"default"`
			Model   struct {
				Model   string `json:"model"`
				Token   string `json:"token"`
				BaseURL string `json:"base_url"`
				Note    string `json:"note"`
				Limit   int    `json:"limit"`
			} `json:"model"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &listEnvelope))
	require.Equal(t, 0, listEnvelope.Status)
	require.Len(t, listEnvelope.Data, 2)
	listedByID := make(map[string]struct {
		Default []string
		Model   string
		Token   string
	})
	for _, item := range listEnvelope.Data {
		listedByID[item.ModelID] = struct {
			Default []string
			Model   string
			Token   string
		}{Default: item.Default, Model: item.Model.Model, Token: item.Model.Token}
	}
	platformListed, ok := listedByID[modelID]
	require.True(t, ok)
	assert.NotNil(t, platformListed.Default, "platform compatibility default must be an explicit empty string array")
	assert.Empty(t, platformListed.Default)
	assert.Equal(t, "gpt-legacy", platformListed.Model)
	assert.Equal(t, platformmodels.MaskedToken, platformListed.Token)
	yamlListed, ok := listedByID[yamlModelID]
	require.True(t, ok)
	assert.Equal(t, yamlDefaults, yamlListed.Default)
	assert.Equal(t, "yaml-provider", yamlListed.Model)
	assert.Equal(t, platformmodels.MaskedToken, yamlListed.Token)
	assert.NotContains(t, listed.Body.String(), plaintextToken)
	assert.NotContains(t, listed.Body.String(), yamlToken)
	detail := governanceRequest(t, router, login.Token, http.MethodGet, "/api/v1/app/models/"+modelID, nil)
	require.Equal(t, http.StatusOK, detail.Code, detail.Body.String())
	var detailEnvelope struct {
		Status int `json:"status"`
		Data   struct {
			ModelID string          `json:"model_id"`
			Default json.RawMessage `json:"default"`
			Model   struct {
				Token string `json:"token"`
			} `json:"model"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(detail.Body.Bytes(), &detailEnvelope))
	assert.Equal(t, 0, detailEnvelope.Status)
	assert.Equal(t, modelID, detailEnvelope.Data.ModelID)
	assert.Equal(t, platformmodels.MaskedToken, detailEnvelope.Data.Model.Token)
	assert.JSONEq(t, `[]`, string(detailEnvelope.Data.Default))
	assert.NotContains(t, detail.Body.String(), plaintextToken)

	yamlDetail := governanceRequest(t, router, login.Token, http.MethodGet, "/api/v1/app/models/"+yamlModelID, nil)
	require.Equal(t, http.StatusOK, yamlDetail.Code, yamlDetail.Body.String())
	require.NoError(t, json.Unmarshal(yamlDetail.Body.Bytes(), &detailEnvelope))
	assert.Equal(t, 0, detailEnvelope.Status)
	assert.Equal(t, yamlModelID, detailEnvelope.Data.ModelID)
	assert.Equal(t, platformmodels.MaskedToken, detailEnvelope.Data.Model.Token)
	assert.JSONEq(t, `["mcp_scan","model_jailbreak"]`, string(detailEnvelope.Data.Default))
	assert.NotContains(t, yamlDetail.Body.String(), yamlToken)

	yamlUpdate := governanceRequest(t, router, login.Token, http.MethodPut, "/api/v1/app/models/"+yamlModelID, map[string]any{
		"model": map[string]any{"model": "must-not-change", "token": "must-not-persist", "base_url": "https://write.invalid/v1"},
	})
	require.Equal(t, http.StatusOK, yamlUpdate.Code, yamlUpdate.Body.String())
	assertLegacyEnvelope(t, yamlUpdate.Body.Bytes(), 1, nil)
	assert.NotContains(t, yamlUpdate.Body.String(), "must-not-persist")
	yamlDelete := governanceRequest(t, router, login.Token, http.MethodDelete, "/api/v1/app/models", map[string]any{
		"model_ids": []string{yamlModelID},
	})
	require.Equal(t, http.StatusOK, yamlDelete.Code, yamlDelete.Body.String())
	assertLegacyEnvelope(t, yamlDelete.Body.Bytes(), 1, nil)
	_, err = modelRepository.Get(ctx, yamlModelID)
	assert.ErrorIs(t, err, platformmodels.ErrNotFound)
	require.NoError(t, db.Model(&database.Model{}).Where("model_id = ?", yamlModelID).Count(&legacyRowCount).Error)
	assert.Zero(t, legacyRowCount, "YAML compatibility reads must never create plaintext legacy rows")
	yamlDetailAfterWrites := governanceRequest(t, router, login.Token, http.MethodGet, "/api/v1/app/models/"+yamlModelID, nil)
	require.Equal(t, http.StatusOK, yamlDetailAfterWrites.Code, yamlDetailAfterWrites.Body.String())
	assert.NotContains(t, yamlDetailAfterWrites.Body.String(), "must-not-change")

	updated := governanceRequest(t, router, login.Token, http.MethodPut, "/api/v1/app/models/"+modelID, map[string]any{
		"model": map[string]any{
			"model": "gpt-updated", "token": platformmodels.MaskedToken, "base_url": "https://updated.invalid/v1", "note": "updated", "limit": 88,
		},
	})
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	assertLegacyEnvelope(t, updated.Body.Bytes(), 0, nil)
	stored, err = modelRepository.Get(ctx, modelID)
	require.NoError(t, err)
	assert.Equal(t, "gpt-updated", stored.ProviderModel)
	decrypted, err = keyring.OpenToken(stored)
	require.NoError(t, err)
	assert.Equal(t, plaintextToken, decrypted, "the masked compatibility token means unchanged")

	for _, id := range []string{secondID} {
		response := governanceRequest(t, router, login.Token, http.MethodPost, "/api/v1/app/models", map[string]any{
			"model_id": id,
			"model":    map[string]any{"model": "gpt-delete", "token": "second-secret", "base_url": "https://models.invalid/v1"},
		})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assertLegacyEnvelope(t, response.Body.Bytes(), 0, nil)
	}
	deleted := governanceRequest(t, router, login.Token, http.MethodDelete, "/api/v1/app/models", map[string]any{
		"model_ids": []string{modelID, secondID},
	})
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	assertLegacyEnvelope(t, deleted.Body.Bytes(), 0, nil)
	for _, id := range []string{modelID, secondID} {
		_, err := modelRepository.Get(ctx, id)
		assert.ErrorIs(t, err, platformmodels.ErrNotFound)
	}

	auditor, err := identityService.CreateUser(ctx, identity.CreateUserInput{
		Username: "legacy-auditor", Password: "temporary-password", Role: identity.RoleAuditor, MustChangePassword: true,
	})
	require.NoError(t, err)
	require.NoError(t, identityService.ChangePassword(ctx, auditor.ID, "temporary-password", "ready-password"))
	auditorLogin, err := identityService.Authenticate(ctx, auditor.Username, "ready-password")
	require.NoError(t, err)
	forbidden := governanceRequest(t, router, auditorLogin.Token, http.MethodPost, "/api/v1/app/models", map[string]any{
		"model_id": "auditor-forbidden",
		"model":    map[string]any{"model": "gpt", "token": "must-not-persist", "base_url": "https://models.invalid/v1"},
	})
	assert.Equal(t, http.StatusForbidden, forbidden.Code, forbidden.Body.String())
	assert.NotContains(t, forbidden.Body.String(), "must-not-persist")

	invalidID := governanceRequest(t, router, login.Token, http.MethodPost, "/api/v1/app/models", map[string]any{
		"model_id": "../not-allowed",
		"model":    map[string]any{"model": "gpt", "token": "invalid-id-token", "base_url": "https://models.invalid/v1"},
	})
	require.Equal(t, http.StatusOK, invalidID.Code, invalidID.Body.String())
	assertLegacyEnvelope(t, invalidID.Body.Bytes(), 1, nil)
	assert.NotContains(t, invalidID.Body.String(), "invalid-id-token")
}

func assertLegacyEnvelope(t *testing.T, payload []byte, expectedStatus int, expectedData any) {
	t.Helper()
	var envelope struct {
		Status  int             `json:"status"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(payload, &envelope))
	assert.Equal(t, expectedStatus, envelope.Status)
	assert.NotEmpty(t, envelope.Message)
	if expectedData == nil {
		assert.JSONEq(t, "null", string(envelope.Data))
	}
}

func openLegacyModelCompatibilityDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to isolated PostgreSQL")
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	schema := "legacy_models_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
