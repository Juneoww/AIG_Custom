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

package websocket

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	version "github.com/Juneoww/AIG_Custom/internal/options"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSafeVersionUsesInjectedMetadataAndUnknownDefaults(t *testing.T) {
	originalCommit, originalBuildTime := BuildCommit, BuildTime
	t.Cleanup(func() { BuildCommit, BuildTime = originalCommit, originalBuildTime })

	for _, testCase := range []struct {
		name                 string
		commit, buildTime    string
		wantCommit, wantTime string
	}{
		{name: "unknown", wantCommit: "unknown", wantTime: "unknown"},
		{name: "injected", commit: "abc123", buildTime: "2026-08-14T10:00:00Z", wantCommit: "abc123", wantTime: "2026-08-14T10:00:00Z"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			BuildCommit, BuildTime = testCase.commit, testCase.buildTime
			response := safeVersionResponse()
			assert.Equal(t, version.GetVersion(), response.Version)
			assert.Equal(t, testCase.wantCommit, response.Commit)
			assert.Equal(t, testCase.wantTime, response.BuildTime)
		})
	}
}

func TestSafeVersionHasNoFileOrNetworkDependency(t *testing.T) {
	originalCommit, originalBuildTime := BuildCommit, BuildTime
	originalTransport := http.DefaultTransport
	originalWorkingDirectory, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() {
		BuildCommit, BuildTime = originalCommit, originalBuildTime
		http.DefaultTransport = originalTransport
		require.NoError(t, os.Chdir(originalWorkingDirectory))
	})
	BuildCommit, BuildTime = "", ""
	http.DefaultTransport = failingRoundTripper{}
	require.NoError(t, os.Chdir(t.TempDir()))

	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	HandleSafeVersion(context)

	require.Equal(t, http.StatusOK, response.Code)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &fields))
	assert.Equal(t, "unknown", fields["commit"])
	assert.Equal(t, "unknown", fields["build_time"])
}

type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("network access is forbidden")
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected [3]int
	}{
		{"v4.1.10", [3]int{4, 1, 10}},
		{"v4.1.9", [3]int{4, 1, 9}},
		{"v1.0.0", [3]int{1, 0, 0}},
		{"4.1.10", [3]int{4, 1, 10}}, // without "v" prefix
		{"v0.0.1", [3]int{0, 0, 1}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseVersion(tt.input)
			if result != tt.expected {
				t.Errorf("parseVersion(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected bool // true if b > a
	}{
		{"patch upgrade", "v4.1.9", "v4.1.10", true},
		{"minor upgrade", "v4.1.10", "v4.2.0", true},
		{"major upgrade", "v4.1.10", "v5.0.0", true},
		{"same version", "v4.1.10", "v4.1.10", false},
		{"downgrade patch", "v4.1.10", "v4.1.9", false},
		{"downgrade minor", "v4.2.0", "v4.1.10", false},
		{"downgrade major", "v5.0.0", "v4.1.10", false},
		{"large patch numbers", "v4.1.99", "v4.1.100", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := compareVersions(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("compareVersions(%q, %q) = %v, want %v", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}
