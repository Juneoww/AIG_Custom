package mcpconnections

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	EnvMCPOutboundAllowedCIDRs    = "MCP_OUTBOUND_ALLOWED_CIDRS"
	EnvMCPGitOutboundAllowedCIDRs = "MCP_GIT_OUTBOUND_ALLOWED_CIDRS"
	EnvMCPGitAllowedHosts         = "MCP_GIT_ALLOWED_HOSTS"
)

var (
	// 所有对外暴露的策略错误均是固定文本。调用方不得用 URL、Header 或上游错误包装
	// 这些错误，以免错误响应和结构化日志成为秘密材料的旁路。
	ErrOutboundDenied           = errors.New("MCP 出站地址不符合安全策略")
	ErrGitOutboundDenied        = errors.New("MCP Git 出站地址不符合安全策略")
	ErrOutboundDialFailed       = errors.New("MCP 受控出站连接失败")
	ErrOutboundRedirectDenied   = errors.New("MCP 出站不允许重定向")
	ErrControlledEgressRequired = errors.New("MCP 出站必须通过受控网关")
	ErrOutboundPolicyInvalid    = errors.New("MCP 出站策略配置无效")
)

// HostResolver 是可注入的 DNS 边界。受控出站会在 URL 校验和每次拨号前分别
// 解析并验证，不能把一次 DNS 预检查误认为实际 egress 的完整防护。
type HostResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

// ContextDialer 由未来 mcpegress gateway 提供。Task 3 不创建普通 Agent 直连
// dialer；没有显式受控实现时所有实际探测、启用和任务选择均应 fail closed。
type ContextDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type OutboundPolicyConfig struct {
	AllowedCIDRs              []string
	GitAllowedCIDRs           []string
	GitAllowedHosts           []string
	Resolver                  HostResolver
	Dialer                    ContextDialer
	ControlledDialerAvailable bool
}

// OutboundPolicyDependencies 是从环境读取允许集时的运行时依赖。允许集只能来自
// 服务端环境，不接受任何浏览器、任务或连接配置的客户端覆盖。
type OutboundPolicyDependencies struct {
	Resolver                  HostResolver
	Dialer                    ContextDialer
	ControlledDialerAvailable bool
}

type OutboundPolicy struct {
	allowedCIDRs    []netip.Prefix
	gitAllowedCIDRs []netip.Prefix
	gitAllowedHosts map[string]struct{}
	resolver        HostResolver
	dialer          ContextDialer
	controlled      bool
}

func NewOutboundPolicy(config OutboundPolicyConfig) (*OutboundPolicy, error) {
	allowedCIDRs, err := parseCIDRs(config.AllowedCIDRs)
	if err != nil {
		return nil, ErrOutboundPolicyInvalid
	}
	gitAllowedCIDRs, err := parseCIDRs(config.GitAllowedCIDRs)
	if err != nil {
		return nil, ErrOutboundPolicyInvalid
	}
	gitAllowedHosts, err := parseHosts(config.GitAllowedHosts)
	if err != nil {
		return nil, ErrOutboundPolicyInvalid
	}
	resolver := config.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return &OutboundPolicy{
		allowedCIDRs:    allowedCIDRs,
		gitAllowedCIDRs: gitAllowedCIDRs,
		gitAllowedHosts: gitAllowedHosts,
		resolver:        resolver,
		dialer:          config.Dialer,
		controlled:      config.ControlledDialerAvailable && config.Dialer != nil,
	}, nil
}

func LoadOutboundPolicyFromEnvironment(dependencies OutboundPolicyDependencies) (*OutboundPolicy, error) {
	return NewOutboundPolicy(OutboundPolicyConfig{
		AllowedCIDRs:              splitEnvironmentList(EnvMCPOutboundAllowedCIDRs),
		GitAllowedCIDRs:           splitEnvironmentList(EnvMCPGitOutboundAllowedCIDRs),
		GitAllowedHosts:           splitEnvironmentList(EnvMCPGitAllowedHosts),
		Resolver:                  dependencies.Resolver,
		Dialer:                    dependencies.Dialer,
		ControlledDialerAvailable: dependencies.ControlledDialerAvailable,
	})
}

func (policy *OutboundPolicy) ControlledDialerAvailable() bool {
	return policy != nil && policy.controlled && policy.dialer != nil
}

func (policy *OutboundPolicy) RequireControlledDialer() error {
	if !policy.ControlledDialerAvailable() {
		return ErrControlledEgressRequired
	}
	return nil
}

