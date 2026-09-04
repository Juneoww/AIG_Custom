package mcpconnections

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// policyResolver 让策略测试不依赖宿主 DNS，避免测试在网络环境改变时放宽 SSRF
// 边界。
type policyResolver func(context.Context, string) ([]net.IPAddr, error)

func (resolver policyResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return resolver(ctx, host)
}

func TestPolicyRejectsUnsafeServerURLsWithoutEchoingEndpoint(t *testing.T) {
	resolver := policyResolver(func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch host {
		case "allowed.example.test":
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.11")}}, nil
		case "loopback.example.test":
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		case "metadata.example.test":
			return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
		default:
			return nil, errors.New("unexpected test DNS query")
		}
	})
	policy, err := NewOutboundPolicy(OutboundPolicyConfig{
		AllowedCIDRs: []string{"203.0.113.0/24"},
		Resolver:     resolver,
	})
	require.NoError(t, err)

	tests := []struct {
		name string
		raw  string
	}{
		{name: "non HTTPS", raw: "http://allowed.example.test/secret"},
		{name: "userinfo", raw: "https://user:password@allowed.example.test/secret"},
		{name: "query", raw: "https://allowed.example.test/secret?token=query-secret"},
		{name: "fragment", raw: "https://allowed.example.test/secret#fragment-secret"},
		{name: "loopback DNS", raw: "https://loopback.example.test/private"},
		{name: "metadata DNS", raw: "https://metadata.example.test/latest/meta-data"},
		{name: "outside allowed CIDR", raw: "https://outside.example.test/private"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := policy.ValidateServerURL(context.Background(), test.raw)
			require.ErrorIs(t, err, ErrOutboundDenied)
			assert.NotContains(t, err.Error(), test.raw)
			assert.NotContains(t, err.Error(), "query-secret")
			assert.NotContains(t, err.Error(), "password")
		})
	}

	require.NoError(t, policy.ValidateServerURL(context.Background(), "https://allowed.example.test/mcp"))
}

type policyDialer struct {
	calls []string
	err   error
}

func (dialer *policyDialer) DialContext(_ context.Context, _ string, address string) (net.Conn, error) {
	dialer.calls = append(dialer.calls, address)
	return nil, dialer.err
}

func TestPolicyRechecksDNSImmediatelyBeforeDial(t *testing.T) {
	queries := 0
	resolver := policyResolver(func(_ context.Context, host string) ([]net.IPAddr, error) {
		assert.Equal(t, "rebind.example.test", host)
		queries++
		if queries == 1 {
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.25")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	})
	dialer := &policyDialer{}
	policy, err := NewOutboundPolicy(OutboundPolicyConfig{
		AllowedCIDRs:              []string{"203.0.113.0/24"},
		Resolver:                  resolver,
		Dialer:                    dialer,
		ControlledDialerAvailable: true,
	})
	require.NoError(t, err)
	require.NoError(t, policy.ValidateServerURL(context.Background(), "https://rebind.example.test/mcp"))

	_, err = policy.DialContext(context.Background(), "tcp", "rebind.example.test:443")
	require.ErrorIs(t, err, ErrOutboundDenied)
	assert.Empty(t, dialer.calls, "a rebound address must never reach the dialer")
}

func TestPolicyDialsValidatedAddressInsteadOfHostname(t *testing.T) {
	dialer := &policyDialer{err: errors.New("network is unavailable in the unit test")}
	policy, err := NewOutboundPolicy(OutboundPolicyConfig{
		AllowedCIDRs: []string{"203.0.113.0/24"},
		Resolver: policyResolver(func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.30")}}, nil
		}),
		Dialer:                    dialer,
		ControlledDialerAvailable: true,
	})
	require.NoError(t, err)

	_, err = policy.DialContext(context.Background(), "tcp", "safe.example.test:8443")
	require.ErrorIs(t, err, ErrOutboundDialFailed)
	assert.Equal(t, []string{"203.0.113.30:8443"}, dialer.calls)
	assert.NotContains(t, err.Error(), "safe.example.test")
}

func TestPolicyAppliesSeparateGitHostAndCIDRAllowlists(t *testing.T) {
	resolver := policyResolver(func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch host {
		case "git.example.test":
			return []net.IPAddr{{IP: net.ParseIP("198.51.100.7")}}, nil
		case "other.example.test":
			return []net.IPAddr{{IP: net.ParseIP("198.51.100.8")}}, nil
		default:
			return nil, errors.New("unexpected host")
		}
	})
	policy, err := NewOutboundPolicy(OutboundPolicyConfig{
		AllowedCIDRs:    []string{"203.0.113.0/24"},
		GitAllowedCIDRs: []string{"198.51.100.0/24"},
		GitAllowedHosts: []string{"git.example.test"},
		Resolver:        resolver,
	})
	require.NoError(t, err)

	require.NoError(t, policy.ValidateGitURL(context.Background(), "https://git.example.test/org/repo.git"))
	for _, raw := range []string{
		"http://git.example.test/org/repo.git",
		"https://other.example.test/org/repo.git",
		"https://git.example.test/org/repo.git?private-token=secret",
		"https://git.example.test/org/repo.git#secret",
	} {
		err := policy.ValidateGitURL(context.Background(), raw)
		require.ErrorIs(t, err, ErrGitOutboundDenied)
		assert.NotContains(t, err.Error(), raw)
		assert.NotContains(t, err.Error(), "private-token")
	}
}

func TestPolicyGitDialRejectsHostOutsideGitAllowlistBeforeDNSOrDial(t *testing.T) {
	resolverCalls := 0
	dialer := &policyDialer{}
	policy, err := NewOutboundPolicy(OutboundPolicyConfig{
		GitAllowedCIDRs: []string{"198.51.100.0/24"},
		GitAllowedHosts: []string{"git.example.test"},
		Resolver: policyResolver(func(context.Context, string) ([]net.IPAddr, error) {
			resolverCalls++
			return []net.IPAddr{{IP: net.ParseIP("198.51.100.9")}}, nil
		}),
		Dialer:                    dialer,
		ControlledDialerAvailable: true,
	})
	require.NoError(t, err)

	_, err = policy.DialGitContext(context.Background(), "tcp", "other.example.test:443")
	require.ErrorIs(t, err, ErrGitOutboundDenied)
	assert.Zero(t, resolverCalls, "an unapproved Git host must not reach DNS")
	assert.Empty(t, dialer.calls, "an unapproved Git host must not reach the dialer")
	assert.NotContains(t, err.Error(), "other.example.test")
}

func TestPolicyLoadsEnvironmentAllowlistsAndFailsClosedWithoutGateway(t *testing.T) {
	t.Setenv(EnvMCPOutboundAllowedCIDRs, "203.0.113.0/24, 2001:db8::/32")
	t.Setenv(EnvMCPGitOutboundAllowedCIDRs, "198.51.100.0/24")
	t.Setenv(EnvMCPGitAllowedHosts, "git.example.test")
	policy, err := LoadOutboundPolicyFromEnvironment(OutboundPolicyDependencies{
		Resolver: policyResolver(func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.8")}}, nil
		}),
	})
	require.NoError(t, err)
	assert.False(t, policy.ControlledDialerAvailable())
	assert.ErrorIs(t, policy.RequireControlledDialer(), ErrControlledEgressRequired)

	_, err = NewControlledHTTPClient(policy, ControlledHTTPClientConfig{RequestTimeout: time.Second})
	require.ErrorIs(t, err, ErrControlledEgressRequired)
}

