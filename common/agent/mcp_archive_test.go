package agent

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/common/utils"
	"github.com/stretchr/testify/require"
)

func mcpTestZIP(t *testing.T, name string, mode os.FileMode) []byte {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	h := &zip.FileHeader{Name: name}
	h.SetMode(mode)
	w, err := z.CreateHeader(h)
	require.NoError(t, err)
	_, err = w.Write([]byte("source"))
	require.NoError(t, err)
	require.NoError(t, z.Close())
	return buf.Bytes()
}

func TestMCPArchiveDownloadBoundedAndAuthenticated(t *testing.T) {
	t.Setenv("AIG_AGENT_TOKEN", "agent-secret")
	body := mcpTestZIP(t, "src/main.py", 0600)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/internal/mcp-archives/session-test/opaque-test", r.URL.Path)
		require.Equal(t, "agent-secret", r.Header.Get("X-Internal-Agent-Token"))
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	dir := t.TempDir()
	require.NoError(t, downloadMCPArchive(context.Background(), srv.URL, "session-test", "archive:opaque-test", dir))
	content, err := os.ReadFile(filepath.Join(dir, "src", "main.py"))
	require.NoError(t, err)
	require.Equal(t, "source", string(content))
}

func TestMCPArchiveRejectsUnsafeEntries(t *testing.T) {
	for _, name := range []string{"../target-secret", "/absolute", "C:/drive", "dir\\escape", "safe/../../escape", "safe/./alias", "CON", "a:b", "dir/name. ", "link"} {
		t.Run(name, func(t *testing.T) {
			mode := os.FileMode(0600)
			if name == "link" {
				mode |= os.ModeSymlink
			}
			err := extractMCPArchive(mcpTestZIP(t, name, mode), t.TempDir())
			require.Error(t, err)
			require.NotContains(t, err.Error(), name)
		})
	}
}

func TestMCPArchiveRejectsRedirectAndInvalidReferences(t *testing.T) {
	t.Setenv("AIG_AGENT_TOKEN", "agent-secret")
	var hit bool
	dst := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer dst.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dst.URL+"/target-secret", 302) }))
	defer src.Close()
	for _, ref := range []string{"archive:opaque-test", "https://target-secret.example/archive.zip", "archive:../target-secret"} {
		err := downloadMCPArchive(context.Background(), src.URL, "session-test", ref, t.TempDir())
		require.Error(t, err)
		require.False(t, strings.Contains(err.Error(), "secret"))
	}
	require.False(t, hit)
}

func TestMCPRepositoryRuntimeScansOnlyInternalArchive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess fixture runs in Docker")
	}
	t.Setenv("AIG_AGENT_TOKEN", "agent-secret")
	source := mcpTestZIP(t, "src/main.py", 0600)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/internal/mcp-archives/session-test/opaque-test", r.URL.Path)
		require.Equal(t, "agent-secret", r.Header.Get("X-Internal-Agent-Token"))
		_, _ = w.Write(source)
	}))
	defer srv.Close()
	dir := t.TempDir()
	bin := filepath.Join(dir, "uv")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$MCP_TEST_ARGV\"\ncat > \"$MCP_TEST_STDIN\"\nfor arg in \"$@\"; do last=\"$arg\"; done\ntest -f \"$last/src/main.py\"\n"), 0700))
	t.Setenv(utils.UvBinEnv, bin)
	t.Setenv(utils.McpScanDirEnv, dir)
	t.Setenv("MCP_TEST_ARGV", filepath.Join(dir, "argv"))
	t.Setenv("MCP_TEST_STDIN", filepath.Join(dir, "stdin"))
	request := TaskRequest{SessionId: "session-test", Language: "zh_CN", Params: json.RawMessage(`{"source_kind":"repository","archive_ref":"archive:opaque-test","model":{"model":"governed","token":"model-secret","base_url":"https://model.example/v1"}}`)}
	require.NoError(t, (&McpTask{Server: srv.URL}).Execute(context.Background(), request, TaskCallbacks{PlanUpdateCallback: func([]SubTask) {}}))
	argv, err := os.ReadFile(filepath.Join(dir, "argv"))
	require.NoError(t, err)
	private, err := os.ReadFile(filepath.Join(dir, "stdin"))
	require.NoError(t, err)
	require.Contains(t, string(argv), "--repo")
	require.Contains(t, string(argv), "--runtime-config-stdin")
	require.NotContains(t, string(argv), "archive:")
	require.NotContains(t, string(argv), "model-secret")
	require.NotContains(t, string(argv), srv.URL)
	require.Contains(t, string(private), "archive:opaque-test")
	require.Contains(t, string(private), "model-secret")
	folder := strings.TrimSpace(string(argv))
	parts := strings.Split(folder, "\n")
	_, err = os.Stat(parts[len(parts)-1])
	require.True(t, os.IsNotExist(err), "extracted source must be removed after execution")
}
