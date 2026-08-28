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
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/common/portscan"
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
	finalTargets, err := agent.scanPortsAndPrepareTargets(targets, portscan.DefaultMode, "step-1", initTexts("zh"), callbacks)
	require.NoError(t, err)
	assert.Equal(t, targets, finalTargets)
	assert.Equal(t, targets, discovered)
}

func TestAIInfraScanAgentPrepareTargetsRejectsInvalidWildcard(t *testing.T) {
	agent := &AIInfraScanAgent{}
	_, err := agent.prepareTargets(TaskRequest{Content: "22.*.10.*"}, ScanRequest{}, initTexts("zh"))
	assert.Error(t, err)
}

func TestAIInfraScanAgentPrepareTargetsRejectsTooManyAttachmentExpressions(t *testing.T) {
	agent := &AIInfraScanAgent{
		downloadFile: func(_, _, _, destination string, _ int64) error {
			return os.WriteFile(destination, []byte(strings.Repeat("example.com\n", 65537)), 0600)
		},
	}

	_, err := agent.prepareTargets(TaskRequest{
		SessionId: "too-many-target-expressions", Content: "192.168.10.1", Attachments: []string{"targets.txt"},
	}, ScanRequest{}, initTexts("zh"))
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

	finalTargets, err := agent.scanPortsAndPrepareTargets(targets, portscan.DefaultMode, "step-1", initTexts("zh"), callbacks)
	require.NoError(t, err)
	assert.Len(t, finalTargets, len(targets)+1)
	assert.Equal(t, "22.2.0.0:11434", finalTargets[len(finalTargets)-1])
}

func TestAIInfraScanAgentPortDiscoveryRejectsTooManyDiscoveredEndpoints(t *testing.T) {
	ports := make([]utils.Port, maxDiscoveredScanEndpoints+1)
	for index := range ports {
		ports[index] = utils.Port{PortID: index + 1, State: utils.State{State: "open"}}
	}
	calls := 0
	agent := &AIInfraScanAgent{
		nmapScan: func(host, _ string) (*utils.NmapRun, error) {
			calls++
			if host == "10.0.0.1" {
				return &utils.NmapRun{Hosts: []utils.Host{{
					Address: utils.Address{Addr: host}, Ports: utils.Ports{PortList: ports},
				}}}, nil
			}
			return &utils.NmapRun{}, nil
		},
	}
	callbacks := TaskCallbacks{
		StepStatusUpdateCallback: func(string, string, string, string, string) {},
		ToolUsedCallback:         func(string, string, string, []Tool) {},
		ToolUseLogCallback:       func(string, string, string, string) {},
	}

	_, err := agent.scanPortsAndPrepareTargets([]string{"10.0.0.1", "10.0.0.2"}, portscan.FullTCP, "step-1", initTexts("zh"), callbacks)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "discovered scan endpoint limit")
	assert.Equal(t, 1, calls)
}

func TestAIInfraScanAgentNormalizesPortScanModeFromRequest(t *testing.T) {
	tests := []struct {
		name    string
		params  string
		want    portscan.Mode
		wantErr error
	}{
		{name: "omitted defaults to fixed AI", params: `{}`, want: portscan.FixedAI},
		{name: "full TCP", params: `{"port_scan_mode":"full_tcp"}`, want: portscan.FullTCP},
		{name: "invalid mode fails", params: `{"port_scan_mode":"all"}`, wantErr: portscan.ErrInvalidMode},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var request ScanRequest
			require.NoError(t, json.Unmarshal([]byte(test.params), &request))

			mode, err := normalizePortScanMode(request.PortScanMode)
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, mode)
		})
	}
}

func TestAIInfraScanAgentExecuteRejectsInvalidPortScanModeBeforeScanStarts(t *testing.T) {
	agent := &AIInfraScanAgent{}
	callbacks := TaskCallbacks{
		PlanUpdateCallback: func([]SubTask) {
			t.Fatal("scan execution started for an invalid port scan mode")
		},
	}

	err := agent.Execute(context.Background(), TaskRequest{
		Params: json.RawMessage(`{"port_scan_mode":"all"}`),
	}, callbacks)

	require.ErrorIs(t, err, portscan.ErrInvalidMode)
}

func TestAIInfraScanAgentExecuteRejectsExplicitNullPortScanModeBeforeScanStarts(t *testing.T) {
	assertPortScanModeRejectedBeforeScan(t, `{"port_scan_mode":null}`)
}

