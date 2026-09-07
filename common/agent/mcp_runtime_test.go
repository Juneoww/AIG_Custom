package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/common/utils"
	"github.com/Juneoww/AIG_Custom/internal/gologger"
	"github.com/stretchr/testify/require"
)

func mcpRuntimeRequest() TaskRequest {
	return TaskRequest{SessionId: "session-test", Params: json.RawMessage(`{"source_kind":"service","authorization_confirmed":true,"mcp_proxy_url":"http://127.0.0.1:8088/api/internal/mcp-egress/task-test","task_capability":"capability-secret","effective_transport":"http","model":{"model":"test-model","token":"model-secret","base_url":"https://model.example.test/v1"}}`)}
}

func TestMCPPrivateRuntimeUsesStdinAndRedactsCallbacks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess fixture runs in Docker")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "uv")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$MCP_TEST_ARGV\"\ncat > \"$MCP_TEST_STDIN\"\nprintf '%s\\n' '{\"type\":\"error\",\"content\":\"model-secret capability-secret https://model.example.test/v1\"}'\nprintf '%s\\n' '{\"type\":\"error\",\"content\":\"model-secret capability-secret\"}' >&2\n"), 0700))
	t.Setenv(utils.McpScanDirEnv, dir)
	t.Setenv(utils.UvBinEnv, script)
	t.Setenv("MCP_TEST_ARGV", filepath.Join(dir, "argv"))
	t.Setenv("MCP_TEST_STDIN", filepath.Join(dir, "stdin"))
	t.Setenv("AIG_AGENT_TOKEN", "agent-secret")
	var logs bytes.Buffer
	old := gologger.StdLogger.Logrus().Out
	gologger.StdLogger.Logrus().SetOutput(&logs)
	t.Cleanup(func() { gologger.StdLogger.Logrus().SetOutput(old) })
	var events []string
	request := mcpRuntimeRequest()
	request.Language = "zh_CN"
	err := (&McpTask{Server: "127.0.0.1:8088"}).Execute(context.Background(), request, TaskCallbacks{PlanUpdateCallback: func([]SubTask) {}, ErrorCallback: func(s string) { events = append(events, s) }})
	require.NoError(t, err)
	argv, err := os.ReadFile(filepath.Join(dir, "argv"))
	require.NoError(t, err)
	stdin, err := os.ReadFile(filepath.Join(dir, "stdin"))
	require.NoError(t, err)
	require.Contains(t, string(argv), "--runtime-config-stdin")
	for _, forbidden := range []string{"--api_key", "--server_url", "--header", "--prompt", "--debug", "model-secret", "capability-secret", "https://model.example.test"} {
		require.NotContains(t, string(argv), forbidden)
		require.NotContains(t, logs.String(), forbidden)
		require.NotContains(t, strings.Join(events, " "), forbidden)
	}
	require.Len(t, events, 2)
	var private map[string]any
	require.NoError(t, json.Unmarshal(stdin, &private))
	require.Len(t, private, 4)
	require.Equal(t, "streamable-http", private["effective_transport"])
	require.Equal(t, "capability-secret", private["task_capability"])
	require.NotContains(t, string(stdin), "agent-secret")
}

func TestMCPRejectsLegacyAndInvalidRuntimeWithoutEcho(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"headers":{"X-Secret-Name":"target-secret"},"model":{"token":"model-secret"}}`, `{"source_kind":"service","authorization_confirmed":true,"effective_transport":"auto"}`} {
		req := mcpRuntimeRequest()
		req.Params = json.RawMessage(raw)
		req.Content = "https://target-secret.example/mcp"
		err := (&McpTask{Server: "127.0.0.1:8088"}).Execute(context.Background(), req, TaskCallbacks{PlanUpdateCallback: func([]SubTask) {}})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
	}
}

func TestMCPCanceledRuntimeDoesNotEcho(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (&McpTask{Server: "127.0.0.1:8088"}).Execute(ctx, mcpRuntimeRequest(), TaskCallbacks{})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret")
}

func TestMCPRuntimeRedactorDecodesJSONEscapesBeforeCallbacks(t *testing.T) {
	params, err := parseMCPRuntime("127.0.0.1:8088", mcpRuntimeRequest())
	require.NoError(t, err)
	line := mcpRuntimeRedactor(params)(`{"type":"error","content":"model-\u0073ecret https:\/\/model.example.test\/v1"}`)
	var output map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &output))
	require.NotContains(t, output["content"], "model-secret")
	require.NotContains(t, output["content"], "https://model.example.test/v1")
}

func TestMCPRuntimeModelIsOptionalButPartialAndNullFail(t *testing.T) {
	request := mcpRuntimeRequest()
	var values map[string]any
	require.NoError(t, json.Unmarshal(request.Params, &values))
	delete(values, "model")
	request.Params, _ = json.Marshal(values)
	parsed, err := parseMCPRuntime("127.0.0.1:8088", request)
	require.NoError(t, err)
	private, err := json.Marshal(parsed.mcpPrivateConfig)
	require.NoError(t, err)
	require.NotContains(t, string(private), `"model"`)
	for _, model := range []any{nil, map[string]any{}, map[string]any{"model": "configured"}, map[string]any{"model": "configured", "token": "secret"}} {
		values["model"] = model
		request.Params, _ = json.Marshal(values)
		_, err := parseMCPRuntime("127.0.0.1:8088", request)
		require.Error(t, err)
	}
}

func TestMCPPrivateRuntimeForcesUTF8OverParentEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess fixture runs in Docker")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "uv")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' \"$PYTHONUTF8\" \"$PYTHONIOENCODING\" > \"$MCP_TEST_ENCODING\"\n"), 0700))
	t.Setenv(utils.UvBinEnv, script)
	t.Setenv(utils.McpScanDirEnv, dir)
	t.Setenv("PYTHONUTF8", "0")
	t.Setenv("PYTHONIOENCODING", "gbk")
	t.Setenv("MCP_TEST_ENCODING", filepath.Join(dir, "encoding"))
	require.NoError(t, (&McpTask{Server: "127.0.0.1:8088"}).Execute(context.Background(), mcpRuntimeRequest(), TaskCallbacks{PlanUpdateCallback: func([]SubTask) {}}))
	got, err := os.ReadFile(filepath.Join(dir, "encoding"))
	require.NoError(t, err)
	require.Equal(t, "1\nutf-8\n", string(got))
}
