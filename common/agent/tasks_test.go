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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/common/runner"
	"github.com/Juneoww/AIG_Custom/common/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAIInfraScanAgentPrepareTargetsExpandsBodyAndAttachmentRangesBeforePortDiscovery(t *testing.T) {
	var discovered []string
	var downloadDirectory string
	agent := &AIInfraScanAgent{
		downloadFile: func(_, _, _, destination string, maxBytes int64) error {
			assert.Equal(t, maxTargetListAttachmentBytes, maxBytes)
			downloadDirectory = filepath.Dir(destination)
			return os.WriteFile(destination, []byte("192.168.10.3-192.168.10.4\n"), 0600)
		},
		nmapScan: func(host, _ string) (*utils.NmapRun, error) {
			discovered = append(discovered, host)
			return &utils.NmapRun{}, nil
		},
	}
	request := TaskRequest{
		SessionId: "target-expression-test",
		Content:   "192.168.10.2-192.168.10.3\n192.168.10.5",
		Attachments: []string{
			"targets.txt",
		},
	}

	targets, err := agent.prepareTargets(request, ScanRequest{}, initTexts("zh"))
	require.NoError(t, err)
	assert.Equal(t, []string{"192.168.10.2", "192.168.10.3", "192.168.10.5", "192.168.10.4"}, targets)
	assert.NoDirExists(t, downloadDirectory)

	callbacks := TaskCallbacks{
		StepStatusUpdateCallback: func(string, string, string, string, string) {},
		ToolUsedCallback:         func(string, string, string, []Tool) {},
	}
	finalTargets, err := agent.scanPortsAndPrepareTargets(targets, "step-1", initTexts("zh"), callbacks)
	require.NoError(t, err)
	assert.Equal(t, targets, finalTargets)
	assert.Equal(t, targets, discovered)
}

func TestAIInfraScanAgentPrepareTargetsRejectsInvalidWildcard(t *testing.T) {
	agent := &AIInfraScanAgent{}
	_, err := agent.prepareTargets(TaskRequest{Content: "22.*.10.*"}, ScanRequest{}, initTexts("zh"))
	assert.Error(t, err)
}

func TestAIInfraScanAgentPortDiscoveryCanAppendToMaximumExpandedInput(t *testing.T) {
	targets, err := runner.ParseTargets([]string{"22.2.*.*"})
	require.NoError(t, err)
	agent := &AIInfraScanAgent{
		nmapScan: func(host, _ string) (*utils.NmapRun, error) {
			if host != "22.2.0.0" {
				return &utils.NmapRun{}, nil
			}
			return &utils.NmapRun{Hosts: []utils.Host{{
				Address: utils.Address{Addr: host},
				Ports: utils.Ports{PortList: []utils.Port{{
					PortID: 11434, State: utils.State{State: "open"},
				}}},
			}}}, nil
		},
	}
	callbacks := TaskCallbacks{
		StepStatusUpdateCallback: func(string, string, string, string, string) {},
		ToolUsedCallback:         func(string, string, string, []Tool) {},
		ToolUseLogCallback:       func(string, string, string, string) {},
	}

	finalTargets, err := agent.scanPortsAndPrepareTargets(targets, "step-1", initTexts("zh"), callbacks)
	require.NoError(t, err)
	assert.Len(t, finalTargets, len(targets)+1)
	assert.Equal(t, "22.2.0.0:11434", finalTargets[len(finalTargets)-1])
}

func TestAIInfraScanAgentPrepareTargetsRejectsUnsafeAttachmentAndCleansUp(t *testing.T) {
	tests := []struct {
		name     string
		contents string
	}{
		{name: "oversized", contents: strings.Repeat("x", (1<<20)+1)},
		{name: "non utf8", contents: string([]byte{0xff, 0xfe})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var downloadDirectory string
			agent := &AIInfraScanAgent{
				downloadFile: func(_, _, _, destination string, maxBytes int64) error {
					assert.Equal(t, maxTargetListAttachmentBytes, maxBytes)
					downloadDirectory = filepath.Dir(destination)
					return os.WriteFile(destination, []byte(test.contents), 0600)
				},
			}

			_, err := agent.prepareTargets(TaskRequest{
				SessionId: "unsafe-target-list", Content: "192.168.10.1", Attachments: []string{"targets.txt"},
			}, ScanRequest{}, initTexts("zh"))
			require.Error(t, err)
			assert.NoDirExists(t, downloadDirectory)
		})
	}
}
