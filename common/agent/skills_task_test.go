package agent

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/gologger"
	"github.com/gorilla/websocket"
)

const skillsValidResult = `{"type":"resultUpdate","content":{"readme":"检查完成","score":100,"language":"zh","start_time":1,"end_time":2,"results":[],"llm":"governed"}}`

func skillsRequest() TaskRequest {
	return TaskRequest{SessionId: "skills-session", Attachments: []string{"/uploads/skill.zip"}, Params: json.RawMessage(`{"model":{"model":"governed","token":"test-placeholder","base_url":"https://model.invalid/v1"}}`)}
}

func skillsCallbacks(results *[]map[string]interface{}) TaskCallbacks {
	return TaskCallbacks{
		ResultCallback:     func(result map[string]interface{}) { *results = append(*results, result) },
		PlanUpdateCallback: func([]SubTask) {}, NewPlanStepCallback: func(string, string) {},
		StepStatusUpdateCallback: func(string, string, string, string, string) {},
		ToolUsedCallback:         func(string, string, string, []Tool) {},
		ToolUseLogCallback:       func(string, string, string, string) {}, ErrorCallback: func(string) {},
	}
}

func skillsWriteZip(t *testing.T, destination string) {
	t.Helper()
	file, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("demo/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	_, err = entry.Write([]byte("---\nname: demo\ndescription: test\n---\n# demo"))
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsTaskRejectsInvalidInputBeforeDownload(t *testing.T) {
	for _, edit := range []func(*TaskRequest){
		func(r *TaskRequest) { r.Content = "https://github.com/demo/repo" },
		func(r *TaskRequest) { r.Attachments = nil },
		func(r *TaskRequest) { r.Attachments = []string{"a.zip", "b.zip"} },
		func(r *TaskRequest) { r.Attachments = []string{"a.tar.gz"} },
		func(r *TaskRequest) { r.Params = json.RawMessage(`{"model":{"model":"governed"}}`) },
	} {
		req := skillsRequest()
		edit(&req)
		task := SkillsTask{downloadFile: func(context.Context, string, string, string, string) error {
			t.Fatal("invalid request downloaded")
			return nil
		}}
		if err := task.Execute(context.Background(), req, TaskCallbacks{}); err == nil {
			t.Fatal("invalid input succeeded")
		}
	}
	if (&SkillsTask{}).GetName() != TaskTypeSkillsScan {
		t.Fatal("wrong capability")
	}
}

func TestSkillsTaskBuffersOnlyOneValidFinalResultAndCleansTemp(t *testing.T) {
	cases := []struct {
		name    string
		lines   []string
		wantErr bool
	}{
		{"valid", []string{skillsValidResult}, false},
		{"missing", nil, true},
		{"duplicate", []string{skillsValidResult, skillsValidResult}, true},
		{"error", []string{`{"type":"error","content":{"msg":"failed"}}`, skillsValidResult}, true},
		{"invalid-json", []string{`{"type":"resultUpdate","content":`}, true},
		{"bad-results", []string{strings.Replace(skillsValidResult, `"results":[]`, `"results":null`, 1)}, true},
		{"missing-score", []string{strings.Replace(skillsValidResult, `"score":100,`, ``, 1)}, true},
		{"wrong-score", []string{strings.Replace(skillsValidResult, `"score":100`, `"score":42`, 1)}, true},
		{"bad-finding", []string{strings.Replace(skillsValidResult, `"results":[]`, `"results":[{"level":"High"}]`, 1)}, true},
		{"malformed-after-valid", []string{skillsValidResult, `{"type":"resultUpdate","content":`}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var downloaded string
			var results []map[string]interface{}
			task := SkillsTask{
				downloadFile: func(ctx context.Context, server, session, uri, destination string) error {
					downloaded = destination
					skillsWriteZip(t, destination)
					return nil
				},
				runScanner: func(ctx context.Context, root string, model skillsModel, language string, consume func(string)) error {
					if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err != nil {
						t.Fatal(err)
					}
					if model.Model != "governed" || model.Token != "test-placeholder" {
						t.Fatal("model was not resolved")
					}
					for _, line := range tc.lines {
						consume(line)
						if len(results) != 0 {
							t.Fatal("result emitted before process success")
						}
					}
					return nil
				},
			}
			err := task.Execute(context.Background(), skillsRequest(), skillsCallbacks(&results))
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v, want error=%v", err, tc.wantErr)
			}
			if _, err := os.Stat(filepath.Dir(downloaded)); !os.IsNotExist(err) {
				t.Fatal("task temp directory was not removed")
			}
			if tc.wantErr && len(results) != 0 {
				t.Fatal("failed task emitted a result")
			}
			if !tc.wantErr && len(results) != 1 {
				t.Fatal("valid task did not emit exactly one result")
			}
		})
	}
}

func TestSkillsTaskCleanupOnDownloadFailureAndCancellation(t *testing.T) {
	for _, cancelScan := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		var tempFile string
		task := SkillsTask{downloadFile: func(ctx context.Context, server, session, uri, destination string) error {
			tempFile = destination
			if cancelScan {
				skillsWriteZip(t, destination)
				return nil
			}
			return errors.New("download failed")
		}, runScanner: func(ctx context.Context, root string, model skillsModel, language string, consume func(string)) error {
			cancel()
			<-ctx.Done()
			return ctx.Err()
		}}
		var results []map[string]interface{}
		if err := task.Execute(ctx, skillsRequest(), skillsCallbacks(&results)); err == nil {
			t.Fatal("failure succeeded")
		}
		cancel()
		if _, err := os.Stat(filepath.Dir(tempFile)); !os.IsNotExist(err) {
			t.Fatal("failure left temporary files")
		}
	}
}

