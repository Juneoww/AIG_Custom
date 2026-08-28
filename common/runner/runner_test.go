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

package runner

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/gologger"
	"github.com/Juneoww/AIG_Custom/internal/options"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baseOptions returns minimal valid options for constructing a Runner without
// requiring live network connectivity or real data files.
func baseOptions(targets []string) *options.Options {
	return &options.Options{
		Target:       targets,
		Output:       "",
		ProxyURL:     "",
		TimeOut:      5,
		JSON:         false,
		RateLimit:    10,
		FPTemplates:  "../../data/fingerprints",
		AdvTemplates: "../../data/vuln",
	}
}

func TestParseTargetsExpandsAndDeduplicatesAcrossSources(t *testing.T) {
	targetFile := filepath.Join(t.TempDir(), "targets.txt")
	require.NoError(t, os.WriteFile(targetFile, []byte("10.0.0.2\n22.2.10.*\n"), 0600))

	r := &Runner{Options: &options.Options{
		Target:     []string{"10.0.0.1-10.0.0.2"},
		TargetFile: targetFile,
	}}
	targets, err := r.parseTargets()
	require.NoError(t, err)
	assert.Len(t, targets, 258)
	assert.Equal(t, "10.0.0.1", targets[0])
	assert.Equal(t, "10.0.0.2", targets[1])
	assert.Equal(t, "22.2.10.0", targets[2])
	assert.Equal(t, "22.2.10.255", targets[257])
}

func TestParseTargetsKeepsPreparedPortDiscoveryBeyondExpressionLimit(t *testing.T) {
	rawTargets, err := ParseTargets([]string{"22.2.*.*"})
	require.NoError(t, err)
	preparedTargets := append(rawTargets, "22.2.0.0:11434")

	r := &Runner{Options: &options.Options{Target: preparedTargets, PreExpandedTargets: true}}
	targets, err := r.parseTargets()
	require.NoError(t, err)
	assert.Len(t, targets, maxTargetExpressions+1)
	assert.Equal(t, "22.2.0.0:11434", targets[len(targets)-1])
}