// ValidateServerURL 只确认连接配置可被安全策略接受；真正拨号还会在
// DialContext 中重新解析和验证。返回值特意不携带 URL，防止调用方写入日志。
func (policy *OutboundPolicy) ValidateServerURL(ctx context.Context, raw string) error {
	parsed, err := parseHTTPSURL(raw)
	if err != nil {
		return ErrOutboundDenied
	}
	_, err = policy.resolveAndValidate(ctx, parsed.Hostname(), policy.allowedCIDRs, ErrOutboundDenied)
	if err != nil {
		return ErrOutboundDenied
	}
	return nil
}

// ValidateGitURL 仅验证未来 mcpegress Fetcher 所消费的 HTTPS Git 来源。Agent
// 进程不得 clone 或直接消费该 URL；Task 3 仅提供安全的 allowlist 判定边界。
func (policy *OutboundPolicy) ValidateGitURL(ctx context.Context, raw string) error {
	parsed, err := parseHTTPSURL(raw)
	if err != nil || policy == nil {
		return ErrGitOutboundDenied
	}
	host := canonicalHost(parsed.Hostname())
	if _, allowed := policy.gitAllowedHosts[host]; !allowed {
		return ErrGitOutboundDenied
	}
	_, err = policy.resolveAndValidate(ctx, host, policy.gitAllowedCIDRs, ErrGitOutboundDenied)
	if err != nil {
		return ErrGitOutboundDenied
	}
	return nil
}

// DialContext 是受控 HTTP transport 的唯一拨号路径。它在拨号前再次 DNS
// 校验并把已验证 IP 直接交给受控 dialer，避免 hostname 在校验后重新解析。
func (policy *OutboundPolicy) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if err := policy.RequireControlledDialer(); err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return nil, ErrOutboundDenied
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return nil, ErrOutboundDenied
	}

	// 第一次解析覆盖当前连接请求；第二次解析紧贴真实拨号，若 DNS 在两次之间
	// rebinding 到内部地址，则 resolveAndValidate 会在触达 dialer 前失败。
	if _, err := policy.resolveAndValidate(ctx, host, policy.allowedCIDRs, ErrOutboundDenied); err != nil {
		return nil, ErrOutboundDenied
	}
	addresses, err := policy.resolveAndValidate(ctx, host, policy.allowedCIDRs, ErrOutboundDenied)
	if err != nil {
		return nil, ErrOutboundDenied
	}
	for _, ip := range addresses {
		connection, dialErr := policy.dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return connection, nil
		}
	}
	return nil, ErrOutboundDialFailed
}

// DialGitContext 是未来受控 mcpegress Fetcher 的 Git 专用拨号边界。它不接受
// Agent 直连；每次真实拨号前都会再次检查 host allowlist 和 DNS/CIDR 结果，且
// 将已验证 IP 传给 gateway，避免 Git host 在验证和拨号之间发生 DNS rebinding。
func (policy *OutboundPolicy) DialGitContext(ctx context.Context, network, address string) (net.Conn, error) {
	if err := policy.RequireControlledDialer(); err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return nil, ErrGitOutboundDenied
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return nil, ErrGitOutboundDenied
	}
	host = canonicalHost(host)
	if _, allowed := policy.gitAllowedHosts[host]; !allowed {
		return nil, ErrGitOutboundDenied
	}

	// 第一次解析用于本次 Git 拨号请求的即时校验；第二次解析紧贴实际 egress，
	// 使 allowlisted 名称在两次查询间 rebinding 到内网地址时无法触达 dialer。
	if _, err := policy.resolveAndValidate(ctx, host, policy.gitAllowedCIDRs, ErrGitOutboundDenied); err != nil {
		return nil, ErrGitOutboundDenied
	}
	addresses, err := policy.resolveAndValidate(ctx, host, policy.gitAllowedCIDRs, ErrGitOutboundDenied)
	if err != nil {
		return nil, ErrGitOutboundDenied
	}
	for _, ip := range addresses {
		connection, dialErr := policy.dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return connection, nil
		}
	}
	return nil, ErrGitOutboundDenied
}

type ControlledHTTPClientConfig struct {
	ConnectTimeout        time.Duration
	RequestTimeout        time.Duration
	ResponseHeaderTimeout time.Duration
	TLSHandshakeTimeout   time.Duration
}