func TestSkillsTaskDoesNotPublishWhenProcessFailsAfterResult(t *testing.T) {
	var results []map[string]interface{}
	task := SkillsTask{
		downloadFile: func(ctx context.Context, server, session, uri, destination string) error {
			skillsWriteZip(t, destination)
			return nil
		},
		runScanner: func(ctx context.Context, root string, model skillsModel, language string, consume func(string)) error {
			consume(skillsValidResult)
			return errors.New("process exit failure")
		},
	}
	if err := task.Execute(context.Background(), skillsRequest(), skillsCallbacks(&results)); err == nil {
		t.Fatal("process failure succeeded")
	}
	if len(results) != 0 {
		t.Fatal("failed process published a result")
	}
}

func TestSkillsDownloadCancelsInFlightRequest(t *testing.T) {
	t.Setenv("AIG_AGENT_TOKEN", "test-agent-token")
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- downloadSkillsArchive(ctx, strings.TrimPrefix(server.URL, "http://"), "session", "skill.zip", filepath.Join(t.TempDir(), "input.zip"))
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("download ignored cancellation")
	}
}

func TestSkillsDownloadHonorsContextAndSizeLimit(t *testing.T) {
	t.Setenv("AIG_AGENT_TOKEN", "test-agent-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Agent-Token") != "test-agent-token" {
			t.Error("missing internal authentication")
		}
		w.Header().Set("Content-Length", "20971521")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	dest := filepath.Join(t.TempDir(), "download.zip")
	if err := downloadSkillsArchive(context.Background(), strings.TrimPrefix(server.URL, "http://"), "session", "skill.zip", dest); err == nil {
		t.Fatal("oversize download accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := downloadSkillsArchive(ctx, strings.TrimPrefix(server.URL, "http://"), "session", "skill.zip", dest); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled, got %v", err)
	}
}

func TestSkillsCommandEnvironmentAndCancellation(t *testing.T) {
	t.Setenv("THINKING_API_KEY", "must-not-inherit")
	t.Setenv("CODING_MODEL", "must-not-inherit")
	env := skillsCommandEnv(skillsModel{Model: "governed", Token: "test-placeholder", BaseURL: "https://model.invalid/v1"})
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "must-not-inherit") {
		t.Fatal("inherited specialized model environment")
	}
	if !strings.Contains(joined, "AIG_SKILLS_MODEL=governed") || !strings.Contains(joined, "AIG_SKILLS_TOKEN=test-placeholder") {
		t.Fatal("missing governed model")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := runSkillsCommand(ctx, t.TempDir(), os.Args[0], []string{"-test.run=^TestSkillsProcessHelper$"}, append(env, "AIG_SKILLS_TEST_HELPER=1"), func(string) {})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel not propagated: %v", err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("child process did not exit on cancellation")
	}
}

func TestSkillsProcessHelper(t *testing.T) {
	if os.Getenv("AIG_SKILLS_TEST_HELPER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

func TestSkillsTaskPropagatesSelectedLanguage(t *testing.T) {
	for _, language := range []string{"", "zh_CN", "zh", "en"} {
		req := skillsRequest()
		req.Language = language
		expected := "zh"
		if language == "en" {
			expected = "en"
		}
		var results []map[string]interface{}
		task := SkillsTask{
			downloadFile: func(ctx context.Context, server, session, uri, destination string) error {
				skillsWriteZip(t, destination)
				return nil
			},
			runScanner: func(ctx context.Context, root string, model skillsModel, actualLanguage string, consume func(string)) error {
				if actualLanguage != expected {
					t.Fatalf("language=%q, want %q", actualLanguage, expected)
				}
				consume(strings.Replace(skillsValidResult, `"language":"zh"`, `"language":"`+expected+`"`, 1))
				return nil
			},
		}
		if err := task.Execute(context.Background(), req, skillsCallbacks(&results)); err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0]["language"] != expected {
			t.Fatal("result language was not propagated")
		}
	}
}

func TestSkillsAgentReceiveFrameNeverLogsGovernedToken(t *testing.T) {
	const governedToken = "fixture-model-token-must-never-appear-in-agent-log"
	req := skillsRequest()
	req.TaskType = TaskTypeSkillsScan
	req.Params = json.RawMessage(`{"model":{"model":"governed","token":"` + governedToken + `","base_url":"https://model.invalid/v1"}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if err := conn.WriteJSON(RequestData{Type: ServerMsgTypeTaskAssign, Content: req}); err != nil {
			t.Error(err)
			return
		}
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	}))
	defer server.Close()
	var output bytes.Buffer
	logger := gologger.StdLogger.Logrus()
	previousOutput, previousLevel := logger.Out, logger.GetLevel()
	logger.SetOutput(&output)
	logger.SetLevel(gologger.TraceLevel)
	defer func() { logger.SetOutput(previousOutput); logger.SetLevel(previousLevel) }()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	agent := NewAgent(AgentConfig{})
	agent.conn = conn
	defer agent.cancel()
	agent.handleReceive()
	if len(agent.Tasks) != 1 {
		t.Fatal("task frame was not received")
	}
	if strings.Contains(output.String(), governedToken) {
		t.Fatal("received task frame leaked governed token into Agent logs")
	}
	if !strings.Contains(output.String(), "task_assign") || !strings.Contains(output.String(), req.SessionId) {
		t.Fatal("missing safe task metadata in receive log")
	}
}
