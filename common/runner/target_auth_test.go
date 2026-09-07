package runner

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Juneoww/AIG_Custom/common/fingerprints/preload"
	"github.com/Juneoww/AIG_Custom/internal/gologger"
	"github.com/Juneoww/AIG_Custom/pkg/httpx"
	"github.com/Juneoww/AIG_Custom/pkg/vulstruct"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunnerTargetAuthMainRequestFailurePreventsSuccessSummary(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		t.Run(strconv.FormatBool(authenticated), func(t *testing.T) {
			opts := baseOptions([]string{"https://example.com/ok", "https://example.com/fail"})
			if authenticated {
				opts.TargetAuth = runnerAuthForTest()
			}
			var results, summaries atomic.Int32
			failures := make(chan error, 1)
			opts.Callback = func(event interface{}) {
				switch value := event.(type) {
				case CallbackScanResult:
					results.Add(1)
				case CallbackReportInfo:
					summaries.Add(1)
				case CallbackErrorInfo:
					failures <- value.Error
				}
			}
			r := &Runner{Options: opts, result: make(chan HttpResult)}
			require.NoError(t, r.initStorage())
			defer r.Close()
			r.storeTargets(opts.Target)
			require.NoError(t, r.initComponents())
			r.runDomainRequestFunc = func(target string) error {
				if strings.HasSuffix(target, "/fail") {
					return errors.New("private primary-request failure detail")
				}
				r.result <- HttpResult{URL: target, StatusCode: http.StatusOK}
				return nil
			}
			err := r.RunEnumeration()
			require.Equal(t, int32(1), results.Load(), "already produced results must drain before enumeration returns")
			if authenticated {
				require.EqualError(t, err, "基础设施认证目标请求失败")
				require.EqualError(t, <-failures, "基础设施认证目标请求失败")
				require.Zero(t, summaries.Load())
			} else {
				require.NoError(t, err)
				require.EqualError(t, <-failures, "private primary-request failure detail")
				require.Equal(t, int32(1), summaries.Load())
			}
		})
	}
}

func TestRunnerTargetAuthRejectedStatusSkipsEvidence(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var requests atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(status)
				_, _ = w.Write([]byte("private denial response"))
			}))
			defer target.Close()
			// 此处单独验证 runner 的主响应策略；httpx 的认证传输与 TLS 已有独立覆盖。
			hp, err := httpx.NewHttpx(&httpx.HTTPOptions{})
			require.NoError(t, err)
			opts := baseOptions([]string{target.URL})
			opts.TargetAuth = runnerAuthForTest()
			r := &Runner{Options: opts, hp: hp, result: make(chan HttpResult, 1), fpEngine: preload.New(hp, nil), advEngine: vulstruct.NewAdvisoryEngine()}
			require.EqualError(t, r.runDomainRequest(target.URL), "基础设施目标认证访问被拒绝")
			require.Empty(t, r.result)
			require.Equal(t, int32(1), requests.Load(), "denied primary responses must not trigger fingerprint or evidence requests")
		})
	}
}

