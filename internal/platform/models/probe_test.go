package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/require"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestUpdateRejectsNewURLWithOldToken(t *testing.T) {
	service := NewService(NewMemoryRepository(), mustTestKeyring(t, "current", bytesOf(1), nil), audit.NewService(audit.NewMemoryRepository()))
	subject := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	view, err := service.Create(context.Background(), subject, CreateInput{Name: "test", Scope: ScopePrivate, BaseURL: "https://api.example/v1", Token: "old-secret"})
	require.NoError(t, err)
	for _, token := range []*string{nil, ptrProbe(""), ptrProbe(MaskedToken)} {
		_, err = service.Update(context.Background(), subject, view.ID, UpdateInput{BaseURL: ptrProbe("https://api.example/other"), Token: token})
		require.ErrorIs(t, err, ErrInvalid)
	}
}
func ptrProbe(s string) *string { return &s }

func TestProbeURLAndResponse(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1/v1", "http://169.254.169.254", "http://[::1]", "http://0.0.0.0", "http://224.0.0.1", "https://api.example/v1?token=secret", "https://u:p@api.example", "https://api.example/#fragment"} {
		_, err := normalizeProbeURL(raw)
		require.Error(t, err, raw)
	}
	a, err := normalizeProbeURL("HTTPS://API.EXAMPLE:443/v1/")
	require.NoError(t, err)
	b, err := normalizeProbeURL("https://api.example/v1")
	require.NoError(t, err)
	require.Equal(t, a, b)
	for _, tc := range []struct {
		status     int
		body, code string
	}{
		{200, `{"choices":[{"message":{"content":"OK"}}]}`, "ok"},
		{200, `{"choices":[{"message":{"content":""}}]}`, "invalid_response"},
		{200, `{"error":"secret"}`, "invalid_response"},
		{401, `secret`, "authentication_failed"}, {403, `secret`, "authentication_failed"},
		{404, `secret`, "model_not_found"}, {429, `secret`, "rate_limited"}, {500, `secret`, "upstream_error"}, {302, `secret`, "redirect_blocked"},
	} {
		p := newProbeRunner()
		p.client.Transport = probeRoundTrip(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
			require.Equal(t, "/v1/chat/completions", r.URL.Path)
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
		})
		result := p.run(context.Background(), ProbeInput{ProviderModel: "test", BaseURL: "https://api.example/v1", token: "secret"})
		require.Equal(t, tc.code, result.Code)
		require.NotContains(t, result.Message, "secret")
	}
}

type probeRoundTrip func(*http.Request) (*http.Response, error)

