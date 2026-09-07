package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/common/utils"
)

const agentTaskSafeResult = `{"schema_version":"agent-security-report@1","score":100,"risk_type":"safe","total_tests":1,"vulnerable_tests":0,"results":[]}`
const agentTaskVulnResult = `{"schema_version":"agent-security-report@1","score":85,"risk_type":"high","total_tests":1,"vulnerable_tests":1,"results":[{"id":"f-001","title":"Unauthorized record access","description":"The target returned the restricted record","level":"High"}]}`

// TestMain 在独立进程中模拟 uv，验证真实参数、环境和输出管道。
func TestMain(m *testing.M) {
	if os.Getenv("AIG_AGENT_TASK_HELPER") == "1" {
		runAgentTaskHelper()
		return
	}
	os.Exit(m.Run())
}

func runAgentTaskHelper() {
	if os.Getenv("AIG_AGENT_TASK_CHILD") == "1" {
		_ = os.WriteFile(os.Getenv("AIG_AGENT_TASK_CAPTURE")+".child-started", []byte("ready"), 0600)
		time.Sleep(2 * time.Second)
		_ = os.WriteFile(os.Getenv("AIG_AGENT_TASK_CAPTURE")+".child-survived", []byte("survived"), 0600)
		return
	}
	if os.Getenv("AIG_AGENT_TASK_TREE") == "1" {
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "AIG_AGENT_TASK_CHILD=1")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			panic(err)
		}
		defer child.Wait()
	}
	args := os.Args[1:]
	capture := map[string]interface{}{"args": args, "key": os.Getenv("OPENROUTER_API_KEY")}
	for i, arg := range args {
		if (arg == "--agent_provider" || arg == "--prompt-file") && i+1 < len(args) {
			path := args[i+1]
			data, err := os.ReadFile(path)
			if err != nil {
				panic(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				panic(err)
			}
			capture[arg] = string(data)
			capture[arg+"-path"] = path
			capture[arg+"-mode"] = uint32(info.Mode().Perm())
		}
	}
	data, _ := json.Marshal(capture)
	if err := os.WriteFile(os.Getenv("AIG_AGENT_TASK_CAPTURE"), data, 0600); err != nil {
		panic(err)
	}
	fmt.Fprintln(os.Stderr, os.Getenv("AIG_AGENT_TASK_RAW_LOGS"))
	if os.Getenv("AIG_AGENT_TASK_OVERSIZED") == "1" {
		for i := 0; i < 100; i++ {
			if _, err := os.Stat(os.Getenv("AIG_AGENT_TASK_CAPTURE") + ".child-started"); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		fmt.Fprintln(os.Stderr, strings.Repeat("x", 11*1024*1024))
	}
	for _, event := range strings.Split(os.Getenv("AIG_AGENT_TASK_EVENTS"), "\n") {
		if event != "" {
			fmt.Fprintln(os.Stderr, os.Getenv("AIG_SCAN_EVENT_PREFIX")+event)
		}
	}
	if os.Getenv("AIG_AGENT_TASK_WAIT") == "1" {
		time.Sleep(30 * time.Second)
	}
	if os.Getenv("AIG_AGENT_TASK_FAIL") == "1" {
		os.Exit(7)
	}
	_ = os.WriteFile(os.Getenv("AIG_AGENT_TASK_CAPTURE")+".exited", []byte("done"), 0600)
}

func agentTaskRequest() TaskRequest {
	return TaskRequest{
		Content: "检查工作流：只使用测试目标。\n保留引号 ' \" 和 $()。",
		Params:  json.RawMessage(`{"agent_data":"providers: []\n# private provider config","eval_model":{"model":"selected-model","token":"selected-test-token","base_url":"http://127.0.0.1:19999/v1"}}`),
	}
}

func agentTaskCallbacks() TaskCallbacks {
	return TaskCallbacks{
		ResultCallback:           func(map[string]interface{}) {},
		ToolUseLogCallback:       func(string, string, string, string) {},
		ToolUsedCallback:         func(string, string, string, []Tool) {},
		NewPlanStepCallback:      func(string, string) {},
		StepStatusUpdateCallback: func(string, string, string, string, string) {},
		PlanUpdateCallback:       func([]SubTask) {},
		ErrorCallback:            func(string) {},
	}
}

func setupAgentTaskHelper(t *testing.T, events string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	capture := filepath.Join(dir, "capture.json")
	t.Setenv(utils.UvBinEnv, executable)
	t.Setenv(utils.AgentScanDirEnv, dir)
	t.Setenv("AIG_AGENT_TASK_HELPER", "1")
	t.Setenv("AIG_AGENT_TASK_CAPTURE", capture)
	t.Setenv("AIG_AGENT_TASK_EVENTS", events)
	return capture
}

func TestAgentTaskPassesPrivatePromptAndGovernedCredentials(t *testing.T) {
	capture := setupAgentTaskHelper(t, `{"type":"resultUpdate","content":`+agentTaskSafeResult+`}`)
	request := agentTaskRequest()
	if err := (&AgentTask{}).Execute(context.Background(), request, agentTaskCallbacks()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["--prompt-file"] != request.Content {
		t.Errorf("prompt was not passed intact: %#v", got["--prompt-file"])
	}
	if got["key"] != "selected-test-token" {
		t.Error("selected credential was not passed through the environment")
	}
	args, _ := json.Marshal(got["args"])
	if strings.Contains(string(args), "selected-test-token") || strings.Contains(string(args), "检查工作流") {
		t.Error("private prompt or credential leaked into command arguments")
	}
	if !strings.Contains(string(args), "--governed-model") {
		t.Error("platform execution did not govern auxiliary models")
	}
	for _, arg := range []string{"--agent_provider", "--prompt-file"} {
		if path, ok := got[arg+"-path"].(string); ok {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("temporary %s was not removed", arg)
			}
		} else {
			t.Errorf("missing %s file", arg)
		}
		if runtime.GOOS != "windows" && got[arg+"-mode"] != float64(0600) {
			t.Errorf("%s was not owner-only: %v", arg, got[arg+"-mode"])
		}
	}
}

func TestAgentTaskRejectsAttachments(t *testing.T) {
	setupAgentTaskHelper(t, "")
	request := agentTaskRequest()
	request.Attachments = []string{"/uploads/source.zip"}
	err := (&AgentTask{}).Execute(context.Background(), request, agentTaskCallbacks())
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "attachment") {
		t.Fatalf("expected explicit unsupported attachment error, got %v", err)
	}
}

