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
	"errors"
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
	taskManager.SetYAMLModelSource(yamlSource)
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

	mutationCountsBeforeCollision := countLegacyModelMutationRows(t, db)
	yamlLoadsBeforeCollision := yamlSource.loadCalls
	yamlGetsBeforeCollision := yamlSource.getCalls
	collisionToken := "yaml-shadow-plaintext-token"
	collisionBaseURL := "https://yaml-shadow.invalid/v1"
	collision := governanceRequest(t, router, login.Token, http.MethodPost, "/api/v1/app/models", map[string]any{
		"model_id": "  " + yamlModelID + "  ",
		"model": map[string]any{
			"model": "must-not-shadow-yaml", "token": collisionToken, "base_url": collisionBaseURL,
		},
	})
	require.Equal(t, http.StatusOK, collision.Code, collision.Body.String())
	var collisionEnvelope struct {
		Status  int    `json:"status"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(collision.Body.Bytes(), &collisionEnvelope))
	assert.Equal(t, 1, collisionEnvelope.Status)
	assert.Contains(t, collisionEnvelope.Message, "已存在")
	assert.NotContains(t, collision.Body.String(), collisionToken)
	assert.NotContains(t, collision.Body.String(), collisionBaseURL)
	assert.NotContains(t, collision.Body.String(), yamlToken)
	assert.Equal(t, yamlLoadsBeforeCollision+1, yamlSource.loadCalls, "POST must inspect the shared YAML source exactly once")
	assert.Equal(t, yamlGetsBeforeCollision, yamlSource.getCalls, "POST must not reload YAML after its single snapshot")
	assert.Equal(t, mutationCountsBeforeCollision, countLegacyModelMutationRows(t, db))
	_, err = modelRepository.Get(ctx, yamlModelID)
	assert.ErrorIs(t, err, platformmodels.ErrNotFound)
	assert.Equal(t, yamlModelID, yamlSource.models[0].ModelID)
	assert.Equal(t, yamlToken, yamlSource.models[0].Token)
	assert.Equal(t, yamlDefaults, yamlSource.models[0].Default)

	yamlScannerParams, err := taskManager.resolveTaskModel(ctx, user.Username, yamlModelID)
	require.NoError(t, err)
	assert.Equal(t, "yaml-provider", yamlScannerParams.Model)
	assert.Equal(t, yamlToken, yamlScannerParams.Token)
	otherUser, err := identityService.CreateUser(ctx, identity.CreateUserInput{
		Username: "other-yaml-user", Password: "ready-password", Role: identity.RoleUser,
	})
	require.NoError(t, err)
	otherYAMLScannerParams, err := taskManager.resolveTaskModel(ctx, otherUser.Username, yamlModelID)
	require.NoError(t, err)
	assert.Equal(t, "yaml-provider", otherYAMLScannerParams.Model)
	assert.Equal(t, yamlToken, otherYAMLScannerParams.Token)

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

func TestLegacyModelCreateFailsClosedWhenYAMLSourceCannotLoad(t *testing.T) {
	db := openLegacyModelCompatibilityDB(t)
	ctx := context.Background()
	identityRepository := identity.NewGormRepository(db)
	require.NoError(t, identityRepository.Init())
	identityService := identity.NewService(identityRepository)
	auditRepository := platformaudit.NewGormRepository(db)
	require.NoError(t, auditRepository.Init())
	modelRepository := platformmodels.NewGormRepository(db)
	require.NoError(t, modelRepository.Init())
	keyring, err := platformmodels.NewKeyring("compat-key", bytes.Repeat([]byte{0x43}, 32), nil)
	require.NoError(t, err)
	modelService := platformmodels.NewService(modelRepository, keyring, platformaudit.NewService(auditRepository))
	yamlLoadError := errors.New("yaml-source-sensitive-detail")
	yamlSource := &stubYAMLModelSource{loadErr: yamlLoadError}

	user, err := identityService.CreateUser(ctx, identity.CreateUserInput{
		Username: "yaml-error-user", Password: "temporary-password", Role: identity.RoleUser, MustChangePassword: true,
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

	before := countLegacyModelMutationRows(t, db)
	plaintextToken := "yaml-load-error-token"
	baseURL := "https://yaml-load-error.invalid/v1"
	response := governanceRequest(t, router, login.Token, http.MethodPost, "/api/v1/app/models", map[string]any{
		"model_id": "must-not-create",
		"model": map[string]any{
			"model": "must-not-create", "token": plaintextToken, "base_url": baseURL,
		},
	})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assertLegacyEnvelope(t, response.Body.Bytes(), 1, nil)
	assert.NotContains(t, response.Body.String(), yamlLoadError.Error())
	assert.NotContains(t, response.Body.String(), plaintextToken)
	assert.NotContains(t, response.Body.String(), baseURL)
	assert.Equal(t, 1, yamlSource.loadCalls)
	assert.Zero(t, yamlSource.getCalls)
	assert.Equal(t, before, countLegacyModelMutationRows(t, db))
	_, err = modelRepository.Get(ctx, "must-not-create")
	assert.ErrorIs(t, err, platformmodels.ErrNotFound)
}

func TestGovernanceModelSafeCatalogListEnvelopePaginationPreservesYAMLCollision(t *testing.T) {
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	adminUser, err := identityService.CreateUser(ctx, identity.CreateUserInput{
		Username: "catalog-admin", Password: "catalog-password", Role: identity.RoleAdmin,
	})
	require.NoError(t, err)
	login, err := identityService.Authenticate(ctx, adminUser.Username, "catalog-password")
	require.NoError(t, err)
	modelRepository := platformmodels.NewMemoryRepository()
	keyring, err := platformmodels.NewKeyring("catalog-key", bytes.Repeat([]byte{0x45}, 32), nil)
	require.NoError(t, err)
	modelService := platformmodels.NewService(modelRepository, keyring, platformaudit.NewService(platformaudit.NewMemoryRepository()))
	_, err = modelService.CreateWithCompatibilityID(ctx, login.Subject, "collision-id", platformmodels.CreateInput{
		Name: "database collision", ProviderModel: "database-provider", BaseURL: "https://database.invalid/v1",
		Token: "database-plaintext-token", Scope: platformmodels.ScopeGlobal,
	})
	require.NoError(t, err)
	yamlSource := &stubYAMLModelSource{models: []*database.Model{
		{ModelID: "collision-id", ModelName: "yaml-collision", BaseURL: "https://yaml-collision.invalid/v1", Token: "yaml-collision-token"},
		{ModelID: "yaml-only", ModelName: "yaml-only", BaseURL: "https://yaml-only.invalid/v1", Token: "yaml-only-token"},
	}}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	policy := identity.CookiePolicy{}
	platformGroup := router.Group("/api/v1/platform", setupIdentityMiddleware(identityService, policy), identity.RequirePasswordChangeCompleted(), identity.RequireCSRF(policy))
	registerGovernanceModelRoutes(platformGroup.Group("/models"), modelService)
	legacyGroup := router.Group("/api/v1/app/models", setupIdentityMiddleware(identityService, policy), identity.RequirePasswordChangeCompleted(), identity.RequireCSRF(policy))
	registerPlatformModelRoutes(legacyGroup, modelService, yamlSource)

	response := governanceRequest(t, router, login.Token, http.MethodGet, "/api/v1/platform/models?page=1&page_size=20", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Items []struct {
			ID       string `json:"id"`
			Token    string `json:"token"`
			Source   string `json:"source"`
			ReadOnly bool   `json:"read_only"`
		} `json:"items"`
		Total    int64 `json:"total"`
		Page     int   `json:"page"`
		PageSize int   `json:"page_size"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	assert.Equal(t, int64(3), envelope.Total)
	assert.Equal(t, 1, envelope.Page)
	assert.Equal(t, 20, envelope.PageSize)
	require.Len(t, envelope.Items, 3)
	assert.Equal(t, "collision-id", envelope.Items[0].ID)
	assert.Equal(t, "platform", envelope.Items[0].Source)
	assert.False(t, envelope.Items[0].ReadOnly)
	assert.Equal(t, "collision-id", envelope.Items[1].ID)
	assert.Equal(t, "yaml", envelope.Items[1].Source)
	assert.True(t, envelope.Items[1].ReadOnly)
	for _, item := range envelope.Items {
		assert.Equal(t, platformmodels.MaskedToken, item.Token)
	}
	for _, secret := range []string{"database-plaintext-token", "yaml-collision-token", "yaml-only-token"} {
		assert.NotContains(t, response.Body.String(), secret)
	}
	response = governanceRequest(t, router, login.Token, http.MethodGet, "/api/v1/platform/models?page_size=1000", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	assert.Equal(t, 100, envelope.PageSize)

	for _, query := range []string{"?page=0", "?page=bad", "?page=1001", "?page=9223372036854775807", "?page_size=0", "?page_size=bad"} {
		response = governanceRequest(t, router, login.Token, http.MethodGet, "/api/v1/platform/models"+query, nil)
		assert.Equal(t, http.StatusBadRequest, response.Code, query)
		assert.JSONEq(t, `{"error":"invalid model request"}`, response.Body.String(), query)
	}
	yamlSource.loadErr = errors.New("C:/private/models.yaml: yaml-loader-token-sentinel")
	loadsBeforeCachedRequest := yamlSource.loadCalls
	response = governanceRequest(t, router, login.Token, http.MethodGet, "/api/v1/platform/models", nil)
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, loadsBeforeCachedRequest, yamlSource.loadCalls, "catalog requests reuse the process-local YAML snapshot")
	assert.NotContains(t, response.Body.String(), "yaml-loader-token-sentinel")
}

