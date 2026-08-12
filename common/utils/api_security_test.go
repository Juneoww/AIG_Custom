package utils

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUploadFileUsesMappedSessionAndInternalToken(t *testing.T) {
	const token = "internal-upload-token"
	t.Setenv("AIG_AGENT_TOKEN", token)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/api/v1/app/tasks/platform-task/uploadFile", request.URL.Path)
		assert.Equal(t, token, request.Header.Get("X-Internal-Agent-Token"))
		assert.Empty(t, request.Header.Get("X-APIKey"))
		file, _, err := request.FormFile("file")
		require.NoError(t, err)
		defer file.Close()
		contents, err := io.ReadAll(io.LimitReader(file, 8))
		require.NoError(t, err)
		assert.Equal(t, "private", string(contents))
		writer.Header().Set("Content-Type", "application/json")
		_, err = io.WriteString(writer, `{"status":0,"data":{"filename":"result.txt","fileUrl":"opaque-id"}}`)
		require.NoError(t, err)
	}))
	defer server.Close()
	filePath := filepath.Join(t.TempDir(), "result.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("private"), 0o600))

	response, err := UploadFile(strings.TrimPrefix(server.URL, "http://"), "platform-task", filePath)
	require.NoError(t, err)
	assert.Equal(t, "opaque-id", response.Data.FileUrl)
}

func TestUploadFileRequiresAgentTokenBeforeRequest(t *testing.T) {
	t.Setenv("AIG_AGENT_TOKEN", "")
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	filePath := filepath.Join(t.TempDir(), "result.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("private"), 0o600))

	_, err := UploadFile(strings.TrimPrefix(server.URL, "http://"), "platform-task", filePath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AIG_AGENT_TOKEN")
	assert.Zero(t, requests.Load())
}

func TestUploadFileErrorsDoNotExposeTokenOrLocalPath(t *testing.T) {
	const token = "internal-upload-secret"
	t.Setenv("AIG_AGENT_TOKEN", token)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
		_, err := io.WriteString(writer, token+` C:\private\result.txt`)
		require.NoError(t, err)
	}))
	defer server.Close()
	filePath := filepath.Join(t.TempDir(), "result.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("private"), 0o600))

	_, err := UploadFile(strings.TrimPrefix(server.URL, "http://"), "platform-task", filePath)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), token)
	assert.NotContains(t, err.Error(), filePath)

	missingPath := filepath.Join(t.TempDir(), "missing.txt")
	_, err = UploadFile(strings.TrimPrefix(server.URL, "http://"), "platform-task", missingPath)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), missingPath)
}

func TestDownloadFileRequiresAgentTokenBeforeRequest(t *testing.T) {
	t.Setenv("AIG_AGENT_TOKEN", "")
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	err := DownloadFile(strings.TrimPrefix(server.URL, "http://"), "session", "attachment", t.TempDir()+"/target")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AIG_AGENT_TOKEN")
	assert.Zero(t, requests.Load())
}

func TestDownloadFileSendsOnlyInternalAgentToken(t *testing.T) {
	const token = "internal-download-token"
	t.Setenv("AIG_AGENT_TOKEN", token)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, token, request.Header.Get("X-Internal-Agent-Token"))
		assert.Empty(t, request.Header.Get("X-APIKey"))
		_, err := io.WriteString(writer, "private")
		require.NoError(t, err)
	}))
	defer server.Close()

	target := t.TempDir() + "/target"
	require.NoError(t, DownloadFile(strings.TrimPrefix(server.URL, "http://"), "session", "attachment", target))
}
