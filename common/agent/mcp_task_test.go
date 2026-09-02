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

package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMcpExecutionPlanUsesExplicitSourceKind(t *testing.T) {
	tests := []struct {
		name        string
		params      string
		content     string
		attachments []string
		transport   string
		titles      []string
	}{
		{
			name:        "repository attachment",
			params:      `{"source_kind":"repository"}`,
			attachments: []string{"mcp-server.zip"},
			transport:   "code",
			titles:      []string{"Info Collection", "Code Audit", "Vulnerability Review"},
		},
		{
			name:      "repository Git URL",
			params:    `{"source_kind":"repository"}`,
			content:   "https://git.example.test/platform/mcp-server.git",
			transport: "code",
			titles:    []string{"Info Collection", "Code Audit", "Vulnerability Review"},
		},
		{
			name:      "service URL",
			params:    `{"source_kind":"service"}`,
			content:   "https://mcp.example.test/rpc",
			transport: "url",
			titles:    []string{"Info Collection", "Malicious Testing", "Vulnerability Testing", "Vulnerability Review"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := planMcpExecution(json.RawMessage(test.params), test.content, test.attachments)

			require.NoError(t, err)
			assert.Equal(t, test.transport, plan.transport)
			assert.Equal(t, test.titles, plan.taskTitles)
		})
	}
}

func TestMcpExecutionPlanRejectsInconsistentExplicitSources(t *testing.T) {
	tests := []struct {
		name        string
		params      string
		content     string
		attachments []string
	}{
		{
			name:        "repository attachment and content",
			params:      `{"source_kind":"repository"}`,
			content:     "https://git.example.test/platform/mcp-server.git",
			attachments: []string{"mcp-server.zip"},
		},
		{
			name:        "service attachment",
			params:      `{"source_kind":"service"}`,
			content:     "https://mcp.example.test/rpc",
			attachments: []string{"mcp-server.zip"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := planMcpExecution(json.RawMessage(test.params), test.content, test.attachments)

			require.Error(t, err)
		})
	}
}

func TestMcpExecutionPlanRejectsUnknownExplicitSourceKind(t *testing.T) {
	_, err := planMcpExecution(json.RawMessage(`{"source_kind":"filesystem"}`), "https://mcp.example.test/rpc", nil)

	require.Error(t, err)
}

func TestMcpExecutionPlanPreservesLegacyHeuristicWithoutSourceKind(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		attachments []string
		transport   string
		titles      []string
	}{
		{
			name:      "GitHub repository content uses code flow",
			content:   "https://github.com/example/mcp-server.git",
			transport: "code",
			titles:    []string{"Info Collection", "Code Audit", "Vulnerability Review"},
		},
		{
			name:        "attachment uses code flow",
			content:     "legacy scan request",
			attachments: []string{"mcp-server.zip"},
			transport:   "code",
			titles:      []string{"Info Collection", "Code Audit", "Vulnerability Review"},
		},
		{
			name:      "service URL uses service flow",
			content:   "https://mcp.example.test/rpc",
			transport: "url",
			titles:    []string{"Info Collection", "Malicious Testing", "Vulnerability Testing", "Vulnerability Review"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := planMcpExecution(json.RawMessage(`{}`), test.content, test.attachments)

			require.NoError(t, err)
			assert.Equal(t, test.transport, plan.transport)
			assert.Equal(t, test.titles, plan.taskTitles)
		})
	}
}