func TestRunnerLocalRulesReturnRecoverableInitializationErrors(t *testing.T) {
	fingerprint, err := os.ReadFile(repositoryDataPath(t, "fingerprints/ollama.yaml"))
	require.NoError(t, err)
	advisory, err := os.ReadFile(repositoryDataPath(t, "vuln/AI-Agent-Config/agent-config-disclosure.yaml"))
	require.NoError(t, err)
	// 拦截进程退出以直接证明初始化错误会返回，不会终止承载其他任务的 Agent。
	logger := gologger.StdLogger.Logrus()
	previousExit := logger.ExitFunc
	logger.ExitFunc = func(int) { panic("runner attempted to terminate the Agent") }
	t.Cleanup(func() { logger.ExitFunc = previousExit })
	for _, scenario := range []string{
		"missing fingerprints", "empty fingerprints", "invalid fingerprints",
		"blank fingerprint", "comment fingerprint", "empty object fingerprint",
		"missing advisories", "empty advisories", "invalid advisories",
		"blank advisory", "comment advisory", "empty object advisory", "unreadable mixed advisory",
	} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			fps := filepath.Join(root, "fingerprints")
			vuls := filepath.Join(root, "vuln")
			require.NoError(t, os.Mkdir(fps, 0700))
			require.NoError(t, os.Mkdir(vuls, 0700))
			require.NoError(t, os.WriteFile(filepath.Join(fps, "test.yaml"), fingerprint, 0600))
			require.NoError(t, os.WriteFile(filepath.Join(vuls, "test.yaml"), advisory, 0600))
			opts := baseOptions([]string{"https://example.com"})
			opts.TargetAuth = runnerAuthForTest()
			opts.FPTemplates, opts.AdvTemplates = fps, vuls
			switch scenario {
			case "missing fingerprints":
				opts.FPTemplates = filepath.Join(root, "missing")
			case "empty fingerprints":
				require.NoError(t, os.Remove(filepath.Join(fps, "test.yaml")))
			case "invalid fingerprints":
				require.NoError(t, os.WriteFile(filepath.Join(fps, "test.yaml"), []byte("info: ["), 0600))
			case "blank fingerprint":
				require.NoError(t, os.WriteFile(filepath.Join(fps, "test.yaml"), nil, 0600))
			case "comment fingerprint":
				require.NoError(t, os.WriteFile(filepath.Join(fps, "test.yaml"), []byte("# no fingerprint rule\n"), 0600))
			case "empty object fingerprint":
				require.NoError(t, os.WriteFile(filepath.Join(fps, "test.yaml"), []byte("{}\n"), 0600))
			case "missing advisories":
				opts.AdvTemplates = filepath.Join(root, "missing")
			case "empty advisories":
				require.NoError(t, os.Remove(filepath.Join(vuls, "test.yaml")))
			case "invalid advisories":
				require.NoError(t, os.WriteFile(filepath.Join(vuls, "test.yaml"), []byte("info: ["), 0600))
			case "blank advisory":
				require.NoError(t, os.WriteFile(filepath.Join(vuls, "test.yaml"), nil, 0600))
			case "comment advisory":
				require.NoError(t, os.WriteFile(filepath.Join(vuls, "test.yaml"), []byte("# no advisory rule\n"), 0600))
			case "empty object advisory":
				require.NoError(t, os.WriteFile(filepath.Join(vuls, "test.yaml"), []byte("{}\n"), 0600))
			case "unreadable mixed advisory":
				if runtime.GOOS == "windows" {
					t.Skip("broken-symlink regression runs on the Linux Agent")
				}
				require.NoError(t, os.Symlink(filepath.Join(root, "absent.yaml"), filepath.Join(vuls, "unreadable.yaml")))
			}
			var scan *Runner
			var initErr error
			require.NotPanics(t, func() { scan, initErr = New(opts) })
			if scan != nil {
				scan.Close()
			}
			require.Error(t, initErr)
			require.Nil(t, scan)
			// 修复资源后，同一进程能够继续初始化下一项任务。
			if scenario == "unreadable mixed advisory" {
				require.NoError(t, os.Remove(filepath.Join(vuls, "unreadable.yaml")))
			}
			require.NoError(t, os.WriteFile(filepath.Join(fps, "test.yaml"), fingerprint, 0600))
			require.NoError(t, os.WriteFile(filepath.Join(vuls, "test.yaml"), advisory, 0600))
			opts.FPTemplates, opts.AdvTemplates = fps, vuls
			scan, initErr = New(opts)
			require.NoError(t, initErr)
			scan.Close()
		})
	}
}