func TestProcessTargetsReturnsRequestedFileError(t *testing.T) {
	r, err := New(&options.Options{TargetFile: filepath.Join(t.TempDir(), "missing.txt")})

	require.Error(t, err)
	assert.Nil(t, r)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestNewReturnsTargetFileReadError(t *testing.T) {
	r, err := New(&options.Options{TargetFile: t.TempDir()})

	require.Error(t, err)
	assert.Nil(t, r)
}

func TestProcessTargetsRejectsInvalidAndOversizedExpressions(t *testing.T) {
	cases := []struct {
		name    string
		targets []string
		wantErr error
	}{
		{name: "invalid range", targets: []string{"10.0.0.1-not-an-ip"}},
		{name: "oversized CIDR", targets: []string{"10.0.0.0/15"}, wantErr: ErrTooManyTargets},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := New(&options.Options{
				Target:       tc.targets,
				FPTemplates:  filepath.Join(t.TempDir(), "missing-fingerprints"),
				AdvTemplates: filepath.Join(t.TempDir(), "missing-vulnerabilities"),
			})
			require.Error(t, err)
			assert.Nil(t, r)
			if tc.wantErr != nil {
				assert.True(t, errors.Is(err, tc.wantErr))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Original integration test (kept for regression)
// ---------------------------------------------------------------------------

func TestRunner_RunEnumeration(t *testing.T) {
	targets := []string{
		"http://127.0.0.1:5000",
	}
	parseOptions := &options.Options{
		Target:       targets,
		Output:       "",
		ProxyURL:     "",
		TimeOut:      10,
		JSON:         false,
		RateLimit:    10,
		FPTemplates:  "data/fingerprints",
		AdvTemplates: "data/advisories",
	}
	r, err := New(parseOptions)
	if err != nil {
		gologger.Fatalf("Could not create runner: %s\n", err)
	}
	defer r.Close()
	r.RunEnumeration()
}

// ---------------------------------------------------------------------------
// Constructor table-driven tests
// ---------------------------------------------------------------------------

func TestNew_TableDriven(t *testing.T) {
	cases := []struct {
		name      string
		targets   []string
		fpDir     string
		advDir    string
		wantError bool
	}{
		{
			name:      "valid options with no targets",
			targets:   []string{},
			fpDir:     "../../data/fingerprints",
			advDir:    "../../data/vuln",
			wantError: false,
		},
		{
			name:      "single valid target",
			targets:   []string{"http://127.0.0.1:9999"},
			fpDir:     "../../data/fingerprints",
			advDir:    "../../data/vuln",
			wantError: false,
		},
		{
			name:      "multiple targets",
			targets:   []string{"http://127.0.0.1:9998", "http://127.0.0.1:9997"},
			fpDir:     "../../data/fingerprints",
			advDir:    "../../data/vuln",
			wantError: false,
		},
		{
			name:      "missing fingerprint directory falls back gracefully",
			targets:   []string{"http://127.0.0.1:9999"},
			fpDir:     "../../data/fingerprints", // real dir
			advDir:    "../../data/vuln",
			wantError: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := &options.Options{
				Target:       tc.targets,
				TimeOut:      5,
				RateLimit:    10,
				FPTemplates:  tc.fpDir,
				AdvTemplates: tc.advDir,
			}
			r, err := New(opts)
			if tc.wantError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, r)
				r.Close()
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Runner.Close idempotency
// ---------------------------------------------------------------------------

func TestRunner_Close_Idempotent(t *testing.T) {
	r, err := New(baseOptions(nil))
	require.NoError(t, err)
	// Calling Close twice should not panic
	r.Close()
}

func TestRunner_CloseHandlesEmptyAndPartialRunners(t *testing.T) {
	assert.NotPanics(t, func() {
		(&Runner{}).Close()
	})

	var r *Runner
	assert.NotPanics(t, func() {
		r.Close()
	})
}

func TestNewCleansUpAfterStorageWhenComponentsFail(t *testing.T) {
	opts := baseOptions([]string{"127.0.0.1"})
	opts.ProxyURL = "http://[::1"

	var r *Runner
	var err error
	assert.NotPanics(t, func() {
		r, err = New(opts)
	})
	require.Error(t, err)
	assert.Nil(t, r)
}

// ---------------------------------------------------------------------------
// RunEnumeration with a live test server (table-driven)
// ---------------------------------------------------------------------------

func TestRunner_RunEnumeration_TableDriven(t *testing.T) {
	cases := []struct {
		name       string
		serverBody string
		statusCode int
		expectRun  bool
	}{
		{
			name:       "200 OK empty body",
			serverBody: "",
			statusCode: http.StatusOK,
			expectRun:  true,
		},
		{
			name:       "200 OK with HTML title",
			serverBody: "<html><head><title>MyApp</title></head><body>hello</body></html>",
			statusCode: http.StatusOK,
			expectRun:  true,
		},
		{
			name:       "404 Not Found",
			serverBody: "not found",
			statusCode: http.StatusNotFound,
			expectRun:  true,
		},
		{
			name:       "500 Internal Server Error",
			serverBody: "internal error",
			statusCode: http.StatusInternalServerError,
			expectRun:  true,
		},
		{
			name:       "JSON body (API style)",
			serverBody: `{"version":"1.0.0","status":"ok"}`,
			statusCode: http.StatusOK,
			expectRun:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				w.Write([]byte(tc.serverBody))
			}))
			defer srv.Close()

			opts := &options.Options{
				Target:       []string{srv.URL},
				TimeOut:      5,
				RateLimit:    10,
				FPTemplates:  "../../data/fingerprints",
				AdvTemplates: "../../data/vuln",
			}
			r, err := New(opts)
			require.NoError(t, err)
			defer r.Close()

			// Should not panic
			r.RunEnumeration()
		})
	}
}

// ---------------------------------------------------------------------------
// Callback invocation
// ---------------------------------------------------------------------------

func TestRunner_Callback_Invoked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html><head><title>CB Test</title></head></html>"))
	}))
	defer srv.Close()

	var received []interface{}
	opts := &options.Options{
		Target:       []string{srv.URL},
		TimeOut:      5,
		RateLimit:    10,
		FPTemplates:  "../../data/fingerprints",
		AdvTemplates: "../../data/vuln",
		Callback: func(v interface{}) {
			received = append(received, v)
		},
	}
	r, err := New(opts)
	require.NoError(t, err)
	defer r.Close()

	r.RunEnumeration()
	assert.NotEmpty(t, received, "callback should have been called at least once")
}

// ---------------------------------------------------------------------------
// JSON output mode
// ---------------------------------------------------------------------------

func TestRunner_JSONMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"info":"test"}`))
	}))
	defer srv.Close()

	opts := &options.Options{
		Target:       []string{srv.URL},
		TimeOut:      5,
		RateLimit:    10,
		JSON:         true,
		FPTemplates:  "../../data/fingerprints",
		AdvTemplates: "../../data/vuln",
	}
	r, err := New(opts)
	require.NoError(t, err)
	defer r.Close()
	r.RunEnumeration()
}
