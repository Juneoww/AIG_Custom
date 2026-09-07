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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/Juneoww/AIG_Custom/common/utils"
)

type McpTask struct {
	Server string
}

type mcpExecutionPlan struct {
	transport  string
	taskTitles []string
}

func planMcpExecution(rawParams json.RawMessage, content string, attachments []string) (mcpExecutionPlan, error) {
	var fields map[string]json.RawMessage
	if len(rawParams) > 0 {
		if err := json.Unmarshal(rawParams, &fields); err != nil {
			return mcpExecutionPlan{}, errors.New("invalid MCP task parameters")
		}
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}

	rawSourceKind, explicitSourceKind := fields["source_kind"]
	if !explicitSourceKind {
		if len(attachments) > 0 || strings.Contains(content, "github.com") {
			return mcpCodeExecutionPlan(), nil
		}
		return mcpServiceExecutionPlan(), nil
	}

	var sourceKind string
	if err := json.Unmarshal(rawSourceKind, &sourceKind); err != nil || sourceKind == "" {
		return mcpExecutionPlan{}, errors.New("invalid MCP source kind")
	}
	switch sourceKind {
	case "repository":
		if len(attachments) > 0 {
			if content != "" {
				return mcpExecutionPlan{}, errors.New("MCP repository source cannot include both content and attachments")
			}
			return mcpCodeExecutionPlan(), nil
		}
		if !validMcpRepositoryReference(content) {
			return mcpExecutionPlan{}, errors.New("MCP repository source requires a Git repository reference")
		}
		return mcpCodeExecutionPlan(), nil
	case "service":
		if len(attachments) > 0 || !validMcpServiceEndpoint(content) {
			return mcpExecutionPlan{}, errors.New("MCP service source requires a service endpoint without attachments")
		}
		rawAuthorization, authorizationProvided := fields["authorization_confirmed"]
		var authorizationConfirmed bool
		if !authorizationProvided || json.Unmarshal(rawAuthorization, &authorizationConfirmed) != nil || !authorizationConfirmed {
			return mcpExecutionPlan{}, errors.New("MCP service source requires explicit authorization")
		}
		return mcpServiceExecutionPlan(), nil
	default:
		return mcpExecutionPlan{}, errors.New("unknown MCP source kind")
	}
}

func mcpCodeExecutionPlan() mcpExecutionPlan {
	return mcpExecutionPlan{
		transport: "code",
		taskTitles: []string{
			"Info Collection",
			"Code Audit",
			"Vulnerability Review",
		},
	}
}

func mcpServiceExecutionPlan() mcpExecutionPlan {
	return mcpExecutionPlan{
		transport: "url",
		taskTitles: []string{
			"Info Collection",
			"Malicious Testing",
			"Vulnerability Testing",
			"Vulnerability Review",
		},
	}
}

func validMcpRepositoryReference(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, "?#") {
		return false
	}
	if parsed, err := url.ParseRequestURI(value); err == nil && parsed.Hostname() != "" &&
		strings.Trim(parsed.Path, "/") != "" && parsed.RawQuery == "" && parsed.Fragment == "" {
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https":
			return parsed.User == nil
		case "ssh":
			if parsed.User == nil || parsed.User.Username() != "git" {
				return false
			}
			_, hasPassword := parsed.User.Password()
			return !hasPassword
		}
	}
	return validMcpRepositorySCPReference(value)
}

func validMcpRepositorySCPReference(value string) bool {
	if !strings.HasPrefix(value, "git@") || strings.ContainsAny(value, " \t\r\n?#") {
		return false
	}
	hostAndPath := strings.TrimPrefix(value, "git@")
	separator := strings.IndexByte(hostAndPath, ':')
	if separator <= 0 || separator == len(hostAndPath)-1 {
		return false
	}
	host, path := hostAndPath[:separator], hostAndPath[separator+1:]
	return !strings.ContainsAny(host, "/@") && strings.Trim(path, "/") != ""
}

func validMcpServiceEndpoint(value string) bool {
	if value == "" || value != strings.TrimSpace(value) {
		return false
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(value, "#") {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	return scheme == "http" || scheme == "https"
}

func (m *McpTask) GetName() string {
	return TaskTypeMcpScan
}

func (m *McpTask) Execute(ctx context.Context, request TaskRequest, callbacks TaskCallbacks) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	params, err := parseMCPRuntime(m.Server, request)
	if err != nil {
		return err
	}
	language := strings.ToLower(strings.TrimSpace(request.Language))
	if language == "" || language == "zh_cn" || language == "zh-cn" {
		language = "zh"
	}
	if language != "zh" && language != "en" {
		return errMCPRuntime
	}
	argv := []string{"run", "--no-project", "main.py", "--runtime-config-stdin", "--language", language}
	plan := mcpServiceExecutionPlan()
	if params.SourceKind == "repository" {
		folder, err := os.MkdirTemp("", "aig-mcp-source-")
		if err != nil {
			return errMCPArchive
		}
		defer os.RemoveAll(folder)
		if err = downloadMCPArchive(ctx, m.Server, request.SessionId, params.ArchiveRef, folder); err != nil {
			return err
		}
		argv = append(argv, "--repo", folder)
		plan = mcpCodeExecutionPlan()
	}
	if params.Model == nil {
		plan.taskTitles = []string{"基础检查（未使用模型）"}
	}
	private, err := json.Marshal(params.mcpPrivateConfig)
	if err != nil {
		return errMCPRuntime
	}
	mcpDir, err := utils.ResolveMcpScanDir()
	if err != nil {
		return errors.New("MCP scanner runtime unavailable")
	}
	uvBin, err := utils.ResolveUvBin()
	if err != nil {
		return errors.New("MCP scanner runtime unavailable")
	}
	var tasks []SubTask
	for i, title := range plan.taskTitles {
		tasks = append(tasks, CreateSubTask(SubTaskStatusTodo, title, 0, strconv.Itoa(i+1)))
	}
	if callbacks.PlanUpdateCallback != nil {
		callbacks.PlanUpdateCallback(tasks)
	}
	config := CmdConfig{}
	return utils.RunCmdWithContextInput(ctx, mcpDir, uvBin, argv, bytes.NewReader(private), []string{"AIG_SERVER=" + m.Server, "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8"}, mcpRuntimeRedactor(params), func(line string) {
		ParseStdoutLine(m.Server, request.SessionId, mcpDir, tasks, line, callbacks, &config, false)
	})
}