func TestControlledHTTPClientDisablesProxyRedirectsAndInsecureTLS(t *testing.T) {
	policy, err := NewOutboundPolicy(OutboundPolicyConfig{
		AllowedCIDRs: []string{"203.0.113.0/24"},
		Resolver: policyResolver(func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.8")}}, nil
		}),
		Dialer:                    &policyDialer{},
		ControlledDialerAvailable: true,
	})
	require.NoError(t, err)

	client, err := NewControlledHTTPClient(policy, ControlledHTTPClientConfig{RequestTimeout: 2 * time.Second})
	require.NoError(t, err)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Nil(t, transport.Proxy, "the client must not consult environment proxy settings")
	require.NotNil(t, transport.DialContext)
	require.NotNil(t, transport.TLSClientConfig)
	assert.False(t, transport.TLSClientConfig.InsecureSkipVerify)

	request, err := http.NewRequest(http.MethodGet, "https://allowed.example.test/redirect", nil)
	require.NoError(t, err)
	redirectErr := client.CheckRedirect(request, nil)
	require.ErrorIs(t, redirectErr, ErrOutboundRedirectDenied)
	assert.NotContains(t, redirectErr.Error(), request.URL.String())
	assert.True(t, client.Timeout > 0)
}

func TestPolicyBlocksAllSpecialDNSAnswersAndGitRebinding(t *testing.T) {
	resolver := policyResolver(func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch host {
		case "linklocal.example.test":
			return []net.IPAddr{{IP: net.ParseIP("169.254.10.20")}}, nil
		case "multicast.example.test":
			return []net.IPAddr{{IP: net.ParseIP("224.0.0.1")}}, nil
		case "unspecified.example.test":
			return []net.IPAddr{{IP: net.ParseIP("0.0.0.0")}}, nil
		default:
			return nil, errors.New("unexpected host")
		}
	})
	policy, err := NewOutboundPolicy(OutboundPolicyConfig{
		AllowedCIDRs: []string{"0.0.0.0/0"},
		Resolver:     resolver,
	})
	require.NoError(t, err)
	for _, host := range []string{"linklocal.example.test", "multicast.example.test", "unspecified.example.test"} {
		require.ErrorIs(t, policy.ValidateServerURL(context.Background(), "https://"+host+"/mcp"), ErrOutboundDenied)
	}

	queries := 0
	dialer := &policyDialer{}
	gitPolicy, err := NewOutboundPolicy(OutboundPolicyConfig{
		GitAllowedCIDRs: []string{"198.51.100.0/24"},
		GitAllowedHosts: []string{"git.example.test"},
		Resolver: policyResolver(func(context.Context, string) ([]net.IPAddr, error) {
			queries++
			if queries == 1 {
				return []net.IPAddr{{IP: net.ParseIP("198.51.100.50")}}, nil
			}
			return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
		}),
		Dialer:                    dialer,
		ControlledDialerAvailable: true,
	})
	require.NoError(t, err)
	require.NoError(t, gitPolicy.ValidateGitURL(context.Background(), "https://git.example.test/org/repo.git"))
	_, err = gitPolicy.DialGitContext(context.Background(), "tcp", "git.example.test:443")
	require.ErrorIs(t, err, ErrGitOutboundDenied)
	assert.Empty(t, dialer.calls)
}
