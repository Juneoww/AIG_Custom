package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Juneoww/AIG_Custom/common/runner"
	"github.com/Juneoww/AIG_Custom/common/utils/models"
	"github.com/Juneoww/AIG_Custom/internal/options"
	"github.com/Juneoww/AIG_Custom/pkg/httpx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTargetCredentialLocalRulesInitializeRealBundle(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	dataRoot := filepath.Join(filepath.Dir(source), "..", "..", "data")
	t.Setenv("AIG_DATA_DIR", dataRoot)
	var knowledgeRequests atomic.Int32
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		knowledgeRequests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer platform.Close()
	for _, language := range []string{"zh", "en"} {
		t.Run(language, func(t *testing.T) {
			opts := &options.Options{Target: []string{"https://example.com"}, TimeOut: 1, RateLimit: 1, FPTemplates: platform.URL, AdvTemplates: platform.URL, LoadRemote: true,
				TargetAuth: &httpx.TargetAuth{CredentialID: "local-rules", Revision: 1, Origin: "https://example.com", Headers: map[string]string{"Authorization": "Bearer local-rules-secret"}}}
			require.NoError(t, configureTargetCredentialRules(opts, language))
			require.False(t, opts.LoadRemote)
			require.True(t, filepath.IsAbs(opts.FPTemplates))
			require.True(t, filepath.IsAbs(opts.AdvTemplates))
			var initialization []string
			opts.Callback = func(event interface{}) {
				if step, ok := event.(runner.Step01); ok {
					initialization = append(initialization, step.Text)
				}
			}
			scan, err := runner.New(opts)
			require.NoError(t, err)
			defer scan.Close()
			countRules := func(path string) int {
				count := 0
				require.NoError(t, filepath.WalkDir(path, func(path string, entry fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yaml") {
						count++
					}
					return nil
				}))
				return count
			}
			require.Len(t, scan.GetFpAndVulList(), countRules(opts.FPTemplates))
			advisoryDir := opts.AdvTemplates
			if language == "en" {
				advisoryDir += "_en"
			}
			require.Contains(t, initialization, fmt.Sprintf("Loading vulnerability database, count:%d", countRules(advisoryDir)))
		})
	}
	require.Zero(t, knowledgeRequests.Load())
}

func TestTargetCredentialRuleSelectionPreservesAnonymousConfiguration(t *testing.T) {
	t.Setenv("AIG_DATA_DIR", filepath.Join(t.TempDir(), "missing"))
	opts := &options.Options{FPTemplates: "platform:8088", AdvTemplates: "platform:8088", LoadRemote: true}
	require.NoError(t, configureTargetCredentialRules(opts, "en"))
	require.True(t, opts.LoadRemote)
	require.Equal(t, "platform:8088", opts.FPTemplates)
	require.Equal(t, "platform:8088", opts.AdvTemplates)
	require.Empty(t, opts.Language)
}

func TestTargetCredentialUsesLocalRulesWithoutKnowledgeRequest(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	t.Setenv("AIG_DATA_DIR", filepath.Join(filepath.Dir(source), "..", "..", "data"))
	var knowledgeRequests atomic.Int32
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		knowledgeRequests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer platform.Close()
	// 不信任的测试证书使目标扫描立即失败；规则初始化不应依赖平台用户会话。
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("target request must not pass the untrusted certificate")
	}))
	defer target.Close()
	auth := &httpx.TargetAuth{CredentialID: "local-rules", Revision: 1, Origin: target.URL, Headers: map[string]string{"Authorization": "Bearer local-rules-secret"}}
	params, err := json.Marshal(ScanRequest{Target: []string{target.URL}, Timeout: 1, TargetAuth: auth, TargetCredentialID: auth.CredentialID, TargetCredentialRevision: auth.Revision})
	require.NoError(t, err)
	var successReports atomic.Int32
	callbacks := agentTaskCallbacks()
	callbacks.ResultCallback = func(map[string]interface{}) { successReports.Add(1) }
	err = (&AIInfraScanAgent{Server: strings.TrimPrefix(platform.URL, "http://")}).Execute(context.Background(), TaskRequest{Content: target.URL, Params: params}, callbacks)
	assert.EqualError(t, err, "基础设施认证目标请求失败")
	assert.Zero(t, successReports.Load(), "failed authenticated access must not produce a success report")
	assert.Zero(t, knowledgeRequests.Load(), "authenticated scans must load the Agent rule bundle without calling browser knowledge APIs")
}

func TestTargetCredentialSkipsUnscopedBrowserEvidence(t *testing.T) {
	for _, model := range []*models.OpenAI{nil, {BaseUrl: "https://must-not-contact.example.com", Key: "must-not-use"}} {
		screenshot, vulnerability, summary, err := captureInfrastructureVisualEvidence(&httpx.TargetAuth{}, "https://must-not-contact.example.com", "response", "zh", model)
		require.NoError(t, err)
		require.Nil(t, screenshot)
		require.Nil(t, vulnerability)
		require.Empty(t, summary)
	}
}

func TestTargetCredentialAgentFailsClosed(t *testing.T) {
	scan := ScanRequest{TargetCredentialID: "id", TargetCredentialRevision: 1, Target: []string{"https://example.com/api/version"}}
	require.Error(t, validateScanTargetAuth(TaskRequest{}, scan))
	scan.TargetAuth = &httpx.TargetAuth{CredentialID: "id", Revision: 1, Origin: "https://example.com", Headers: map[string]string{"Authorization": "Bearer test-secret"}}
	require.NoError(t, validateScanTargetAuth(TaskRequest{}, scan))
	scan.Target = []string{"https://other.example.com"}
	require.Error(t, validateScanTargetAuth(TaskRequest{}, scan))
	scan.Target = []string{"https://example.com"}
	scan.Headers = map[string]string{"Host": "other.example.com"}
	require.Error(t, validateScanTargetAuth(TaskRequest{}, scan))
}

func TestTargetCredentialAgentHTTPAssignment(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(fmt.Sprint(allowed), func(t *testing.T) {
			var auth httpx.TargetAuth
			require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"credential_id":"http-target","revision":1,"origin":"http://example.com:80","allow_insecure_http":%t,"headers":{"Authorization":"Bearer http-test-secret"}}`, allowed)), &auth))
			scan := ScanRequest{TargetCredentialID: auth.CredentialID, TargetCredentialRevision: auth.Revision, TargetAuth: &auth, Target: []string{"http://example.com/api/version"}}
			err := validateScanTargetAuth(TaskRequest{}, scan)
			if !allowed {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			for _, target := range []string{"https://example.com/api", "http://example.com:8080/api", "http://other.example/api", "example.com", "10.0.0.1-10.0.0.2"} {
				scan.Target = []string{target}
				require.Error(t, validateScanTargetAuth(TaskRequest{}, scan))
			}
		})
	}
	assert.Equal(t, "infra-target-auth-v2", TargetCredentialCapability)
}
