package websocket

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	platformtasks "github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/stretchr/testify/require"
)

const validWorkflowProvider = `providers:
  - id: http
    config:
      url: https://agent.example.test/chat
      body: {query: "{{prompt}}"}
      transform_response: answer
`

func TestAgentWorkflowProviderValidation(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		valid      bool
	}{
		{"http", validWorkflowProvider, true},
		{"websocket", `targets: [{id: websocket, config: {url: "wss://agent.example.test/chat"}}]`, true},
		{"list-shorthand", `[{http: {config: {url: "http://127.0.0.1:8181/chat"}}}]`, true},
		{"shorthand-delay", `[{http: {delay: nope, config: {url: "https://example.test"}}}]`, false},
		{"shorthand-label", `[{http: {label: [], config: {url: "https://example.test"}}}]`, false},
		{"dify-chat", `providers: [{id: "dify:chat", config: {apiKey: test-key, apiBaseUrl: "https://dify.example.test/v1", extra: {dify_type: chat}}}]`, true},
		{"dify-workflow", `providers: [{id: "dify:workflow", config: {apiKey: test-key, apiBaseUrl: "https://dify.example.test/v1", extra: {dify_type: workflow, inputs: {context: test}}}}]`, true},
		{"empty", "", false},
		{"unsupported", `providers: [{id: "unknown:local", config: {url: "https://example.test"}}]`, false},
		{"multiple", `providers: [{id: http, config: {url: "https://example.test"}}, {id: http, config: {url: "https://example.test/2"}}]`, false},
		{"both-arrays", validWorkflowProvider + "targets: []\n", false},
		{"wrong-list-type", `providers: {id: http}`, false},
		{"missing-url", `providers: [{id: http}]`, false},
		{"invalid-url", `providers: [{id: http, config: {url: "file:///tmp/key"}}]`, false},
		{"url-credentials", `providers: [{id: http, config: {url: "https://name:secret@example.test"}}]`, false},
		{"headers-type", `providers: [{id: http, config: {url: "https://example.test", headers: [token]}}]`, false},
		{"unknown-config", `providers: [{id: http, config: {url: "https://example.test", script: "run-local"}}]`, false},
		{"ambiguous-scalar", `providers: [{id: http, config: {url: "https://example.test", headers: {X-Flag: on}}}]`, false},
		{"sexagesimal-scalar", `providers: [{id: http, config: {url: "https://example.test", headers: {X-Window: 1:20}}}]`, false},
		{"custom-tag", `!custom {providers: [{id: http, config: {url: "https://example.test"}}]}`, false},
		{"quoted-scalar", `providers: [{id: http, config: {url: "https://example.test", headers: {X-Flag: "on"}}}]`, true},
		{"timestamp", `providers: [{id: http, config: {url: "https://example.test", body: {at: 2026-09-05}}}]`, false},
		{"ambiguous-number", `providers: [{id: http, config: {url: "https://example.test", body: {code: 012}}}]`, false},
		{"dify-route-conflict", `providers: [{id: dify, config: {url: "wss://name:secret@other.example.test/chat", apiKey: test-key, apiBaseUrl: "https://example.test/v1", extra: {dify_type: chat}}}]`, false},
		{"implicit-credentials", `providers: [{id: dify, config: {apiBaseUrl: "https://example.test/v1", extra: {dify_type: chat}}}]`, false},
		{"implicit-base-url", `providers: [{id: dify, config: {apiKey: test-key, extra: {dify_type: chat}}}]`, false},
		{"invalid-extra", `providers: [{id: dify, config: {apiKey: test-key, apiBaseUrl: "https://example.test/v1", extra: {inputs: []}}}]`, false},
		{"duplicate", `providers: [{id: http, config: {url: "https://example.test", url: "https://example.test/2"}}]`, false},
		{"multi-document", validWorkflowProvider + "\n---\n" + validWorkflowProvider, false},
		{"aliases", `providers: [{id: http, config: &config {url: "https://example.test", body: *config}}]`, false},
		{"oversize", strings.Repeat(" ", 1024*1024+1) + validWorkflowProvider, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAgentWorkflowProvider([]byte(tc.data))
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, platformtasks.ErrInvalid)
			}
		})
	}
}

func TestAgentWorkflowReferenceValidationRejectsMalformedConfig(t *testing.T) {
	previous, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(t.TempDir()))
	t.Cleanup(func() { _ = os.Chdir(previous) })
	dir := filepath.Join("data", "agents", PublicUser)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, "target.yaml")
	manager := NewTaskManager(NewAgentManager(), nil, nil, nil, NewSSEManager())
	task := platformtasks.EngineTask{OwnerUsername: "alice", TaskType: "agent_scan", Params: json.RawMessage(`{"agent_id":"target"}`)}
	require.NoError(t, os.WriteFile(path, []byte(validWorkflowProvider), 0o600))
	require.NoError(t, manager.ValidateTaskReferences(context.Background(), task))
	require.NoError(t, os.WriteFile(path, []byte("provider: private-sentinel-key"), 0o600))
	err = manager.ValidateTaskReferences(context.Background(), task)
	require.ErrorIs(t, err, platformtasks.ErrInvalid)
	require.NotContains(t, err.Error(), "private-sentinel-key")

	// 创建之后配置可能变化，下发前必须再次拒绝，不能向 Agent 发送失效配置。
	pair := websocketConnectionPair(t)
	connection := NewAgentConnection(pair.server)
	connection.agentID = "workflow-worker"
	manager.agentManager.connections[connection.agentID] = connection
	manager.tasks["workflow-revalidate"] = &TaskCreateRequest{
		SessionID: "workflow-revalidate", Username: "alice", Task: "Agent-Scan",
		Params: map[string]any{"agent_id": "target"},
	}
	err = manager.dispatchTask("workflow-revalidate", "workflow-validation-test")
	require.EqualError(t, err, "Agent 配置不符合扫描要求")
}
