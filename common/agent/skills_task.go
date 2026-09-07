// Copyright (c) 2024-2026 Tencent Zhuque Lab. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// Requirement: Any integration or derivative work must explicitly attribute
// Tencent Zhuque Lab (https://github.com/Tencent/AI-Infra-Guard) in its
// documentation or user interface, as detailed in the NOTICE file.

package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Juneoww/AIG_Custom/common/utils"
	"github.com/Juneoww/AIG_Custom/internal/skillarchive"
)

// SkillsTask 下载并校验唯一 Skill ZIP，在临时目录内启动只读静态扫描。
type SkillsTask struct {
	Server       string
	downloadFile func(context.Context, string, string, string, string) error
	runScanner   func(context.Context, string, skillsModel, string, func(string)) error
}

type skillsModel struct {
	Model   string `json:"model"`
	Token   string `json:"token"`
	BaseURL string `json:"base_url"`
}

func (m *SkillsTask) GetName() string { return TaskTypeSkillsScan }

func (m *SkillsTask) Execute(ctx context.Context, request TaskRequest, callbacks TaskCallbacks) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.Content != "" || len(request.Attachments) != 1 {
		return errors.New("Skills 扫描只接受一个 ZIP 附件且 content 必须为空")
	}
	language := strings.ToLower(strings.TrimSpace(request.Language))
	switch language {
	case "", "zh", "zh_cn", "zh-cn":
		language = "zh"
	case "en":
	default:
		return errors.New("Skills 扫描语言无效")
	}
	attachment, err := url.Parse(request.Attachments[0])
	if err != nil || !strings.EqualFold(filepath.Ext(attachment.Path), ".zip") {
		return errors.New("Skills 扫描附件必须为 ZIP")
	}
	var params struct {
		Model skillsModel `json:"model"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return errors.New("Skills 模型配置无效")
	}
	if strings.TrimSpace(params.Model.Model) == "" || strings.TrimSpace(params.Model.Token) == "" || strings.TrimSpace(params.Model.BaseURL) == "" {
		return errors.New("Skills 扫描需要完整的受治理模型配置")
	}
	endpoint, err := url.Parse(params.Model.BaseURL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil {
		return errors.New("Skills 模型服务地址无效")
	}
	for _, field := range []string{params.Model.Model, params.Model.Token, params.Model.BaseURL} {
		if strings.ContainsRune(field, '\x00') {
			return errors.New("Skills 模型配置无效")
		}
	}
	tempDir, err := os.MkdirTemp("", "aig-skills-")
	if err != nil {
		return fmt.Errorf("创建 Skills 临时目录失败: %w", err)
	}
	defer os.RemoveAll(tempDir)
	download := m.downloadFile
	if download == nil {
		download = downloadSkillsArchive
	}
	archive := filepath.Join(tempDir, "skill.zip")
	if err := download(ctx, m.Server, request.SessionId, request.Attachments[0], archive); err != nil {
		return fmt.Errorf("下载 Skills 附件失败: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := skillarchive.Extract(archive, filepath.Join(tempDir, "source"))
	if err != nil {
		return errors.New("Skills ZIP 校验失败")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tasks := make([]SubTask, 0, 3)
	for i, title := range []string{"Skill 信息收集", "Skill 静态审计", "漏洞复核"} {
		tasks = append(tasks, CreateSubTask(SubTaskStatusTodo, title, 0, strconv.Itoa(i+1)))
	}
	if callbacks.PlanUpdateCallback != nil {
		callbacks.PlanUpdateCallback(tasks)
	}
	config := CmdConfig{}
	var finalResult map[string]interface{}
	var eventErr error
	resultCount := 0
	consume := func(line string) {
		// 不转发子进程自由文本；只接收协议事件，并对模型凭据做最后一道脱敏。
		line = strings.ReplaceAll(strings.TrimSpace(line), params.Model.Token, "[REDACTED]")
		if !strings.HasPrefix(line, "{") {
			return
		}
		var event CmdContent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			eventErr = errors.New("Skills 扫描返回损坏的事件")
			return
		}
		switch event.Type {
		case AgentMsgTypeResultUpdate:
			resultCount++
			if resultCount != 1 {
				eventErr = errors.New("Skills 扫描返回重复结果")
				return
			}
			finalResult, err = validateSkillsResult(event.Content, params.Model.Model, language)
			if err != nil {
				eventErr = err
			}
		case AgentMsgTypeError:
			eventErr = errors.New("Skills 扫描器报告执行失败")
		case AgentMsgTypeNewPlanStep, AgentMsgTypeStatusUpdate, AgentMsgTypeToolUsed, AgentMsgTypeActionLog:
			ParseStdoutLine(m.Server, request.SessionId, root, tasks, line, callbacks, &config, false)
		}
	}
	run := m.runScanner
	if run == nil {
		run = runSkillsScanner
	}
	if err := run(ctx, root, params.Model, language, consume); err != nil {
		return fmt.Errorf("Skills 扫描失败: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if eventErr != nil {
		return eventErr
	}
	if resultCount != 1 || finalResult == nil {
		return errors.New("Skills 扫描缺少有效最终结果")
	}
	for i := range tasks {
		tasks[i].Status = SubTaskStatusDone
	}
	if callbacks.PlanUpdateCallback != nil {
		callbacks.PlanUpdateCallback(tasks)
	}
	if callbacks.ResultCallback != nil {
		callbacks.ResultCallback(finalResult)
	}
	return nil
}

// downloadSkillsArchive 绑定任务上下文并在读取时限制压缩包大小。
func downloadSkillsArchive(ctx context.Context, server, session, uri, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	token := strings.TrimSpace(os.Getenv("AIG_AGENT_TOKEN"))
	if token == "" {
		return errors.New("AIG_AGENT_TOKEN is required")
	}
	data, _ := json.Marshal(map[string]string{"fileUrl": uri})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://%s/api/v1/app/tasks/%s/downloadFile", server, url.PathEscape(session)), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Agent-Token", token)
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("附件下载状态码 %d", resp.StatusCode)
	}
	if resp.ContentLength > skillarchive.MaxArchiveBytes {
		return errors.New("Skills ZIP 超过大小限制")
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, skillarchive.MaxArchiveBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > skillarchive.MaxArchiveBytes {
		return errors.New("Skills ZIP 超过大小限制")
	}
	return os.WriteFile(destination, data, 0600)
}

// validateSkillsResult 不允许缺字段、未知级别或评分不一致的结果进入平台。
func validateSkillsResult(raw json.RawMessage, model, language string) (map[string]interface{}, error) {
	invalid := errors.New("Skills 扫描最终结果无效")
	if len(raw) > 1<<20 {
		return nil, invalid
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil || result == nil {
		return nil, invalid
	}
	score, ok := result["score"].(float64)
	if !ok || score < 0 || score > 100 || score != float64(int(score)) {
		return nil, invalid
	}
	findings, ok := result["results"].([]interface{})
	if !ok {
		return nil, invalid
	}
	for _, key := range []string{"readme", "language", "llm"} {
		value, ok := result[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, invalid
		}
	}
	if result["llm"] != model || result["language"] != language {
		return nil, invalid
	}
	start, okStart := result["start_time"].(float64)
	end, okEnd := result["end_time"].(float64)
	if !okStart || !okEnd || start <= 0 || end < start {
		return nil, invalid
	}
	expectedScore := 100
	for _, value := range findings {
		finding, ok := value.(map[string]interface{})
		if !ok {
			return nil, invalid
		}
		for _, key := range []string{"title", "description", "risk_type", "level", "suggestion"} {
			text, ok := finding[key].(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil, invalid
			}
		}
		switch finding["level"] {
		case "Critical":
			expectedScore -= 100
		case "High":
			expectedScore -= 40
		case "Medium":
			expectedScore -= 25
		case "Low":
			expectedScore -= 10
		default:
			return nil, invalid
		}
	}
	if expectedScore < 0 {
		expectedScore = 0
	}
	if score != float64(expectedScore) {
		return nil, invalid
	}
	return result, nil
}

func skillsCommandEnv(model skillsModel) []string {
	var env []string
	for _, entry := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
		blocked := key == "AIG_AGENT_TOKEN" || key == "PYTHONPATH" || key == "PYTHONSTARTUP"
		for _, prefix := range []string{"THINKING_", "CODING_", "FAST_", "DEFAULT_", "OPENAI_", "OPENROUTER_", "ANTHROPIC_", "LAMINAR_", "AIG_SKILLS_", "AIG_SCAN_MODE"} {
			blocked = blocked || strings.HasPrefix(key, prefix)
		}
		if !blocked {
			env = append(env, entry)
		}
	}
	return append(env, "AIG_SCAN_MODE=skills", "AIG_SKILLS_MODEL="+model.Model, "AIG_SKILLS_TOKEN="+model.Token, "AIG_SKILLS_BASE_URL="+model.BaseURL, "PYTHONDONTWRITEBYTECODE=1", "PYTHONUNBUFFERED=1")
}

func runSkillsScanner(ctx context.Context, root string, model skillsModel, language string, consume func(string)) error {
	dir, err := utils.ResolveMcpScanDir()
	if err != nil {
		return err
	}
	// 直接启动解释器，避免 uv 包装进程在取消时遗留实际 Python 子进程。
	python := strings.TrimSpace(os.Getenv("AIG_SKILLS_PYTHON_BIN"))
	if python == "" {
		for _, candidate := range []string{filepath.Join(dir, ".venv", "bin", "python"), filepath.Join(dir, ".venv", "Scripts", "python.exe"), "python3", "python"} {
			if found, lookupErr := exec.LookPath(candidate); lookupErr == nil {
				python = found
				break
			}
		}
	}
	if python == "" {
		return errors.New("找不到 Skills Python 解释器")
	}
	return runSkillsCommand(ctx, dir, python, []string{"-B", "main.py", "--mode", "skills", "--repo", root, "--language", language}, skillsCommandEnv(model), consume)
}

func runSkillsCommand(ctx context.Context, dir, executable string, args, env []string, consume func(string)) error {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir, cmd.Env = dir, env
	cmd.WaitDelay = time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	var total int
	var outputErr error
	for scanner.Scan() {
		line := scanner.Text()
		total += len(line)
		if total > 32<<20 {
			outputErr = errors.New("Skills 扫描输出超过限制")
			break
		}
		consume(line)
	}
	if scanner.Err() != nil {
		outputErr = errors.New("Skills 扫描输出无法读取")
	}
	if outputErr != nil {
		_ = cmd.Process.Kill()
	}
	processErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if outputErr != nil {
		return outputErr
	}
	return processErr
}
