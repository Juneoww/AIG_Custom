package tasks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/models"
	"github.com/Juneoww/AIG_Custom/internal/platform/targetcredentials"
	"github.com/stretchr/testify/require"
)

func TestInfrastructureTargetCredentialReferenceAndPrivateRuntime(t *testing.T) {
	for _, protocol := range []string{"https", "http"} {
		t.Run(protocol, func(t *testing.T) {
			ctx := context.Background()
			owner := identity.Subject{UserID: "owner", Username: "owner", Role: identity.RoleUser}
			auditService := audit.NewService(audit.NewMemoryRepository())
			keys, err := models.NewKeyring("test", make([]byte, 32), nil)
			require.NoError(t, err)
			credentials := targetcredentials.NewService(targetcredentials.NewMemoryRepository(), keys, auditService)
			input := targetcredentials.Input{Name: "Inference", Origin: protocol + "://inference.example.com", AuthType: "bearer", Secret: "task-secret-sentinel", AllowInsecureHTTP: protocol == "http"}
			credential, err := credentials.Create(ctx, owner, input)
			require.NoError(t, err)
			repository := NewMemoryRepository()
			engine := &recordingEngine{}
			service := NewService(repository, engine, auditService)
			service.SetTargetCredentials(credentials)
			params, err := json.Marshal(map[string]any{"target_credential_id": credential.ID, "target_credential_revision": credential.Revision})
			require.NoError(t, err)
			create := CreateInput{IdempotencyKey: "target-auth-task", TaskType: "ai_infra_scan", Content: input.Origin + "/api/version", Params: params}
			view, err := service.Create(ctx, owner, create)
			require.NoError(t, err)
			require.NotContains(t, string(view.Params), input.Secret)
			require.Nil(t, engine.last.RuntimeParams)
			require.NotNil(t, engine.last.RuntimeIssuer)
			runtime, err := engine.last.RuntimeIssuer(ctx)
			require.NoError(t, err)
			require.True(t, ValidInfrastructureRuntimeAssignment(engine.last.Params, runtime))
			runtimeJSON, err := json.Marshal(runtime)
			require.NoError(t, err)
			require.Contains(t, string(runtimeJSON), input.Secret)
			stored, err := repository.Get(ctx, view.ID)
			require.NoError(t, err)
			require.NotContains(t, string(stored.Params), input.Secret)
			input.Disabled = true
			input.Secret = ""
			_, err = credentials.Update(ctx, owner, credential.ID, credential.Revision, input)
			require.NoError(t, err)
			_, err = engine.last.RuntimeIssuer(ctx)
			require.Error(t, err, "disabled queued credential must fail")
			replay, err := service.Create(ctx, owner, create)
			require.NoError(t, err)
			require.Equal(t, view.ID, replay.ID)
			create.IdempotencyKey = "new-disabled"
			_, err = service.Create(ctx, owner, create)
			require.Error(t, err)
		})
	}
}

func TestInfrastructureHTTPRuntimeRequiresPermission(t *testing.T) {
	safe := json.RawMessage(`{"target_credential_id":"id","target_credential_revision":1}`)
	auth := map[string]any{"credential_id": "id", "revision": 1, "origin": "http://inference.internal", "headers": map[string]string{"Authorization": "Bearer fictional-test-token"}}
	runtime := map[string]any{"target_auth": auth}
	require.False(t, ValidInfrastructureRuntimeAssignment(safe, runtime))
	auth["allow_insecure_http"] = true
	require.True(t, ValidInfrastructureRuntimeAssignment(safe, runtime))
}

func TestInfrastructureTargetCredentialParamsAreStrict(t *testing.T) {
	for _, raw := range []string{
		`{"target_credential_id":"id"}`,
		`{"target_credential_revision":1}`,
		`{"target_credential_id":"id","target_credential_revision":0}`,
		`{"target_credential_id":"id","target_credential_revision":1.2}`,
		`{"target_credential_id":"id","target_credential_revision":null}`,
		`{"target_auth":{"headers":{"Authorization":"secret"}}}`,
	} {
		_, ok := normalizeInfrastructureTaskParams(json.RawMessage(raw))
		require.False(t, ok, raw)
	}
	safe := json.RawMessage(`{"target_credential_id":"id","target_credential_revision":1}`)
	_, ok := normalizeInfrastructureTaskParams(safe)
	require.True(t, ok)
	require.False(t, ValidInfrastructureRuntimeAssignment(safe, nil), "must never silently dispatch anonymously")
	require.False(t, ValidInfrastructureRuntimeAssignment(json.RawMessage(`{}`), map[string]any{"target_auth": "secret"}))
}
