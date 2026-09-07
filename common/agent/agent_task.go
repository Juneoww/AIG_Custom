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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Juneoww/AIG_Custom/common/utils"
	"github.com/google/uuid"
)

type AgentTask struct {
	Server string
}

func (m *AgentTask) GetName() string {
	return TaskTypeAgentScan
}

func (m *AgentTask) Execute(ctx context.Context, request TaskRequest, callbacks TaskCallbacks) error {
	if len(request.Attachments) != 0 {
		return errors.New("agent scan attachments are not supported; provide workflow instructions in content")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	type EvalModel struct {
		Model         string `json:"model"`
		ApiKey        string `json:"token"`
		BaseUrl       string `json:"base_url"`
		MaxConcurrent int    `json:"limit"`
	}

	type AgentScanParams struct {
		AgentData string    `json:"agent_data"` // yaml content from dispatchTask
		EvalModel EvalModel `json:"eval_model"`
	}

	var params AgentScanParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return err
	}

	// Validate required fields
	if params.AgentData == "" {
		return errors.New("agent_data is required")
	}
	if params.EvalModel.Model == "" {
		return errors.New("eval_model.model is required")
	}
	if params.EvalModel.ApiKey == "" {
		return errors.New("eval_model.token is required")
	}
	if params.EvalModel.BaseUrl == "" {
		return errors.New("eval_model.base_url is required")
	}

	// Set default max_concurrent
	if params.EvalModel.MaxConcurrent == 0 {
		params.EvalModel.MaxConcurrent = 10
	}

	// 配置与任务说明只写入当前用户可读的临时文件，避免进入命令行和执行日志。
	providerPath, err := writeAgentTaskFile("agent_provider_*.yaml", params.AgentData)
	if err != nil {
		return fmt.Errorf("write agent config: %w", err)
	}
	defer os.Remove(providerPath)
	promptPath, err := writeAgentTaskFile("agent_prompt_*.txt", request.Content)
	if err != nil {
		return fmt.Errorf("write agent prompt: %w", err)
	}
	defer os.Remove(promptPath)

	// Get language
	language := request.Language
	if language == "" {
		language = "zh"
	}

	// Build command arguments
	var argv []string
	argv = append(argv, "run", "--no-project", "main.py")
	argv = append(argv, "-m", params.EvalModel.Model)
	argv = append(argv, "-u", params.EvalModel.BaseUrl)
	argv = append(argv, "--agent_provider", providerPath)
	argv = append(argv, "--prompt-file", promptPath, "--governed-model")
	argv = append(argv, "--language", language)

	// Define task titles
	taskTitles := []string{
		"Info Collection",
		"Vulnerability Detection",
		"Vulnerability Review",
	}

	var tasks []SubTask
	for i, title := range taskTitles {
		tasks = append(tasks, CreateSubTask(SubTaskStatusTodo, title, 0, strconv.Itoa(i+1)))
	}
	callbacks.PlanUpdateCallback(tasks)
	config := CmdConfig{StatusId: ""}
	agentScanDir, err := utils.ResolveAgentScanDir()
	if err != nil {
		return fmt.Errorf("resolve agent-scan directory: %v", err)
	}
	uvBin, err := utils.ResolveUvBin()
	if err != nil {
		return fmt.Errorf("resolve uv binary: %v", err)
	}
	var result map[string]interface{}
	var terminalErr error
	err = runAgentTaskCommand(ctx, agentScanDir, uvBin, argv, params.EvalModel.ApiKey, func(line string) {
		// 只转发结构化事件；普通子进程日志可能包含完整提示词或目标配置。
		var event CmdContent
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			return
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			terminalErr = errors.New("agent scan emitted invalid event JSON")
			return
		}
		switch event.Type {
		case AgentMsgTypeResultUpdate:
			if result != nil {
				terminalErr = errors.New("agent scan emitted more than one result")
				return
			}
			parsed, err := validateAgentTaskResult(event.Content)
			if err != nil {
				terminalErr = err
				return
			}
			result = parsed
		case AgentMsgTypeError:
			terminalErr = errors.New("agent scan reported an execution error")
			if callbacks.ErrorCallback != nil {
				callbacks.ErrorCallback(terminalErr.Error())
			}
		case AgentMsgTypeNewPlanStep, AgentMsgTypeStatusUpdate, AgentMsgTypeToolUsed, AgentMsgTypeActionLog:
			line = strings.ReplaceAll(line, params.EvalModel.ApiKey, "[REDACTED]")
			ParseStdoutLine(m.Server, request.SessionId, agentScanDir, tasks, line, callbacks, &config, false)
		}
	})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if terminalErr != nil {
		return terminalErr
	}
	if result == nil {
		return errors.New("agent scan exited without a valid result")
	}
	for i := range tasks {
		tasks[i].Status = SubTaskStatusDone
	}
	callbacks.PlanUpdateCallback(tasks)
	callbacks.ResultCallback(result)
	return nil
}