func TestRunnerAnonymousKeepsPermissiveLocalRules(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("broken-symlink regression runs on the Linux Agent")
	}
	root := t.TempDir()
	fps := filepath.Join(root, "fingerprints")
	vuls := filepath.Join(root, "vuln")
	require.NoError(t, os.Mkdir(fps, 0700))
	require.NoError(t, os.Mkdir(vuls, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(fps, "empty.yaml"), []byte("{}\n"), 0600))
	advisory, err := os.ReadFile(repositoryDataPath(t, "vuln/AI-Agent-Config/agent-config-disclosure.yaml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(vuls, "valid.yaml"), advisory, 0600))
	require.NoError(t, os.Symlink(filepath.Join(root, "absent.yaml"), filepath.Join(vuls, "unreadable.yaml")))
	opts := baseOptions([]string{"https://example.com"})
	opts.FPTemplates, opts.AdvTemplates = fps, vuls
	scan, err := New(opts)
	require.NoError(t, err)
	defer scan.Close()
	require.Equal(t, 1, scan.advEngine.GetCount())
}

func TestRunnerRejectsTargetAuthOutsideExplicitURLs(t *testing.T) {
	for _, targets := range [][]string{{"http://example.com"}, {"example.com"}, {"10.0.0.1-10.0.0.2"}, {"https://example.com:8443"}, {"https://other.example"}, nil} {
		opts := baseOptions(targets)
		opts.TargetAuth = runnerAuthForTest()
		_, err := (&Runner{Options: opts}).parseTargets()
		assert.Error(t, err, "authenticated runner must reject non-URL and off-origin targets")
	}
}

func runnerAuthForTest() *httpx.TargetAuth {
	return &httpx.TargetAuth{CredentialID: "cred-test", Revision: 1, Origin: "https://example.com", Headers: map[string]string{"Authorization": "Bearer private-target-token"}}
}

func TestRunnerTargetAuthIsPrivateAndReachesHTTPX(t *testing.T) {
	opts := baseOptions([]string{"https://example.com/a", "https://example.com/b"})
	opts.TargetAuth = runnerAuthForTest()
	encoded, err := json.Marshal(opts)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "private-target-token")
	assert.NotContains(t, string(encoded), "TargetAuth")
	r := &Runner{Options: opts}
	targets, err := r.parseTargets()
	require.NoError(t, err)
	err = r.initComponents()
	require.NoError(t, err)
	defer r.Close()
	assert.Same(t, opts.TargetAuth, r.hp.Options.TargetAuth)
	assert.Len(t, targets, 2)
}

func TestRunnerRejectsTargetAuthFilesAndDiscoveryBeforeReading(t *testing.T) {
	for _, discover := range []bool{false, true} {
		opts := baseOptions([]string{"https://example.com"})
		opts.TargetAuth = runnerAuthForTest()
		if discover {
			opts.LocalScan = true
		} else {
			opts.TargetFile = "must-not-read-this-file"
		}
		_, err := (&Runner{Options: opts}).parseTargets()
		require.Error(t, err)
		assert.Equal(t, "target authentication requires explicit URL targets", err.Error())
	}
}

func TestRunnerTargetAuthHTTPAssignment(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(strconv.FormatBool(allowed), func(t *testing.T) {
			opts := baseOptions([]string{"http://example.com/a", "http://EXAMPLE.com:80/b?q=value"})
			opts.TargetAuth = runnerAuthForTest()
			opts.TargetAuth.Origin = "http://example.com"
			require.NoError(t, json.Unmarshal([]byte(`{"allow_insecure_http":`+strconv.FormatBool(allowed)+`}`), opts.TargetAuth))
			r := &Runner{Options: opts}
			targets, err := r.parseTargets()
			if !allowed {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, targets, 2)
			require.NoError(t, r.initComponents())
			defer r.Close()
			assert.Same(t, opts.TargetAuth, r.hp.Options.TargetAuth)
			for _, target := range []string{"https://example.com", "http://example.com:443", "http://other.example", "example.com", "10.0.0.1-10.0.0.2"} {
				opts.Target = []string{target}
				_, err := r.parseTargets()
				require.Error(t, err)
			}
		})
	}
}