func TestGovernanceModelSafeCatalogCachesFixedInitialYAMLFailure(t *testing.T) {
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	admin, err := identityService.CreateUser(ctx, identity.CreateUserInput{
		Username: "catalog-failure-admin", Password: "catalog-password", Role: identity.RoleAdmin,
	})
	require.NoError(t, err)
	login, err := identityService.Authenticate(ctx, admin.Username, "catalog-password")
	require.NoError(t, err)
	modelService := platformmodels.NewService(platformmodels.NewMemoryRepository(), nil, nil)
	yamlSource := &stubYAMLModelSource{loadErr: errors.New("C:/private/models.yaml: token-sentinel")}
	configureSafeModelCatalog(modelService, yamlSource)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	policy := identity.CookiePolicy{}
	group := router.Group("/api/v1/platform", setupIdentityMiddleware(identityService, policy), identity.RequirePasswordChangeCompleted(), identity.RequireCSRF(policy))
	registerGovernanceModelRoutes(group.Group("/models"), modelService)
	for attempt := 0; attempt < 2; attempt++ {
		response := governanceRequest(t, router, login.Token, http.MethodGet, "/api/v1/platform/models", nil)
		assert.Equal(t, http.StatusInternalServerError, response.Code)
		assert.JSONEq(t, `{"error":"model catalog request failed"}`, response.Body.String())
		assert.NotContains(t, response.Body.String(), "models.yaml")
		assert.NotContains(t, response.Body.String(), "token-sentinel")
	}
	assert.Equal(t, 1, yamlSource.loadCalls)
}

type legacyModelMutationRows struct {
	PlatformModels int64
	LegacyModels   int64
	AuditEvents    int64
	AuditOutbox    int64
}

func countLegacyModelMutationRows(t *testing.T, db *gorm.DB) legacyModelMutationRows {
	t.Helper()
	var counts legacyModelMutationRows
	require.NoError(t, db.Model(&platformmodels.Model{}).Count(&counts.PlatformModels).Error)
	require.NoError(t, db.Model(&database.Model{}).Count(&counts.LegacyModels).Error)
	require.NoError(t, db.Model(&platformaudit.Event{}).Count(&counts.AuditEvents).Error)
	require.NoError(t, db.Model(&platformaudit.CompletionOutbox{}).Count(&counts.AuditOutbox).Error)
	return counts
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