// NewControlledHTTPClient 明确关闭环境代理和重定向，并将所有连接导向策略的
// DialContext。它不能在缺少未来 gateway/dialer 的情况下退化到 net.Dialer。
func NewControlledHTTPClient(policy *OutboundPolicy, config ControlledHTTPClientConfig) (*http.Client, error) {
	if err := policy.RequireControlledDialer(); err != nil {
		return nil, err
	}
	config = config.normalized()
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			connectContext, cancel := context.WithTimeout(ctx, config.ConnectTimeout)
			defer cancel()
			return policy.DialContext(connectContext, network, address)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   config.TLSHandshakeTimeout,
		ResponseHeaderTimeout: config.ResponseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   config.RequestTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return ErrOutboundRedirectDenied
		},
	}, nil
}

func (config ControlledHTTPClientConfig) normalized() ControlledHTTPClientConfig {
	if config.ConnectTimeout <= 0 {
		config.ConnectTimeout = 5 * time.Second
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 15 * time.Second
	}
	if config.ResponseHeaderTimeout <= 0 {
		config.ResponseHeaderTimeout = 10 * time.Second
	}
	if config.TLSHandshakeTimeout <= 0 {
		config.TLSHandshakeTimeout = 5 * time.Second
	}
	return config
}

func parseHTTPSURL(raw string) (*url.URL, error) {
	// url.ParseRequestURI 会把 fragment 当作不属于 request-target 的部分处理，
	// 从而无法可靠地区分携带 fragment 的配置；这里必须保留完整 URL 后显式拒绝。
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, ErrOutboundDenied
	}
	host := canonicalHost(parsed.Hostname())
	if host == "" {
		return nil, ErrOutboundDenied
	}
	if port := parsed.Port(); port != "" {
		value, portErr := strconv.ParseUint(port, 10, 16)
		if portErr != nil || value == 0 {
			return nil, ErrOutboundDenied
		}
	}
	return parsed, nil
}

func (policy *OutboundPolicy) resolveAndValidate(ctx context.Context, host string, allowedCIDRs []netip.Prefix, denied error) ([]netip.Addr, error) {
	if policy == nil || policy.resolver == nil {
		return nil, denied
	}
	host = canonicalHost(host)
	if host == "" {
		return nil, denied
	}
	addresses, err := resolveAddresses(ctx, policy.resolver, host)
	if err != nil || len(addresses) == 0 {
		return nil, denied
	}
	validated := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !address.IsValid() || forbiddenIP(address) || !insideAllowedCIDRs(address, allowedCIDRs) {
			return nil, denied
		}
		if _, exists := seen[address]; !exists {
			seen[address] = struct{}{}
			validated = append(validated, address)
		}
	}
	return validated, nil
}

func resolveAddresses(ctx context.Context, resolver HostResolver, host string) ([]netip.Addr, error) {
	if literal, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{literal.Unmap()}, nil
	}
	resolved, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	addresses := make([]netip.Addr, 0, len(resolved))
	for _, item := range resolved {
		address, ok := netip.AddrFromSlice(item.IP)
		if !ok {
			return nil, ErrOutboundDenied
		}
		addresses = append(addresses, address.Unmap())
	}
	return addresses, nil
}

func forbiddenIP(address netip.Addr) bool {
	address = address.Unmap()
	if address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return true
	}
	// 除了 RFC 3927 的 link-local 元数据地址，还显式拒绝常见云厂商元数据别名。
	for _, metadata := range []netip.Addr{
		netip.MustParseAddr("169.254.169.254"),
		netip.MustParseAddr("100.100.100.200"),
		netip.MustParseAddr("100.100.100.100"),
		netip.MustParseAddr("fd00:ec2::254"),
	} {
		if address == metadata {
			return true
		}
	}
	return false
}

func insideAllowedCIDRs(address netip.Addr, allowedCIDRs []netip.Prefix) bool {
	for _, prefix := range allowedCIDRs {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func parseCIDRs(values []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil || !prefix.IsValid() {
			return nil, ErrOutboundPolicyInvalid
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func parseHosts(values []string) (map[string]struct{}, error) {
	hosts := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = canonicalHost(value)
		if value == "" || strings.ContainsAny(value, "/@?#") {
			return nil, ErrOutboundPolicyInvalid
		}
		hosts[value] = struct{}{}
	}
	return hosts, nil
}

func canonicalHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

func splitEnvironmentList(key string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}
