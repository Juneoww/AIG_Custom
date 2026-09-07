package httpx

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTargetAuthRejectsUntrustedTLS(t *testing.T) {
	var received atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	defer srv.Close()
	opts := defaultOpts()
	// 通过公开选项解码，以便缺少认证选项时也能真实重现不受信任证书被接受。
	require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"TargetAuth":{"credential_id":"cred-test","revision":1,"origin":%q,"headers":{"Authorization":"Bearer never-log-this-token"}}}`, srv.URL)), opts))
	h, err := NewHttpx(opts)
	require.NoError(t, err)
	_, err = h.Get(srv.URL, nil)
	require.Error(t, err, "authenticated requests must verify the server certificate")
	assert.Zero(t, received.Load(), "untrusted server must receive no authenticated HTTP request")
}

func TestNormalizeTargetOrigin(t *testing.T) {
	for input, want := range map[string]string{
		"HTTPS://Example.COM:443/":   "https://example.com",
		"https://example.com:8443":   "https://example.com:8443",
		"https://127.0.0.1:443":      "https://127.0.0.1",
		"https://[2001:db8::1]:443/": "https://[2001:db8::1]",
		"https://localhost":          "https://localhost",
	} {
		t.Run(input, func(t *testing.T) {
			got, err := NormalizeTargetOrigin(input)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
	for _, input := range []string{
		"", "http://example.com", "example.com", "https://user:secret@example.com", "https://example.com/path",
		"https://example.com?", "https://example.com?secret=value", "https://example.com#", "https://example.com#x",
		"https://example.com:0", "https://example.com:65536", "https://example.com:", "https://bad_host",
		"https://-bad.example", "https://bad..example", "https://127.1", "https://0177.0.0.1", "https://2130706433",
		"https://0x7f000001", "https://99999999999999999999999999", "https://[fe80::1%25eth0]", "https://[example.com]", "https://example.com/\n",
		" https://example.com", "https://example.com/%2f", "https://example.com\\evil",
	} {
		t.Run(input, func(t *testing.T) {
			_, err := NormalizeTargetOrigin(input)
			require.Error(t, err)
			assert.Equal(t, "invalid target credential origin", err.Error())
		})
	}
}

func TestValidateTargetURLs(t *testing.T) {
	require.NoError(t, ValidateTargetURLs("https://example.com", []string{"https://EXAMPLE.com:443/a", "https://example.com/b?q=normal"}))
	for _, targets := range [][]string{
		nil, {"http://example.com"}, {"example.com"}, {"https://example.com:444/a"}, {"https://other.example/a"},
		{"https://user:secret@example.com/a"}, {"https://example.com/#fragment"}, {"https://example.com/#"},
		{"https://example.com/a", "https://other.example/b"}, {"https://example.com/%0A"},
	} {
		require.Error(t, ValidateTargetURLs("https://example.com", targets))
	}
}

func TestValidCredentialHeader(t *testing.T) {
	for _, name := range []string{"Authorization", "Cookie", "X-API-Key", "api-key", "ApiKey", "X-Auth-Token", "Ocp-Apim-Subscription-Key"} {
		assert.True(t, ValidCredentialHeader(name), name)
	}
	for _, name := range []string{"", "Host", "Proxy-Authorization", "Proxy-Connection", "Connection", "Content-Length", "Content-Type", "Accept", "User-Agent", "Location", "Set-Cookie", "Forwarded", "X-Forwarded-Host", "X-Forwarded-For", "Upgrade", "TE", "Trailer", "Transfer-Encoding", "X-Original-URL", "X-Rewrite-URL", "X-HTTP-Method-Override", "X-Accel-Redirect", "X-Host", "Referer", "Origin", " Sec-Key", "Bad\r\nName", "X Bad"} {
		assert.False(t, ValidCredentialHeader(name), name)
	}
}

func authForTest(origin string) *TargetAuth {
	return &TargetAuth{CredentialID: "cred-test", Revision: 1, Origin: origin, Headers: map[string]string{"Authorization": "Bearer never-log-this-token"}}
}

func TestTargetAuthValidationAndFormatting(t *testing.T) {
	auth := authForTest("https://example.com")
	require.NoError(t, auth.Validate())
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		assert.NotContains(t, fmt.Sprintf(format, auth), "never-log")
		assert.NotContains(t, fmt.Sprintf(format, *auth), "never-log")
	}
	encoded, err := json.Marshal(auth)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), "never-log-this-token", "private assignment JSON must carry the runtime material")
	for _, mutate := range []func(*TargetAuth){
		func(a *TargetAuth) { a.CredentialID = "" },
		func(a *TargetAuth) { a.Revision = 0 },
		func(a *TargetAuth) { a.Origin = "http://example.com" },
		func(a *TargetAuth) { a.Headers = nil },
		func(a *TargetAuth) { a.Headers["Cookie"] = "secret=value" },
		func(a *TargetAuth) { a.Headers = map[string]string{"Host": "example.com"} },
		func(a *TargetAuth) { a.Headers["Authorization"] = "Bearer secret\r\nHost: evil" },
		func(a *TargetAuth) { a.Headers["Authorization"] = "\tsecret" },
		func(a *TargetAuth) { a.Headers["Authorization"] = "" },
	} {
		invalid := authForTest("https://example.com")
		mutate(invalid)
		require.Error(t, invalid.Validate())
	}
	var absent *TargetAuth
	require.Error(t, absent.Validate())
}

func TestTargetAuthValidationAcceptsEncodedMaximumSecret(t *testing.T) {
	auth := authForTest("https://example.com")
	for _, value := range []string{
		"Bearer " + strings.Repeat("s", 8192),
		"Basic " + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("u", 256)+":"+strings.Repeat("s", 8192))),
	} {
		auth.Headers["Authorization"] = value
		require.NoError(t, auth.Validate())
	}
}

func TestTargetAuthRejectsAdditionalTransportControlHeaders(t *testing.T) {
	for _, name := range []string{"Proxy", "HTTP2-Settings", "X-HTTP-Host-Override", "X-URL-Scheme", "Front-End-Https"} {
		assert.False(t, ValidCredentialHeader(name), name)
	}
}

func trustedAuthHTTPX(t *testing.T, srv *httptest.Server, auth *TargetAuth) *HTTPX {
	t.Helper()
	opts := defaultOpts()
	opts.TargetAuth = auth
	h, err := NewHttpx(opts)
	require.NoError(t, err)
	transport, ok := h.client.HTTPClient.Transport.(*targetAuthTransport)
	require.True(t, ok, "authenticated traffic must use the scoped transport")
	require.False(t, transport.base.TLSClientConfig.InsecureSkipVerify)
	// 测试仅信任当前 httptest 证书；不能关闭证书或主机名验证。
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	transport.base.TLSClientConfig.RootCAs = pool
	return h
}

func TestTargetAuthInjectsOnlyIntoTransportClone(t *testing.T) {
	var received string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Get("Authorization")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	h := trustedAuthHTTPX(t, srv, authForTest(srv.URL))
	req, err := h.newRequest(http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := h.do(req)
	require.NoError(t, err)
	assert.Equal(t, "Bearer never-log-this-token", received)
	assert.Empty(t, req.Header.Get("Authorization"))
	assert.Empty(t, resp.Request.Header.Get("Authorization"))
	assert.NotContains(t, fmt.Sprintf("%+v", resp.Request), "never-log-this-token")
	assert.NotContains(t, DumpRequestRaw(req), "never-log-this-token")
}

func TestTargetAuthSameOriginRedirect(t *testing.T) {
	var paths []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer never-log-this-token", r.Header.Get("Authorization"))
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/finish", http.StatusFound)
			return
		}
		w.Write([]byte("complete"))
	}))
	defer srv.Close()
	h := trustedAuthHTTPX(t, srv, authForTest(srv.URL))
	resp, err := h.Get(srv.URL+"/start", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"/start", "/finish"}, paths)
	assert.Empty(t, resp.Request.Header.Get("Authorization"))
	assert.Equal(t, "complete", resp.DataStr)
}

func TestTargetAuthRejectsRedirectEscapeBeforeSending(t *testing.T) {
	var received atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer other.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer plain.Close()
	for _, destination := range []string{other.URL + "/stolen", plain.URL + "/stolen", "https://other.invalid/stolen"} {
		t.Run(destination, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, destination, http.StatusFound)
			}))
			defer srv.Close()
			h := trustedAuthHTTPX(t, srv, authForTest(srv.URL))
			_, err := h.Get(srv.URL, nil)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), destination)
			assert.Zero(t, received.Load())
		})
	}
}

func TestTargetAuthRejectsHostOverridesAndOffOriginRules(t *testing.T) {
	var received atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer srv.Close()
	h := trustedAuthHTTPX(t, srv, authForTest(srv.URL))
	for _, headers := range []map[string]string{{"Host": "evil.example"}, {"host": "evil.example"}, {"Connection": "Authorization"}} {
		_, err := h.Get(srv.URL, headers)
		require.Error(t, err)
	}
	_, err := h.Get("https://other.invalid/rule", nil)
	require.Error(t, err)
	assert.Zero(t, received.Load())
}

func TestTargetAuthPreventsUnsafeAndHTTP2Bypass(t *testing.T) {
	opts := defaultOpts()
	opts.TargetAuth = authForTest("https://example.com")
	opts.Unsafe = true
	_, err := NewHttpx(opts)
	require.Error(t, err)
	var received atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer srv.Close()
	h := trustedAuthHTTPX(t, srv, authForTest(srv.URL))
	h.Options.Unsafe = true
	req, err := h.newRequest(http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	_, err = h.do(req)
	require.Error(t, err)
	_, err = h.doUnsafe(req)
	require.Error(t, err)
	assert.Same(t, h.client.HTTPClient, h.client.HTTPClient2)
	assert.Same(t, h.client.HTTPClient, h.client2)
	for _, client := range []*http.Client{h.client.HTTPClient2, h.client2} {
		_, err := client.Get("http://127.0.0.1:1")
		require.Error(t, err)
	}
	assert.Zero(t, received.Load())
}

func TestTargetAuthSnapshotCannotBeRetargeted(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer never-log-this-token", r.Header.Get("Authorization"))
	}))
	defer srv.Close()
	auth := authForTest(srv.URL)
	h := trustedAuthHTTPX(t, srv, auth)
	auth.Origin = "https://other.invalid"
	auth.Headers["Authorization"] = "mutated"
	h.Options.TargetAuth = nil
	_, err := h.Get(srv.URL, nil)
	require.NoError(t, err)
	_, err = h.Get("https://other.invalid", nil)
	require.Error(t, err)
}

func TestTargetAuthRedactsSimpleReflections(t *testing.T) {
	password := `p@ss/"word<>&`
	basic := base64.StdEncoding.EncodeToString([]byte("username:" + password))
	for _, tc := range []struct {
		name, header, value string
		secrets             []string
	}{
		{"bearer", "Authorization", "Bearer never-log-this-token", []string{"never-log-this-token", "Bearer never-log-this-token"}},
		{"basic", "Authorization", "Basic " + basic, []string{basic, password, "username:" + password}},
		{"cookie", "Cookie", "session=private-session; csrf=private-csrf", []string{"private-session", "private-csrf"}},
		{"api key", "X-API-Key", password, []string{password}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &TargetAuth{CredentialID: "cred-test", Revision: 1, Origin: "https://example.com", Headers: map[string]string{tc.header: tc.value}}
			for _, secret := range append(tc.secrets, tc.value) {
				encoded, err := json.Marshal(secret)
				require.NoError(t, err)
				for _, reflection := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret), string(encoded[1 : len(encoded)-1]), strings.ReplaceAll(string(encoded[1:len(encoded)-1]), "/", `\/`)} {
					redacted := auth.Redact("prefix " + reflection + " suffix")
					assert.NotContains(t, redacted, reflection)
					assert.Contains(t, redacted, "[REDACTED]")
				}
			}
		})
	}
}

