package mcpconnections

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/stretchr/testify/require"
)

func TestManagementUpdatePreservesSecretsAndVersionsConditionally(t *testing.T) {
	ctx := context.Background()
	db := openMCPConnectionPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	service := NewService(repository, testKeyring(t), nil, testPolicy(t, true))
	owner := identity.Subject{UserID: "manager", Role: identity.RoleUser}
	input := serviceInput("连接配置", ScopePrivate, TransportHTTP)
	created, err := service.Create(ctx, owner, input)
	require.NoError(t, err)
	detail, err := service.GetEditableDetail(ctx, owner, created.ID)
	require.NoError(t, err)
	require.Equal(t, input.ServerURL, detail.ServerURL)
	require.Equal(t, "1", detail.ResourceRevision)
	encoded, err := json.Marshal(detail)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), input.Authentication.Secret)
	require.NotContains(t, string(encoded), input.Headers[0].Value)
	_, err = service.GetEditableDetail(ctx, identity.Subject{UserID: "audit", Role: identity.RoleAuditor}, created.ID)
	require.Error(t, err)

	name := "更新展示名称"
	updated, err := service.Update(ctx, owner, created.ID, "1", UpdateConnectionInput{Name: &name})
	require.NoError(t, err)
	require.Equal(t, 1, updated.CurrentVersion)
	require.Equal(t, "2", updated.ResourceRevision)
	_, err = service.Update(ctx, owner, created.ID, "1", UpdateConnectionInput{Name: &name})
	require.ErrorIs(t, err, ErrConflict)
	endpoint := "https://safe.example.test/new-mcp"
	updated, err = service.Update(ctx, owner, created.ID, "2", UpdateConnectionInput{ServerURL: &endpoint})
	require.NoError(t, err)
	require.Equal(t, 2, updated.CurrentVersion)
	require.False(t, updated.Enabled)
	require.Equal(t, ProbeStatusNotTested, updated.ProbeStatus)
	config, err := repository.GetConfig(ctx, created.ID)
	require.NoError(t, err)
	version, err := repository.GetVersion(ctx, created.ID, 2)
	require.NoError(t, err)
	payload, err := testKeyring(t).OpenConnectionPayload(config, version)
	require.NoError(t, err)
	require.Equal(t, input.Authentication.Secret, payload.Authentication.Secret)
	require.Equal(t, input.Headers[0].Value, payload.Headers[0].Value)
	old, err := repository.GetVersion(ctx, created.ID, 1)
	require.NoError(t, err)
	oldPayload, err := testKeyring(t).OpenConnectionPayload(config, old)
	require.NoError(t, err)
	require.Equal(t, input.ServerURL, oldPayload.Endpoint)
}
