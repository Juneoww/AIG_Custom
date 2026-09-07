package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 从私有 assignment 解码，验证旧数据缺省拒绝、新许可贯穿真实传输。
func httpAuthAssignmentForTest(t *testing.T, origin string, allowHTTP bool) *TargetAuth {
	t.Helper()
	auth := authForTest(origin)
	require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"allow_insecure_http":%t}`, allowHTTP)), auth))
	return auth
}

func TestTargetAuthHTTPAssignmentRequiresExplicitPermission(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(fmt.Sprint(allowed), func(t *testing.T) {
			auth := httpAuthAssignmentForTest(t, "http://example.com:80", allowed)
			if !allowed {
				require.Error(t, auth.Validate())
				return
			}
			require.NoError(t, auth.Validate())
			copy := auth.clone()
			require.NoError(t, copy.Validate())
			assert.Equal(t, "http://example.com", copy.Origin)
			encoded, err := json.Marshal(copy)
			require.NoError(t, err)
			assert.Contains(t, string(encoded), `"allow_insecure_http":true`)
		})
	}
}

func TestTargetAuthHTTPRealRequestRedirectAndReflections(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer never-log-this-token", r.Header.Get("Authorization"))
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/finish", http.StatusFound)
			return
		}
		w.Header().Set("X-Echo", r.Header.Get("Authorization"))
		w.Header().Set("Set-Cookie", "session=never-log-this-token")
		_, _ = w.Write([]byte("<title>never-log-this-token</title>" + r.Header.Get("Authorization")))
	}))
	defer srv.Close()
	opts := defaultOpts()
	opts.TargetAuth = httpAuthAssignmentForTest(t, srv.URL, true)
	h, err := NewHttpx(opts)
	require.NoError(t, err)
	assert.False(t, h.client.HTTPClient.Transport.(*targetAuthTransport).base.TLSClientConfig.InsecureSkipVerify)
	req, err := h.newRequest(http.MethodGet, srv.URL+"/start", nil)
	require.NoError(t, err)
	resp, err := h.do(req)
	require.NoError(t, err)
	assert.Equal(t, []string{"/start", "/finish"}, paths)
	assert.Empty(t, req.Header.Get("Authorization"))
	assert.Empty(t, resp.Request.Header.Get("Authorization"))
	assert.Empty(t, resp.Header.Get("Set-Cookie"))
	for _, value := range []string{resp.DataStr, resp.Title, resp.DumpResponse(), resp.Header.Get("X-Echo")} {
		assert.NotContains(t, value, "never-log-this-token")
		assert.Contains(t, value, targetAuthRedacted)
	}
}

func TestTargetAuthHTTPDefaultDenialMakesNoRequest(t *testing.T) {
	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer srv.Close()
	opts := defaultOpts()
	opts.TargetAuth = authForTest(srv.URL)
	_, err := NewHttpx(opts)
	require.Error(t, err)
	assert.Zero(t, received.Load())
}

func TestTargetAuthHTTPRejectsRedirectEscapeBeforeNetwork(t *testing.T) {
	var received atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer other.Close()
	for _, destination := range []string{other.URL + "/stolen", strings.Replace(other.URL, "127.0.0.1", "localhost", 1) + "/stolen"} {
		t.Run(destination, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, destination, http.StatusFound)
			}))
			defer srv.Close()
			opts := defaultOpts()
			opts.TargetAuth = httpAuthAssignmentForTest(t, srv.URL, true)
			h, err := NewHttpx(opts)
			require.NoError(t, err)
			_, err = h.Get(srv.URL, nil)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), destination)
			assert.Zero(t, received.Load())
		})
	}
}

func TestNormalizeTargetOriginHTTPPermission(t *testing.T) {
	for input, want := range map[string]string{
		"HTTP://Example.COM:80/":   "http://example.com",
		"http://example.com:080":   "http://example.com",
		"http://example.com:443":   "http://example.com:443",
		"http://example.com:8080":  "http://example.com:8080",
		"http://127.0.0.1:80":      "http://127.0.0.1",
		"http://[2001:db8::1]:80/": "http://[2001:db8::1]",
		"HTTPS://Example.COM:443/": "https://example.com",
		"https://example.com:80":   "https://example.com:80",
	} {
		t.Run(input, func(t *testing.T) {
			got, err := NormalizeTargetOrigin(input, true)
			require.NoError(t, err)
			assert.Equal(t, want, got)
			if strings.HasPrefix(want, "http://") {
				_, err = NormalizeTargetOrigin(input)
				require.Error(t, err)
				_, err = NormalizeTargetOrigin(input, false)
				require.Error(t, err)
			}
		})
	}
	for _, invalid := range []string{"ftp://example.com", "http://user:secret@example.com", "http://example.com/path", "http://example.com?secret=value", "http://example.com#fragment", "http://example.com:0", "http://example.com:65536", "http://example.com:", "http://[fe80::1%25eth0]", "http://127.1", "http://example.com\\evil"} {
		_, err := NormalizeTargetOrigin(invalid, true)
		require.Error(t, err, invalid)
	}
}

func TestValidateTargetURLsHTTPPermissionKeepsExactOrigin(t *testing.T) {
	targets := []string{"http://EXAMPLE.com:80/api", "http://example.com/health?q=value"}
	require.NoError(t, ValidateTargetURLs("http://example.com", targets, true))
	require.Error(t, ValidateTargetURLs("http://example.com", targets))
	require.Error(t, ValidateTargetURLs("http://example.com", targets, false))
	for _, origin := range []string{"http://example.com", "https://example.com:80"} {
		for _, target := range []string{"http://example.com:443", "https://example.com", "http://other.example", "http://example.com:8080", "example.com", "http://example.com/#fragment", "http://example.com/%0a", "http://user:secret@example.com"} {
			require.Error(t, ValidateTargetURLs(origin, []string{target}, true), origin+" -> "+target)
		}
	}
	require.Error(t, ValidateTargetURLs("https://example.com:80", []string{"http://example.com:80"}, true))
	require.Error(t, ValidateTargetURLs("http://example.com:443", []string{"https://example.com:443"}, true))
}

func TestTargetAuthHTTPRejectsOffOriginAndHostOverridesBeforeDial(t *testing.T) {
	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer srv.Close()
	opts := defaultOpts()
	opts.TargetAuth = httpAuthAssignmentForTest(t, srv.URL, true)
	h, err := NewHttpx(opts)
	require.NoError(t, err)
	transport := h.client.HTTPClient.Transport.(*targetAuthTransport)
	var dials atomic.Int32
	transport.base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	for _, target := range []string{strings.Replace(srv.URL, "http://", "https://", 1), strings.Replace(srv.URL, "127.0.0.1", "localhost", 1), "http://127.0.0.1:1"} {
		_, err := h.Get(target, nil)
		require.Error(t, err)
	}
	for _, headers := range []map[string]string{{"Host": "evil.example"}, {"host": "evil.example"}, {"Connection": "Authorization"}, {"Upgrade": "websocket"}} {
		_, err := h.Get(srv.URL, headers)
		require.Error(t, err)
	}
	assert.Zero(t, dials.Load())
	assert.Zero(t, received.Load())
	// HTTP 同源 Host 覆盖按当前协议判断，不能套用 HTTPS 默认端口。
	_, err = h.Get(srv.URL, map[string]string{"Host": strings.TrimPrefix(srv.URL, "http://")})
	require.NoError(t, err)
	assert.Equal(t, int32(1), received.Load())
}

func TestTargetAuthHTTPPermissionRejectsSamePortCrossSchemeRedirectBeforeDial(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprint(secure), func(t *testing.T) {
			var destination string
			var received atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received.Add(1)
				http.Redirect(w, r, destination, http.StatusFound)
			})
			var srv *httptest.Server
			if secure {
				srv = httptest.NewTLSServer(handler)
				destination = strings.Replace(srv.URL, "https://", "http://", 1)
			} else {
				srv = httptest.NewServer(handler)
				destination = strings.Replace(srv.URL, "http://", "https://", 1)
			}
			defer srv.Close()
			auth := httpAuthAssignmentForTest(t, srv.URL, true)
			var h *HTTPX
			if secure {
				h = trustedAuthHTTPX(t, srv, auth)
			} else {
				opts := defaultOpts()
				opts.TargetAuth = auth
				var err error
				h, err = NewHttpx(opts)
				require.NoError(t, err)
			}
			var dials atomic.Int32
			h.client.HTTPClient.Transport.(*targetAuthTransport).base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				dials.Add(1)
				return (&net.Dialer{}).DialContext(ctx, network, address)
			}
			_, err := h.Get(srv.URL, nil)
			require.Error(t, err)
			assert.Equal(t, int32(1), received.Load())
			assert.Equal(t, int32(1), dials.Load(), "cross-scheme redirect must be rejected before dialing even on the same host and port")
		})
	}
}

func TestTargetAuthHTTPPermissionKeepsTLSVerification(t *testing.T) {
	var received atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	defer srv.Close()
	opts := defaultOpts()
	opts.TargetAuth = httpAuthAssignmentForTest(t, srv.URL, true)
	h, err := NewHttpx(opts)
	require.NoError(t, err)
	_, err = h.Get(srv.URL, nil)
	require.Error(t, err)
	assert.Zero(t, received.Load())
}