func TestAgentTaskIgnoresUnframedTargetJSONLogs(t *testing.T) {
	setupAgentTaskHelper(t, `{"type":"resultUpdate","content":`+agentTaskSafeResult+`}`)
	t.Setenv("AIG_AGENT_TASK_RAW_LOGS", "model response:\n{\n  \"sample\": 1\n}\n{\"type\":\"error\",\"content\":{\"msg\":\"quoted target response\"}}")
	if err := (&AgentTask{}).Execute(context.Background(), agentTaskRequest(), agentTaskCallbacks()); err != nil {
		t.Fatal(err)
	}
}

func TestAgentTaskPublishesOnlyAfterSuccessfulExit(t *testing.T) {
	resultEvent := `{"type":"resultUpdate","content":` + agentTaskSafeResult + `}`
	for _, tc := range []struct {
		name, events  string
		fail, success bool
	}{
		{"success", resultEvent, false, true},
		{"success_with_finding", `{"type":"resultUpdate","content":` + agentTaskVulnResult + `}`, false, true},
		{"duplicate", resultEvent + "\n" + resultEvent, false, false},
		{"no_result", "", false, false},
		{"late_error", resultEvent + "\n" + `{"type":"error","content":{"msg":"late failure"}}`, false, false},
		{"early_error", `{"type":"error","content":{"msg":"early failure"}}` + "\n" + resultEvent, false, false},
		{"exit_failure", resultEvent, true, false},
		{"invalid_result", `{"type":"resultUpdate","content":{}}`, false, false},
		{"null_result", `{"type":"resultUpdate","content":null}`, false, false},
		{"invalid_json", `{"type":"resultUpdate","content":`, false, false},
		{"contradictory_safe", `{"type":"resultUpdate","content":{"schema_version":"agent-security-report@1","risk_type":"safe","score":100,"total_tests":1,"vulnerable_tests":1,"results":[]}}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := setupAgentTaskHelper(t, tc.events)
			if tc.fail {
				t.Setenv("AIG_AGENT_TASK_FAIL", "1")
			}
			results := 0
			callbacks := agentTaskCallbacks()
			callbacks.ResultCallback = func(map[string]interface{}) {
				results++
				if _, err := os.Stat(capture + ".exited"); err != nil {
					t.Error("result published before successful subprocess exit")
				}
			}
			err := (&AgentTask{}).Execute(context.Background(), agentTaskRequest(), callbacks)
			if tc.success && (err != nil || results != 1) {
				t.Fatalf("expected one success; error=%v results=%d", err, results)
			}
			if !tc.success && (err == nil || results != 0) {
				t.Fatalf("expected failure without result; error=%v results=%d", err, results)
			}
		})
	}
}

func TestAgentTaskCancellationSuppressesBufferedResult(t *testing.T) {
	capture := setupAgentTaskHelper(t, `{"type":"resultUpdate","content":`+agentTaskSafeResult+`}`)
	t.Setenv("AIG_AGENT_TASK_WAIT", "1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			if _, err := os.Stat(capture); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	results := 0
	callbacks := agentTaskCallbacks()
	callbacks.ResultCallback = func(map[string]interface{}) { results++ }
	err := (&AgentTask{}).Execute(ctx, agentTaskRequest(), callbacks)
	if !errors.Is(err, context.Canceled) || results != 0 {
		t.Fatalf("expected cancellation without result; error=%v results=%d", err, results)
	}
}

func TestAgentTaskCancellationStopsDescendantProcess(t *testing.T) {
	capture := setupAgentTaskHelper(t, "")
	t.Setenv("AIG_AGENT_TASK_TREE", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			if _, err := os.Stat(capture + ".child-started"); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	err := (&AgentTask{}).Execute(ctx, agentTaskRequest(), agentTaskCallbacks())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if _, err := os.Stat(capture + ".child-started"); err != nil {
		t.Fatal("descendant did not start")
	}
	time.Sleep(2200 * time.Millisecond)
	if _, err := os.Stat(capture + ".child-survived"); !os.IsNotExist(err) {
		t.Fatal("descendant continued after cancellation")
	}
}

// TestAgentTaskFindingsAreNotBoundedByDialogueCount 同一目标回复可以证明多个漏洞。
func TestAgentTaskFindingsAreNotBoundedByDialogueCount(t *testing.T) {
	result := `{"schema_version":"agent-security-report@1","score":70,"risk_type":"high","total_tests":1,"vulnerable_tests":2,"results":[{"id":"one","title":"one","description":"first","level":"High"},{"id":"two","title":"two","description":"second","level":"High"}]}`
	if _, err := validateAgentTaskResult(json.RawMessage(result)); err != nil {
		t.Fatal(err)
	}
}

func TestAgentTaskOversizedLogStopsDescendant(t *testing.T) {
	capture := setupAgentTaskHelper(t, "")
	t.Setenv("AIG_AGENT_TASK_TREE", "1")
	t.Setenv("AIG_AGENT_TASK_OVERSIZED", "1")
	err := (&AgentTask{}).Execute(context.Background(), agentTaskRequest(), agentTaskCallbacks())
	if err == nil || !strings.Contains(err.Error(), "read agent scan output") {
		t.Fatalf("expected output limit failure, got %v", err)
	}
	if _, err := os.Stat(capture + ".child-started"); err != nil {
		t.Fatal("descendant did not start")
	}
	time.Sleep(2200 * time.Millisecond)
	if _, err := os.Stat(capture + ".child-survived"); !os.IsNotExist(err) {
		t.Fatal("descendant continued after oversized output failure")
	}
}
