package mcpegress

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceRedactsBoundSecretsAndRuntimeShapedAgentEvents(t *testing.T) {
	const (
		taskID         = "event-redactor-task"
		endpoint       = "https://mcp.allowed.example.test/private-runtime-endpoint"
		bearerSecret   = "event-redactor-bearer-secret"
		headerName     = "X-Intranet-Role"
		headerValue    = "event-redactor-header-value"
		taskCapability = "event-redactor-capability"
		internalPath   = `C:\\private\\mcp-archives\\source.zip`
	)
	service := newProxyServiceFixture(t, taskID, endpoint, bearerSecret, "event-redactor-issued-capability")
	bindings := service.bindings.(*memoryBindingReader)
	config := bindings.configs["proxy-service-config"]
	version := bindings.versions[connectionVersionKey(config.ID, 1)]
	require.NoError(t, service.keyring.SealConnectionPayload(config, version, mcpconnections.ConnectionPayload{
		Endpoint: endpoint,
		Authentication: mcpconnections.Authentication{
			Kind: mcpconnections.AuthenticationBearer, Secret: bearerSecret,
		},
		Headers: []mcpconnections.Header{{Name: headerName, Value: headerValue}},
	}))
	event := map[string]any{
		"message":         "failed against " + endpoint + " with " + headerName + "=" + headerValue,
		"task_capability": taskCapability,
		"nested": map[string]any{
			"authorization": "Bearer " + bearerSecret,
			"archive_path":  internalPath,
		},
	}

	redacted, err := service.RedactMCPEvent(context.Background(), taskID, event)

	require.NoError(t, err)
	encoded, marshalErr := json.Marshal(redacted)
	require.NoError(t, marshalErr)
	for _, secret := range []string{endpoint, bearerSecret, headerName, headerValue, taskCapability, internalPath} {
		assert.NotContains(t, string(encoded), secret)
	}
	assert.Contains(t, string(encoded), "已脱敏")
}

func TestServiceRedactsSensitiveMapKeysWithoutChangingSafeEventStructure(t *testing.T) {
	const endpoint = "https://mcp.allowed.example.test/private-map-key"
	const secret = "short-secret"
	service := newProxyServiceFixture(t, "redact-map-key", endpoint, secret, "issued-capability")
	event := map[string]any{
		"text":      "发现潜在风险，请检查工具权限。",
		"timestamp": 123,
		"tools": []any{map[string]any{
			"name": "read_document", endpoint: "do not preserve URL keys", secret: "do not preserve secret keys",
		}},
	}
	redacted, err := service.RedactMCPEvent(context.Background(), "redact-map-key", event)
	require.NoError(t, err)
	encoded, err := json.Marshal(redacted)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), endpoint)
	assert.NotContains(t, string(encoded), secret)
	assert.Contains(t, string(encoded), "read_document")
	assert.Contains(t, string(encoded), "发现潜在风险")
	assert.Contains(t, string(encoded), `"timestamp":123`)
	assert.Contains(t, event["tools"].([]any)[0].(map[string]any), secret, "redaction must not mutate input")
}

func TestServiceRedactionPreservesDistinctToolUUIDsAndReferences(t *testing.T) {
	const firstID = "123e4567-e89b-12d3-a456-426614174000"
	const secondID = "123e4567-e89b-12d3-a456-426614174001"
	service := newProxyServiceFixture(t, "tool-identities", "https://mcp.allowed.example.test/mcp", "bound-secret", "issued-capability")
	event := map[string]any{"tools": []any{
		map[string]any{"tool_id": firstID}, map[string]any{"tool_id": secondID},
	}, "action": map[string]any{"tool_id": firstID}}
	redacted, err := service.RedactMCPEvent(context.Background(), "tool-identities", event)
	require.NoError(t, err)
	encoded, err := json.Marshal(redacted)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), firstID)
	assert.Contains(t, string(encoded), secondID)

	// A credential that happens to be UUID-shaped is still a credential.
	service = newProxyServiceFixture(t, "secret-identity", "https://mcp.allowed.example.test/mcp", firstID, "issued-capability")
	redacted, err = service.RedactMCPEvent(context.Background(), "secret-identity", event)
	require.NoError(t, err)
	encoded, err = json.Marshal(redacted)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), firstID)
	assert.Contains(t, string(encoded), secondID)
}

func TestServiceRedactsExactNumericBoundSecrets(t *testing.T) {
	service := newProxyServiceFixture(t, "numeric-secret", "https://mcp.allowed.example.test/mcp", "123456789", "issued-capability")
	redacted, err := service.RedactMCPEvent(context.Background(), "numeric-secret", map[string]any{
		"detail": 123456789, "count": 17,
	})
	require.NoError(t, err)
	encoded, err := json.Marshal(redacted)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "123456789")
	assert.Contains(t, string(encoded), `"count":17`)
}

func TestServiceRedactionPreservesStatusAndPlanStepReferences(t *testing.T) {
	const statusID = "123e4567-e89b-12d3-a456-426614174010"
	const stepID = "123e4567-e89b-12d3-a456-426614174011"
	for _, test := range []struct {
		name       string
		secret     string
		wantStatus string
		wantStep   string
	}{
		{"normal references", "bound-secret", statusID, stepID},
		{"status matches credential", statusID, "[已脱敏]", stepID},
		{"step matches credential", stepID, statusID, "[已脱敏]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newProxyServiceFixture(t, "status-references", "https://mcp.allowed.example.test/mcp", test.secret, "issued-capability")
			event := map[string]any{
				"planStep":     map[string]any{"id": stepID},
				"statusUpdate": map[string]any{"id": statusID, "planStepId": stepID},
				"toolUsed":     map[string]any{"statusId": statusID, "planStepId": stepID},
			}
			redacted, err := service.RedactMCPEvent(context.Background(), "status-references", event)
			require.NoError(t, err)
			result := redacted.(map[string]any)
			assert.Equal(t, test.wantStep, result["planStep"].(map[string]any)["id"])
			assert.Equal(t, test.wantStatus, result["statusUpdate"].(map[string]any)["id"])
			assert.Equal(t, test.wantStep, result["statusUpdate"].(map[string]any)["planStepId"])
			assert.Equal(t, test.wantStatus, result["toolUsed"].(map[string]any)["statusId"])
			assert.Equal(t, test.wantStep, result["toolUsed"].(map[string]any)["planStepId"])
		})
	}
}

func TestServiceRedactsBooleanAndNullBoundSecrets(t *testing.T) {
	for _, test := range []struct {
		secret string
		value  any
	}{
		{"true", true}, {"false", false}, {"null", nil},
	} {
		t.Run(test.secret, func(t *testing.T) {
			service := newProxyServiceFixture(t, "scalar-secret", "https://mcp.allowed.example.test/mcp", test.secret, "issued-capability")
			redacted, err := service.RedactMCPEvent(context.Background(), "scalar-secret", map[string]any{"detail": test.value})
			require.NoError(t, err)
			encoded, err := json.Marshal(redacted)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), test.secret)
		})
	}
}