func writeAgentTaskFile(pattern, content string) (string, error) {
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	name := file.Name()
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		os.Remove(name)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// runAgentTaskCommand 不记录 argv 或环境；读取结束后才等待并判定子进程退出状态。
func runAgentTaskCommand(ctx context.Context, dir, binary string, argv []string, apiKey string, onLine func(string)) error {
	cmd := exec.CommandContext(ctx, binary, argv...)
	utils.ConfigureAgentCommandCancellation(cmd)
	cmd.Dir = dir
	// 每个子进程使用独立事件帧，普通模型/目标日志即使是 JSON 也不能冒充执行事件。
	eventPrefix := "AIG_SCAN_EVENT:" + uuid.NewString() + ":"
	cmd.Env = append(os.Environ(), "OPENROUTER_API_KEY="+apiKey, "AIG_SCAN_EVENT_PREFIX="+eventPrefix)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start agent scan: %w", err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, eventPrefix) {
			onLine(strings.TrimPrefix(line, eventPrefix))
		}
	}
	readErr := scanner.Err()
	if readErr != nil {
		_ = cmd.Cancel()
	}
	waitErr := cmd.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if readErr != nil {
		return fmt.Errorf("read agent scan output: %w", readErr)
	}
	if waitErr != nil {
		return fmt.Errorf("agent scan process failed: %w", waitErr)
	}
	return nil
}

func validateAgentTaskResult(raw json.RawMessage) (map[string]interface{}, error) {
	invalid := errors.New("agent scan emitted an invalid agent-security-report@1 result")
	var report struct {
		SchemaVersion string            `json:"schema_version"`
		RiskType      string            `json:"risk_type"`
		Score         *int              `json:"score"`
		TotalTests    *int              `json:"total_tests"`
		Vulnerable    *int              `json:"vulnerable_tests"`
		Results       []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, invalid
	}
	if report.SchemaVersion != "agent-security-report@1" || report.Score == nil || *report.Score < 0 || *report.Score > 100 ||
		report.TotalTests == nil || report.Vulnerable == nil || report.Results == nil ||
		*report.Vulnerable != len(report.Results) || *report.TotalTests <= 0 {
		return nil, invalid
	}
	if len(report.Results) == 0 {
		if report.RiskType != "safe" {
			return nil, invalid
		}
	} else if report.RiskType != "low" && report.RiskType != "medium" && report.RiskType != "high" {
		return nil, invalid
	}
	for _, rawFinding := range report.Results {
		var finding struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Level       string `json:"level"`
		}
		if json.Unmarshal(rawFinding, &finding) != nil || strings.TrimSpace(finding.ID) == "" ||
			strings.TrimSpace(finding.Title) == "" || strings.TrimSpace(finding.Description) == "" ||
			(finding.Level != "High" && finding.Level != "Medium" && finding.Level != "Low") {
			return nil, invalid
		}
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, invalid
	}
	return result, nil
}