func (f probeRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProbeInputRedactsAndBounds(t *testing.T) {
	var input ProbeInput
	require.NoError(t, json.Unmarshal([]byte(`{"provider_model":"m","base_url":"https://api.example/v1","token":"secret-canary"}`), &input))
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		require.NotContains(t, fmt.Sprintf(format, input), "secret-canary")
		require.NotContains(t, fmt.Sprintf(format, &input), "secret-canary")
	}
	data, err := json.Marshal(input)
	require.NoError(t, err)
	require.NotContains(t, string(data), "secret-canary")
	for _, raw := range []string{`{"token":"` + strings.Repeat("a", 8193) + `"}`, `{"unknown":"secret-canary"}`, `{"token":123}`} {
		require.ErrorIs(t, json.Unmarshal([]byte(raw), &input), ErrInvalid)
	}
}
func TestProbeAuthorizationAuditAndNoMutation(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	audits := audit.NewMemoryRepository()
	service := NewService(repository, mustTestKeyring(t, "current", bytesOf(1), nil), audit.NewService(audits))
	alice := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	view, err := service.Create(ctx, alice, CreateInput{Name: "m", Scope: ScopePrivate, BaseURL: "https://api.example/v1", Token: "saved-secret"})
	require.NoError(t, err)
	before, err := repository.Get(ctx, view.ID)
	require.NoError(t, err)
	calls := 0
	service.probe.client.Transport = probeRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "Bearer saved-secret", r.Header.Get("Authorization"))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"OK"}}]}`))}, nil
	})
	input := ProbeInput{ProviderModel: "m", BaseURL: "https://api.example/v1/"}
	for _, subject := range []identity.Subject{{UserID: "bob", Role: identity.RoleUser}, {UserID: "audit", Role: identity.RoleAuditor}, {UserID: "admin", Role: identity.RoleAdmin}} {
		_, err := service.Probe(ctx, subject, view.ID, input)
		require.ErrorIs(t, err, ErrForbidden)
	}
	input.BaseURL = "https://api.example/v2"
	_, err = service.Probe(ctx, alice, view.ID, input)
	require.ErrorIs(t, err, ErrInvalid)
	require.Zero(t, calls)
	input.BaseURL = "https://api.example:443/v1/"
	result, err := service.Probe(ctx, alice, view.ID, input)
	require.NoError(t, err)
	require.Equal(t, "ok", result.Code)
	after, err := repository.Get(ctx, view.ID)
	require.NoError(t, err)
	require.Equal(t, before, after)
	_, err = service.Probe(ctx, alice, view.ID, input)
	require.ErrorIs(t, err, ErrProbeBusy)
	require.Equal(t, 1, calls)
	events, err := audits.List(ctx, audit.Filter{Action: audit.Action("model.probe")})
	require.NoError(t, err)
	require.Len(t, events, 2)
	data, err := json.Marshal(events)
	require.NoError(t, err)
	require.NotContains(t, string(data), "saved-secret")
	require.NotContains(t, string(data), "api.example")
	service.probe = newProbeRunner()
	service.audits = &failingModelRecorder{failOn: 1}
	_, err = service.Probe(ctx, alice, view.ID, input)
	require.ErrorIs(t, err, ErrProbeUnavailable)
	require.Equal(t, 1, calls)
}
func TestProbeFailureSafetyAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		transport probeRoundTrip
		code      string
	}{
		{"timeout", func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded }, "timeout"},
		{"cancel", func(*http.Request) (*http.Response, error) { return nil, context.Canceled }, "timeout"},
		{"network", func(*http.Request) (*http.Response, error) { return nil, errors.New("secret-canary") }, "network_error"},
		{"oversize", func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 65537)))}, nil
		}, "invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newProbeRunner()
			p.client.Transport = tc.transport
			result := p.run(context.Background(), ProbeInput{ProviderModel: "m", BaseURL: "https://api.example", token: "secret-canary"})
			require.Equal(t, tc.code, result.Code)
			require.NotContains(t, result.Message, "secret-canary")
		})
	}
}
func TestProbeTransportDefaultsAndUnsafeAddresses(t *testing.T) {
	p := newProbeRunner()
	transport := p.client.Transport.(*http.Transport)
	require.Nil(t, transport.Proxy)
	require.Equal(t, 30*time.Second, p.client.Timeout)
	require.Nil(t, transport.TLSClientConfig)
	require.ErrorIs(t, p.client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
	for _, address := range []string{"127.0.0.1", "::1", "169.254.169.254", "100.100.100.200", "168.63.129.16", "fd00:ec2::254", "::ffff:127.0.0.1", "255.255.255.255"} {
		require.False(t, allowedProbeIP(netip.MustParseAddr(address)), address)
	}
	require.True(t, allowedProbeIP(netip.MustParseAddr("10.1.2.3")))
	_, err := safeProbeDial(context.Background(), "tcp", "127.0.0.1:80")
	require.ErrorIs(t, err, ErrInvalid)
}

func TestProbeTLSAndRedirectNeverFollow(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls++
		w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	p := newProbeRunner()
	p.client.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(source.URL, "http://"))
	}
	result := p.run(context.Background(), ProbeInput{ProviderModel: "m", BaseURL: "http://api.example", token: "secret"})
	require.Equal(t, "redirect_blocked", result.Code)
	require.Zero(t, targetCalls)
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("untrusted TLS request must never reach handler")
	}))
	defer tls.Close()
	p = newProbeRunner()
	p.client.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(tls.URL, "https://"))
	}
	result = p.run(context.Background(), ProbeInput{ProviderModel: "m", BaseURL: "https://api.example", token: "secret"})
	require.Equal(t, "network_error", result.Code)
}
func TestProbeLimiterBoundedAndReleases(t *testing.T) {
	p := newProbeRunner()
	release, err := p.acquire("alice")
	require.NoError(t, err)
	_, err = p.acquire("alice")
	require.ErrorIs(t, err, ErrProbeBusy)
	release()
	require.Zero(t, p.active)
	_, err = p.acquire("alice")
	require.ErrorIs(t, err, ErrProbeBusy)
	p.next = time.Time{}
	p.users["alice"] = probeUserState{next: time.Now().Add(-time.Second)}
	release, err = p.acquire("alice")
	require.NoError(t, err)
	release()
	p.next = time.Time{}
	p.active = 8
	_, err = p.acquire("bob")
	require.ErrorIs(t, err, ErrProbeBusy)
	p.active = 0
	for n := 0; n < 1024; n++ {
		p.users[fmt.Sprint(n)] = probeUserState{next: time.Now().Add(time.Minute)}
	}
	_, err = p.acquire("bob")
	require.ErrorIs(t, err, ErrProbeBusy)
}
func TestUpdateChangedURLRequiresActualNewCredential(t *testing.T) {
	service := NewService(NewMemoryRepository(), mustTestKeyring(t, "current", bytesOf(1), nil), audit.NewService(audit.NewMemoryRepository()))
	subject := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	ctx := context.Background()
	view, err := service.Create(ctx, subject, CreateInput{Name: "m", Scope: ScopePrivate, BaseURL: "https://api.example/v1", Token: "secret"})
	require.NoError(t, err)
	_, err = service.Update(ctx, subject, view.ID, UpdateInput{BaseURL: ptrProbe("https://API.EXAMPLE:443/v1/")})
	require.NoError(t, err)
	for _, token := range []string{"   ", "bad\nheader", MaskedToken} {
		_, err = service.Update(ctx, subject, view.ID, UpdateInput{BaseURL: ptrProbe("https://api.example/other"), Token: ptrProbe(token)})
		require.ErrorIs(t, err, ErrInvalid)
	}
	_, err = service.Update(ctx, subject, view.ID, UpdateInput{BaseURL: ptrProbe("https://api.example/other"), Token: ptrProbe("new-secret")})
	require.NoError(t, err)
}

func TestProbeCanceledServiceDoesNotSendAndAuditCompletionFailureIsSafe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service := NewService(NewMemoryRepository(), nil, audit.NewService(audit.NewMemoryRepository()))
	calls := 0
	service.probe.client.Transport = probeRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("secret"))}, nil
	})
	subject := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	input := ProbeInput{ProviderModel: "m", BaseURL: "https://api.example/v1", token: "secret"}
	result, err := service.Probe(ctx, subject, "", input)
	require.NoError(t, err)
	require.Equal(t, "timeout", result.Code)
	require.Zero(t, calls)
	service.probe.users = make(map[string]probeUserState)
	service.probe.next = time.Time{}
	service.audits = &failingModelRecorder{delegate: audit.NewService(audit.NewMemoryRepository()), failOn: 2}
	result, err = service.Probe(context.Background(), subject, "", input)
	require.NoError(t, err)
	require.Equal(t, "unavailable", result.Code)
	require.Equal(t, 1, calls)
}

func TestProbeEncodedTrailingSlashPreservesCredentialBoundary(t *testing.T) {
	encoded, err := normalizeProbeURL("https://api.example/v1%2F/")
	require.NoError(t, err)
	require.Equal(t, "https://api.example/v1%2F", encoded)
	plain, err := normalizeProbeURL("https://api.example/v1/")
	require.NoError(t, err)
	require.NotEqual(t, plain, encoded)
	service := NewService(NewMemoryRepository(), mustTestKeyring(t, "current", bytesOf(1), nil), audit.NewService(audit.NewMemoryRepository()))
	ctx := context.Background()
	subject := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	view, err := service.Create(ctx, subject, CreateInput{Name: "m", Scope: ScopePrivate, BaseURL: "https://api.example/v1%2F", Token: "secret"})
	require.NoError(t, err)
	_, err = service.Update(ctx, subject, view.ID, UpdateInput{BaseURL: ptrProbe("https://api.example/v1")})
	require.ErrorIs(t, err, ErrInvalid)
	_, err = service.Probe(ctx, subject, view.ID, ProbeInput{ProviderModel: "m", BaseURL: "https://api.example/v1"})
	require.ErrorIs(t, err, ErrInvalid)
	p := newProbeRunner()
	p.client.Transport = probeRoundTrip(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "/v1%2F/chat/completions", r.URL.EscapedPath())
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"OK"}}]}`))}, nil
	})
	result := p.run(ctx, ProbeInput{ProviderModel: "m", BaseURL: encoded, token: "secret"})
	require.Equal(t, "ok", result.Code)
}
