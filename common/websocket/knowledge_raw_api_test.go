package websocket

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type rawKnowledgeResponse struct {
	Status int `json:"status"`
	Data   struct {
		Content string `json:"content"`
	} `json:"data"`
}

func TestKnowledgeRawHandlerPreservesExactBytesAndRejectsUnsafeNames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	original := "# keep this comment\ninfo: &metadata\n  name: demo\ncopy: *metadata\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "demo.yaml"), []byte(original), 0o600))

	router := gin.New()
	router.GET("/:name/raw", newDirectKnowledgeRawHandler(root, "name", ".yaml"))

	request := httptest.NewRequest(http.MethodGet, "/demo/raw", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	var decoded rawKnowledgeResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &decoded))
	require.Equal(t, 0, decoded.Status)
	require.Equal(t, original, decoded.Data.Content)

	for _, unsafeName := range []string{".", "..", "%2e", "%2e%2e", "%5cdemo", "demo%00name"} {
		t.Run(unsafeName, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/"+unsafeName+"/raw", nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.NotEqual(t, http.StatusOK, response.Code)
			require.NotContains(t, response.Body.String(), root)
		})
	}
}

func TestFingerprintRawHandlerResolvesNestedRuleByInfoName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	nested := filepath.Join(root, "comfyui")
	require.NoError(t, os.MkdirAll(nested, 0o700))
	original := "# exact bytes\ninfo:\n  name: comfy_mtb\n  author: lab\n  desc: demo\n"
	require.NoError(t, os.WriteFile(filepath.Join(nested, "comfyui-comfy-mtb.yaml"), []byte(original), 0o600))

	router := gin.New()
	router.GET("/:name/raw", newFingerprintKnowledgeRawHandler(root, "name"))
	request := httptest.NewRequest(http.MethodGet, "/comfy_mtb/raw", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var decoded rawKnowledgeResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &decoded))
	require.Equal(t, original, decoded.Data.Content)
}

func TestKnowledgeRawHandlerRejectsOversizedAndSymlinkedFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "large.json"), []byte(strings.Repeat("x", int(maxKnowledgeRawBytes+1))), 0o600))

	router := gin.New()
	router.GET("/:name/raw", newDirectKnowledgeRawHandler(root, "name", ".json"))
	request := httptest.NewRequest(http.MethodGet, "/large/raw", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	require.NotContains(t, response.Body.String(), root)

	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel.json")
	require.NoError(t, os.WriteFile(sentinel, []byte(`{"secret":"outside"}`), 0o600))
	if err := os.Symlink(sentinel, filepath.Join(root, "linked.json")); err != nil {
		t.Skipf("当前环境不能创建符号链接: %v", err)
	}
	request = httptest.NewRequest(http.MethodGet, "/linked/raw", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.NotEqual(t, http.StatusOK, response.Code)
	require.NotContains(t, response.Body.String(), "outside")

	linkedRoot := filepath.Join(t.TempDir(), "linked-root")
	if err := os.Symlink(outside, linkedRoot); err != nil {
		t.Skipf("当前环境不能创建目录符号链接: %v", err)
	}
	rootRouter := gin.New()
	rootRouter.GET("/:name/raw", newDirectKnowledgeRawHandler(linkedRoot, "name", ".json"))
	request = httptest.NewRequest(http.MethodGet, "/sentinel/raw", nil)
	response = httptest.NewRecorder()
	rootRouter.ServeHTTP(response, request)
	require.NotEqual(t, http.StatusOK, response.Code)
	require.NotContains(t, response.Body.String(), "outside")
}

func TestVulnerabilityRawHandlerFindsNestedFileWithoutFollowingSymlinks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	nested := filepath.Join(root, "product")
	require.NoError(t, os.MkdirAll(nested, 0o700))
	original := "# exact\ninfo:\n  cve: CVE-2026-1234\nrule: version >= 1\n"
	require.NoError(t, os.WriteFile(filepath.Join(nested, "CVE-2026-1234.yaml"), []byte(original), 0o600))

	router := gin.New()
	router.GET("/:id/raw", newNestedKnowledgeRawHandler(root, "id", ".yaml"))
	request := httptest.NewRequest(http.MethodGet, "/CVE-2026-1234/raw", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	var decoded rawKnowledgeResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &decoded))
	require.Equal(t, original, decoded.Data.Content)
}
