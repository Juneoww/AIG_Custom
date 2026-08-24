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
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedConsoleManifestContainsOnlyReviewedAssets(t *testing.T) {
	index, err := staticFS.ReadFile("static/index.html")
	require.NoError(t, err)
	assert.Contains(t, string(index), "assets/index-")
	assert.NotContains(t, string(index), "A.I.G")

	requiredAssets := []string{
		"static/fonts/DroidSansFallbackFull.ttf",
		"static/fonts/IBMPlexSans-Regular.woff2",
		"static/fonts/IBMPlexSans-SemiBold.woff2",
		"static/licenses/APACHE-2.0.txt",
		"static/licenses/DROID_FONT_LICENSE.txt",
		"static/licenses/IBM_PLEX_LICENSE.txt",
	}
	for _, asset := range requiredAssets {
		_, readErr := staticFS.ReadFile(asset)
		require.NoErrorf(t, readErr, "required embedded asset %s is missing", asset)
	}

	for _, forbidden := range []string{
		"static/aigdocs",
		"static/fonts/Tencentsans.ttf",
	} {
		_, statErr := fs.Stat(staticFS, forbidden)
		assert.Error(t, statErr, "legacy asset %s must not be embedded", forbidden)
	}

	cssFiles, err := fs.Glob(staticFS, "static/assets/*.css")
	require.NoError(t, err)
	require.Len(t, cssFiles, 1)
	javaScriptFiles, err := fs.Glob(staticFS, "static/assets/*.js")
	require.NoError(t, err)
	require.Len(t, javaScriptFiles, 1)

	actualFiles := make([]string, 0)
	err = fs.WalkDir(staticFS, "static", func(assetPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			actualFiles = append(actualFiles, assetPath)
		}
		return nil
	})
	require.NoError(t, err)
	expectedFiles := append([]string{"static/index.html"}, requiredAssets...)
	expectedFiles = append(expectedFiles, cssFiles...)
	expectedFiles = append(expectedFiles, javaScriptFiles...)
	assert.ElementsMatch(t, expectedFiles, actualFiles)

	for _, textAsset := range append([]string{"static/index.html"}, append(cssFiles, javaScriptFiles...)...) {
		content, readErr := staticFS.ReadFile(textAsset)
		require.NoError(t, readErr)
		assert.NotContains(t, string(content), "A.I.G")
		assert.NotContains(t, strings.ToLower(string(content)), "aigdocs")
		assert.NotContains(t, strings.ToLower(string(content)), "tencentsans")
		assert.NotContains(t, string(content), "/api/v1/app/")
		if strings.HasSuffix(textAsset, ".css") {
			assert.NotContains(t, strings.ToLower(string(content)), "http://")
			assert.NotContains(t, strings.ToLower(string(content)), "https://")
		}
	}
}

func TestEmbeddedConsoleRoutesServeSPAWithoutMaskingAPIErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerEmbeddedStaticRoutes(router)

	cssFiles, err := fs.Glob(staticFS, "static/assets/*.css")
	require.NoError(t, err)
	require.Len(t, cssFiles, 1)
	cssPath := strings.TrimPrefix(cssFiles[0], "static")
	javaScriptFiles, err := fs.Glob(staticFS, "static/assets/*.js")
	require.NoError(t, err)
	require.Len(t, javaScriptFiles, 1)
	javaScriptPath := strings.TrimPrefix(javaScriptFiles[0], "static")

	for _, testCase := range []struct {
		name             string
		method           string
		path             string
		wantStatus       int
		wantCacheControl string
		wantContentType  string
		wantHTML         bool
	}{
		{
			name:             "root serves the console entry document",
			method:           http.MethodGet,
			path:             "/",
			wantStatus:       http.StatusOK,
			wantCacheControl: "no-cache",
			wantContentType:  "text/html",
			wantHTML:         true,
		},
		{
			name:             "deep console route falls back to the entry document",
			method:           http.MethodGet,
			path:             "/governance",
			wantStatus:       http.StatusOK,
			wantCacheControl: "no-cache",
			wantContentType:  "text/html",
			wantHTML:         true,
		},
		{
			name:             "hashed style asset is immutable",
			method:           http.MethodGet,
			path:             cssPath,
			wantStatus:       http.StatusOK,
			wantCacheControl: "public, max-age=31536000, immutable",
			wantContentType:  "text/css",
		},
		{
			name:             "hashed javascript asset is immutable",
			method:           http.MethodGet,
			path:             javaScriptPath,
			wantStatus:       http.StatusOK,
			wantCacheControl: "public, max-age=31536000, immutable",
		},
		{
			name:             "droid font is immutable",
			method:           http.MethodGet,
			path:             "/fonts/DroidSansFallbackFull.ttf",
			wantStatus:       http.StatusOK,
			wantCacheControl: "public, max-age=31536000, immutable",
		},
		{
			name:             "ibm regular font is immutable",
			method:           http.MethodGet,
			path:             "/fonts/IBMPlexSans-Regular.woff2",
			wantStatus:       http.StatusOK,
			wantCacheControl: "public, max-age=31536000, immutable",
		},
		{
			name:             "ibm semibold font is immutable",
			method:           http.MethodGet,
			path:             "/fonts/IBMPlexSans-SemiBold.woff2",
			wantStatus:       http.StatusOK,
			wantCacheControl: "public, max-age=31536000, immutable",
		},
		{
			name:       "unknown api route remains a not found response",
			method:     http.MethodGet,
			path:       "/api/v1/app/removed-endpoint",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "legacy route is not revived by the spa fallback",
			method:     http.MethodGet,
			path:       "/legacy",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "missing static asset is not rewritten to html",
			method:     http.MethodGet,
			path:       "/assets/missing.css",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "non get route is not rewritten to html",
			method:     http.MethodPost,
			path:       "/governance",
			wantStatus: http.StatusNotFound,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(testCase.method, testCase.path, nil)
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			assert.Equal(t, testCase.wantStatus, response.Code)
			assert.Equal(t, testCase.wantCacheControl, response.Header().Get("Cache-Control"))
			if testCase.wantContentType != "" {
				assert.Contains(t, response.Header().Get("Content-Type"), testCase.wantContentType)
			}
			if testCase.wantHTML {
				assert.Contains(t, response.Body.String(), "<div id=\"root\">")
			} else {
				assert.NotContains(t, response.Body.String(), "<div id=\"root\">")
			}
		})
	}
}