func TestTargetAuthSanitizesResponseBeforeExtraction(t *testing.T) {
	secret := "never-log-this-token"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "issued=new-server-session")
		w.Header().Set("X-Echo", "Bearer "+secret)
		w.Header().Set("Trailer", "X-Echo-Trailer")
		w.Write([]byte("<title>" + secret + "</title>body " + secret))
		w.Header().Set("X-Echo-Trailer", secret)
	}))
	defer srv.Close()
	h := trustedAuthHTTPX(t, srv, authForTest(srv.URL))
	resp, err := h.Get(srv.URL, nil)
	require.NoError(t, err)
	assert.Empty(t, resp.Header.Get("Set-Cookie"))
	assert.Empty(t, resp.GetHeader("Set-Cookie"))
	for _, text := range []string{resp.DataStr, string(resp.Data), resp.Title, resp.DumpResponse(), resp.Header.Get("X-Echo"), resp.Trailer.Get("X-Echo-Trailer")} {
		assert.NotContains(t, text, secret)
		assert.NotContains(t, text, "new-server-session")
		assert.Contains(t, text, "[REDACTED]")
	}
	assert.Equal(t, len([]rune(resp.DataStr)), resp.ContentLength)
}

func TestTargetAuthRedactsJSONEncoderVariants(t *testing.T) {
	auth := &TargetAuth{Headers: map[string]string{"X-API-Key": `私有口令/"<>&`}}
	for _, reflected := range []string{
		`私有口令/\"<>&`,
		`\u79c1\u6709\u53e3\u4ee4/\"<>&`,
		`\u79c1\u6709\u53e3\u4ee4\/\"\u003c\u003e\u0026`,
		`%e7%a7%81%e6%9c%89%e5%8f%a3%e4%bb%a4%2f%22%3c%3e%26`,
	} {
		assert.Equal(t, targetAuthRedacted, auth.Redact(reflected))
	}
}

func TestTargetAuthResponseReadFailureHasFixedError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.Write([]byte("Bearer never-log-this-token"))
	}))
	defer srv.Close()
	h := trustedAuthHTTPX(t, srv, authForTest(srv.URL))
	_, err := h.Get(srv.URL+"/never-log-this-token", nil)
	require.Error(t, err)
	assert.Equal(t, "authenticated target request failed", err.Error())
}

func TestTargetAuthRedactsHeaderWhitespaceNormalization(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Header.Get("X-API-Key")))
	}))
	defer srv.Close()
	auth := authForTest(srv.URL)
	auth.Headers = map[string]string{"X-API-Key": "  edge-trim-secret  "}
	h := trustedAuthHTTPX(t, srv, auth)
	resp, err := h.Get(srv.URL, nil)
	require.NoError(t, err)
	assert.Equal(t, targetAuthRedacted, resp.DataStr)
}
