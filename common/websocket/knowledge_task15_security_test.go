package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	platformknowledge "github.com/Juneoww/AIG_Custom/internal/platform/knowledge"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeListPaginationRejectsAmbiguousOrUnsafeValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	changeWorkingDirectory(t, root)
	for _, directory := range []string{"data/fingerprints", "data/vuln", "data/eval"} {
		require.NoError(t, os.MkdirAll(directory, 0o700))
	}
	handlers := map[string]gin.HandlerFunc{
		"fingerprints":    HandleListFingerprints,
		"vulnerabilities": HandleListVulnerabilities(),
		"evaluations":     HandleListEvaluations,
	}
	for name, handler := range handlers {
		for _, query := range []string{"page=", "page=1.5", "page=1001", "page=9223372036854775807", "page=1&page=2", "size=0", "size=101", "size=1&size=2"} {
			t.Run(name+"/"+query, func(t *testing.T) {
				router := gin.New()
				router.GET("/knowledge", handler)
				request := httptest.NewRequest(http.MethodGet, "/knowledge?"+query, nil)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			})
		}
	}
}

func TestVulnerabilityAndEvaluationCreatesPreserveValidatedOriginalBytes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	changeWorkingDirectory(t, root)
	require.NoError(t, os.MkdirAll("data/vuln", 0o700))
	require.NoError(t, os.MkdirAll("data/eval", 0o700))

	identityService := identity.NewService(identity.NewMemoryRepository())
	_, err := identityService.CreateUser(context.Background(), identity.CreateUserInput{Username: "knowledge-admin", Password: "secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	login, err := identityService.Authenticate(context.Background(), "knowledge-admin", "secret")
	require.NoError(t, err)
	policy := identity.CookiePolicy{SessionCookieName: "aig_session"}
	knowledgeHandler := platformknowledge.NewHandler(platformknowledge.NewService(audit.NewService(audit.NewMemoryRepository())))
	router := gin.New()
	router.POST("/vulnerabilities", identity.Authenticate(identityService, policy), knowledgeHandler.Govern(platformknowledge.KindVulnerability, platformknowledge.OperationCreate, HandleCreateVulnerability()))
	router.POST("/evaluations", identity.Authenticate(identityService, policy), knowledgeHandler.Govern(platformknowledge.KindEvaluation, platformknowledge.OperationCreate, HandleCreateEvaluation))

	vulnerability := "# preserve comment\ninfo:\n  name: demo\n  cve: CVE-2026-1000\n  summary: &summary demo\n  details: *summary\n  cvss: 5.0\n  severity: MEDIUM\n  security_advise: fix\n  x-extra: keep\nrule: version = \"1.0\"\nreferences: []\n"
	response := performKnowledgeMutation(t, router, login.Token, http.MethodPost, "/vulnerabilities", vulnerability)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	written, err := os.ReadFile(filepath.Join("data", "vuln", "demo", "CVE-2026-1000.yaml"))
	require.NoError(t, err)
	require.Equal(t, vulnerability, string(written))

	evaluation := "{\n  \"name\": \"eval-original\",\n  \"description\": \"demo\",\n  \"count\": 1,\n  \"x-extra\": {\"keep\": true},\n  \"data\": [{\"prompt\": \"hello\"}]\n}\n"
	response = performKnowledgeMutation(t, router, login.Token, http.MethodPost, "/evaluations", evaluation)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	written, err = os.ReadFile(filepath.Join("data", "eval", "eval-original.json"))
	require.NoError(t, err)
	require.Equal(t, evaluation, string(written))

	mismatched := "{\"name\":\"eval-mismatch\",\"count\":2,\"data\":[{\"prompt\":\"one\"}]}"
	response = performKnowledgeMutation(t, router, login.Token, http.MethodPost, "/evaluations", mismatched)
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	_, err = os.Stat(filepath.Join("data", "eval", "eval-mismatch.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestNestedFingerprintUpdatePreservesResolvedFileLocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	changeWorkingDirectory(t, root)
	nested := filepath.Join("data", "fingerprints", "comfyui")
	require.NoError(t, os.MkdirAll(nested, 0o700))
	originalPath := filepath.Join(nested, "comfyui-comfy-mtb.yaml")
	require.NoError(t, os.WriteFile(originalPath, []byte("info:\n  name: comfy_mtb\n  author: lab\n  severity: info\n"), 0o600))

	identityService := identity.NewService(identity.NewMemoryRepository())
	_, err := identityService.CreateUser(context.Background(), identity.CreateUserInput{Username: "fingerprint-admin", Password: "secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	login, err := identityService.Authenticate(context.Background(), "fingerprint-admin", "secret")
	require.NoError(t, err)
	policy := identity.CookiePolicy{SessionCookieName: "aig_session"}
	knowledgeHandler := platformknowledge.NewHandler(platformknowledge.NewService(audit.NewService(audit.NewMemoryRepository())))
	router := gin.New()
	router.PUT("/fingerprints/:name", identity.Authenticate(identityService, policy), knowledgeHandler.Govern(platformknowledge.KindFingerprint, platformknowledge.OperationUpdate, HandleEditFingerprint))

	updated := "# preserved nested rule\ninfo:\n  name: comfy_mtb\n  author: lab\n  severity: info\n"
	response := performKnowledgeMutation(t, router, login.Token, http.MethodPut, "/fingerprints/comfy_mtb", updated)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	written, err := os.ReadFile(originalPath)
	require.NoError(t, err)
	require.Equal(t, updated, string(written))
	_, err = os.Stat(filepath.Join("data", "fingerprints", "comfy_mtb.yaml"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestNestedFingerprintDeleteRemovesUniqueResolvedFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	changeWorkingDirectory(t, root)
	nested := filepath.Join("data", "fingerprints", "comfyui")
	require.NoError(t, os.MkdirAll(nested, 0o700))
	originalPath := filepath.Join(nested, "comfyui-comfy-mtb.yaml")
	require.NoError(t, os.WriteFile(originalPath, []byte("info:\n  name: comfy_mtb\n  author: lab\n  severity: info\n"), 0o600))

	identityService := identity.NewService(identity.NewMemoryRepository())
	_, err := identityService.CreateUser(context.Background(), identity.CreateUserInput{Username: "fingerprint-admin", Password: "secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	login, err := identityService.Authenticate(context.Background(), "fingerprint-admin", "secret")
	require.NoError(t, err)
	policy := identity.CookiePolicy{SessionCookieName: "aig_session"}
	knowledgeHandler := platformknowledge.NewHandler(platformknowledge.NewService(audit.NewService(audit.NewMemoryRepository())))
	router := gin.New()
	router.DELETE("/fingerprints", identity.Authenticate(identityService, policy), knowledgeHandler.Govern(platformknowledge.KindFingerprint, platformknowledge.OperationDelete, HandleDeleteFingerprint))

	request := httptest.NewRequest(http.MethodDelete, "/fingerprints", bytes.NewBufferString(`{"name":["comfy_mtb"]}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: policy.SessionCookieName, Value: login.Token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	_, err = os.Stat(originalPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestPromptCollectionDeleteMapsInvalidAndMissingIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	changeWorkingDirectory(t, root)
	require.NoError(t, os.MkdirAll(PromptCollectionsRoot, 0o700))

	identityService := identity.NewService(identity.NewMemoryRepository())
	_, err := identityService.CreateUser(context.Background(), identity.CreateUserInput{Username: "prompt-admin", Password: "secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	login, err := identityService.Authenticate(context.Background(), "prompt-admin", "secret")
	require.NoError(t, err)
	policy := identity.CookiePolicy{SessionCookieName: "aig_session"}
	knowledgeHandler := platformknowledge.NewHandler(platformknowledge.NewService(audit.NewService(audit.NewMemoryRepository())))
	router := gin.New()
	router.DELETE("/prompt_collections/:id", identity.Authenticate(identityService, policy), knowledgeHandler.Govern(platformknowledge.KindPromptCollection, platformknowledge.OperationDelete, HandleDelete(promptCollectionDeleteFunc)))

	for _, testCase := range []struct {
		name       string
		id         string
		wantStatus int
	}{
		{name: "invalid", id: "bad..id", wantStatus: http.StatusBadRequest},
		{name: "missing", id: "missing", wantStatus: http.StatusNotFound},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodDelete, "/prompt_collections/"+testCase.id, nil)
			request.AddCookie(&http.Cookie{Name: policy.SessionCookieName, Value: login.Token})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, testCase.wantStatus, response.Code, response.Body.String())
		})
	}
}

func changeWorkingDirectory(t *testing.T, directory string) {
	t.Helper()
	original, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(directory))
	t.Cleanup(func() { require.NoError(t, os.Chdir(original)) })
}

func performKnowledgeMutation(t *testing.T, router http.Handler, token string, method string, path string, content string) *httptest.ResponseRecorder {
	t.Helper()
	body := []byte(`{"file_content":`)
	encoded, err := json.Marshal(content)
	require.NoError(t, err)
	body = append(body, encoded...)
	body = append(body, '}')
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