func TestAIInfraScanAgentExecuteRejectsPortScanModeFieldNameVariantsAndDuplicatesBeforeScanStarts(t *testing.T) {
	tests := []struct {
		name   string
		params string
	}{
		{name: "uppercase null", params: `{"PORT_SCAN_MODE":null}`},
		{name: "mixed case null", params: `{"Port_Scan_Mode":null}`},
		{name: "canonical and uppercase collision", params: `{"port_scan_mode":"full_tcp","PORT_SCAN_MODE":null}`},
		{name: "uppercase name with valid value", params: `{"PORT_SCAN_MODE":"full_tcp"}`},
		{name: "duplicate canonical field", params: `{"port_scan_mode":"fixed_ai","port_scan_mode":"full_tcp"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertPortScanModeRejectedBeforeScan(t, test.params)
		})
	}
}

func assertPortScanModeRejectedBeforeScan(t *testing.T, params string) {
	t.Helper()
	var callbackCalls int
	var nmapCalls int
	agent := &AIInfraScanAgent{
		nmapScan: func(string, string) (*utils.NmapRun, error) {
			nmapCalls++
			return nil, errors.New("nmap must not run for an invalid port scan mode")
		},
	}
	callbacks := TaskCallbacks{
		ResultCallback: func(map[string]interface{}) { callbackCalls++ },
		ToolUseLogCallback: func(string, string, string, string) {
			callbackCalls++
		},
		ToolUsedCallback:    func(string, string, string, []Tool) { callbackCalls++ },
		NewPlanStepCallback: func(string, string) { callbackCalls++ },
		StepStatusUpdateCallback: func(string, string, string, string, string) {
			callbackCalls++
		},
		PlanUpdateCallback: func([]SubTask) { callbackCalls++ },
		ErrorCallback:      func(string) { callbackCalls++ },
	}

	err := agent.Execute(context.Background(), TaskRequest{
		Content: "198.51.100.1",
		Params:  json.RawMessage(params),
	}, callbacks)

	assert.ErrorIs(t, err, portscan.ErrInvalidMode)
	assert.Zero(t, callbackCalls)
	assert.Zero(t, nmapCalls)
}

func TestAIInfraScanAgentExecuteRejectsNonStringPortScanModeBeforeScanStarts(t *testing.T) {
	for _, value := range []string{"true", "1", "[]", "{}"} {
		t.Run(value, func(t *testing.T) {
			assertPortScanModeRejectedBeforeScan(t, `{"port_scan_mode":`+value+`}`)
		})
	}
}

func TestAIInfraScanAgentExecuteUsesVerifiedCanonicalPortScanModeOrDefault(t *testing.T) {
	tests := []struct {
		name     string
		params   json.RawMessage
		wantSpec string
	}{
		{name: "omitted defaults to fixed AI", params: json.RawMessage(`{}`), wantSpec: portscan.FixedAIPortSpec},
		{name: "canonical full TCP", params: json.RawMessage(`{"port_scan_mode":"full_tcp"}`), wantSpec: portscan.FullTCPPortSpec},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stopAfterNmap := errors.New("stop after nmap")
			var portSpec string
			agent := &AIInfraScanAgent{
				nmapScan: func(_ string, gotPortSpec string) (*utils.NmapRun, error) {
					portSpec = gotPortSpec
					return nil, stopAfterNmap
				},
			}
			callbacks := TaskCallbacks{
				PlanUpdateCallback:       func([]SubTask) {},
				NewPlanStepCallback:      func(string, string) {},
				StepStatusUpdateCallback: func(string, string, string, string, string) {},
				ToolUsedCallback:         func(string, string, string, []Tool) {},
				ToolUseLogCallback:       func(string, string, string, string) {},
			}

			err := agent.Execute(context.Background(), TaskRequest{
				Content: "198.51.100.1",
				Params:  test.params,
			}, callbacks)

			require.ErrorIs(t, err, stopAfterNmap)
			assert.Equal(t, test.wantSpec, portSpec)
		})
	}
}

func TestAIInfraScanAgentPortDiscoveryUsesModePortSpecInNmapAndToolEvents(t *testing.T) {
	tests := []struct {
		name string
		mode portscan.Mode
		want string
	}{
		{name: "fixed AI default", mode: portscan.DefaultMode, want: portscan.FixedAIPortSpec},
		{name: "full TCP", mode: portscan.FullTCP, want: portscan.FullTCPPortSpec},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var nmapPortSpecs []string
			var toolParams []string
			agent := &AIInfraScanAgent{
				nmapScan: func(_ string, portSpec string) (*utils.NmapRun, error) {
					nmapPortSpecs = append(nmapPortSpecs, portSpec)
					return &utils.NmapRun{}, nil
				},
			}
			callbacks := TaskCallbacks{
				StepStatusUpdateCallback: func(string, string, string, string, string) {},
				ToolUsedCallback: func(_ string, _ string, _ string, tools []Tool) {
					for _, tool := range tools {
						if tool.Tool == initTexts("en").nmapTool {
							toolParams = append(toolParams, tool.Message.Param)
						}
					}
				},
				ToolUseLogCallback: func(string, string, string, string) {},
			}

			_, err := agent.scanPortsAndPrepareTargets([]string{"198.51.100.1"}, test.mode, "step-1", initTexts("en"), callbacks)

			require.NoError(t, err)
			assert.Equal(t, []string{test.want}, nmapPortSpecs)
			assert.Equal(t, []string{"-T4 -p " + test.want, "-T4 -p " + test.want}, toolParams)
		})
	}
}

func TestAIInfraScanAgentPortDiscoverySkipsNonBareIPv4Targets(t *testing.T) {
	targets := []string{
		"https://198.51.100.1",
		"example.test",
		"198.51.100.1:8080",
		"2001:db8::1",
		"[2001:db8::1]:443",
		"http://[2001:db8::1]:8080",
	}
	for _, mode := range []portscan.Mode{portscan.FixedAI, portscan.FullTCP} {
		t.Run(string(mode), func(t *testing.T) {
			var nmapCalls int
			agent := &AIInfraScanAgent{
				nmapScan: func(string, string) (*utils.NmapRun, error) {
					nmapCalls++
					return &utils.NmapRun{}, nil
				},
			}
			callbacks := TaskCallbacks{
				StepStatusUpdateCallback: func(string, string, string, string, string) {},
				ToolUsedCallback:         func(string, string, string, []Tool) {},
				ToolUseLogCallback:       func(string, string, string, string) {},
			}

			finalTargets, err := agent.scanPortsAndPrepareTargets(targets, mode, "step-1", initTexts("en"), callbacks)

			require.NoError(t, err)
			assert.Equal(t, targets, finalTargets)
			assert.Zero(t, nmapCalls)
		})
	}
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
