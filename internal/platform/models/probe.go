package models

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
)

// ProbeInput 独立承载瞬时凭据，所有格式化和 JSON 输出均隐藏输入。
type ProbeInput struct {
	ProviderModel string `json:"-"`
	BaseURL       string `json:"-"`
	token         string
}

func (ProbeInput) String() string                  { return "[model probe input redacted]" }
func (p ProbeInput) GoString() string              { return p.String() }
func (p ProbeInput) Format(s fmt.State, verb rune) { _, _ = io.WriteString(s, p.String()) }
func (ProbeInput) MarshalJSON() ([]byte, error)    { return []byte(`{}`), nil }
func (p *ProbeInput) UnmarshalJSON(data []byte) error {
	*p = ProbeInput{}
	if len(data) > 16384 {
		return ErrInvalid
	}
	var raw struct {
		ProviderModel string `json:"provider_model"`
		BaseURL       string `json:"base_url"`
		Token         string `json:"token"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&raw) != nil {
		return ErrInvalid
	}
	if len(raw.ProviderModel) > 512 || len(raw.BaseURL) > 2048 || len(raw.Token) > 8192 {
		return ErrInvalid
	}
	p.ProviderModel = strings.TrimSpace(raw.ProviderModel)
	p.BaseURL = strings.TrimSpace(raw.BaseURL)
	p.token = raw.Token
	return nil
}

type ProbeResult struct {
	Status    string `json:"status"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

var ErrProbeBusy = errors.New("model probe busy")
var ErrProbeUnavailable = errors.New("model probe unavailable")

func probeResult(code string) ProbeResult {
	status := "error"
	if code == "ok" {
		status = "success"
	}
	return ProbeResult{Status: status, Code: code, Message: code}
}
func validProbeToken(token string) bool {
	return strings.TrimSpace(token) != "" && token != MaskedToken && len(token) <= 8192 && !strings.ContainsAny(token, "\r\n\x00")
}
func normalizeProbeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 2048 {
		return "", ErrInvalid
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", ErrInvalid
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", ErrInvalid
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || strings.Contains(host, "%") || host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "metadata.google.internal" || host == "metadata.goog" {
		return "", ErrInvalid
	}
	if ip, err := netip.ParseAddr(host); err == nil && !allowedProbeIP(ip) {
		return "", ErrInvalid
	}
	port := u.Port()
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", ErrInvalid
		}
	}
	if port == "80" && u.Scheme == "http" || port == "443" && u.Scheme == "https" {
		port = ""
	}
	u.Host = host
	if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	}
	// 只修剪编码路径中的字面斜线，不能将 %2F 解码后当作路径分隔符删除。
	u.RawPath = strings.TrimRight(u.EscapedPath(), "/")
	u.Path, err = url.PathUnescape(u.RawPath)
	if err != nil {
		return "", ErrInvalid
	}
	return u.String(), nil
}
func allowedProbeIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && ip.IsGlobalUnicast() && ip.String() != "fd00:ec2::254" && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified() && ip.String() != "100.100.100.200" && ip.String() != "168.63.129.16"
}

// safeProbeDial 将 DNS 结果固定为数值地址，并核验实际连接，避免 DNS 重绑定。
func safeProbeDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrInvalid
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, ErrInvalid
	}
	for _, ip := range addresses {
		if !allowedProbeIP(ip) {
			return nil, ErrInvalid
		}
	}
	var last error
	for _, ip := range addresses {
		c, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err != nil {
			last = err
			continue
		}
		remote, _, err := net.SplitHostPort(c.RemoteAddr().String())
		actual, e := netip.ParseAddr(remote)
		if err != nil || e != nil || !allowedProbeIP(actual) || actual.Unmap() != ip.Unmap() {
			c.Close()
			return nil, ErrInvalid
		}
		return c, nil
	}
	return nil, last
}

type probeUserState struct {
	active bool
	next   time.Time
}
type probeRunner struct {
	client *http.Client
	mu     sync.Mutex
	users  map[string]probeUserState
	active int
	next   time.Time
}

func newProbeRunner() *probeRunner {
	transport := &http.Transport{Proxy: nil, DialContext: safeProbeDial, DisableKeepAlives: true, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second, MaxResponseHeaderBytes: 16384}
	return &probeRunner{client: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, users: make(map[string]probeUserState)}
}
func (p *probeRunner) acquire(user string) (func(), error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for id, state := range p.users {
		if !state.active && !now.Before(state.next) {
			delete(p.users, id)
		}
	}
	state := p.users[user]
	if state.active || now.Before(state.next) || p.active >= 8 || now.Before(p.next) || len(p.users) >= 1024 {
		return nil, ErrProbeBusy
	}
	p.users[user] = probeUserState{active: true, next: now.Add(5 * time.Second)}
	p.active++
	p.next = now.Add(100 * time.Millisecond)
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		state := p.users[user]
		state.active = false
		p.users[user] = state
		p.active--
	}, nil
}
func (p *probeRunner) run(ctx context.Context, input ProbeInput) (result ProbeResult) {
	start := time.Now()
	defer func() { result.ElapsedMS = time.Since(start).Milliseconds() }()
	base, err := normalizeProbeURL(input.BaseURL)
	if err != nil {
		return probeResult("invalid_config")
	}
	body, _ := json.Marshal(map[string]any{"model": input.ProviderModel, "messages": []map[string]string{{"role": "user", "content": "Reply with OK."}}, "max_tokens": 8, "stream": false})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return probeResult("invalid_config")
	}
	req.Header.Set("Authorization", "Bearer "+input.token)
	req.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &netErr) && netErr.Timeout() {
			return probeResult("timeout")
		}
		if errors.Is(err, ErrInvalid) {
			return probeResult("invalid_config")
		}
		return probeResult("network_error")
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode >= 300 && response.StatusCode < 400:
		return probeResult("redirect_blocked")
	case response.StatusCode == 401 || response.StatusCode == 403:
		return probeResult("authentication_failed")
	case response.StatusCode == 404:
		return probeResult("model_not_found")
	case response.StatusCode == 429:
		return probeResult("rate_limited")
	case response.StatusCode < 200 || response.StatusCode >= 300:
		return probeResult("upstream_error")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil {
		if ctx.Err() != nil {
			return probeResult("timeout")
		}
		return probeResult("network_error")
	}
	if len(data) > 65536 {
		return probeResult("invalid_response")
	}
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(data, &payload) != nil || len(payload.Choices) == 0 || strings.TrimSpace(payload.Choices[0].Message.Content) == "" {
		return probeResult("invalid_response")
	}
	return probeResult("ok")
}

// Probe 仅执行一次探测，不写入任何模型配置；审计预写失败时禁止发送凭据。
func (service *Service) Probe(ctx context.Context, subject identity.Subject, id string, input ProbeInput) (ProbeResult, error) {
	if subject.UserID == "" || subject.Role != identity.RoleUser && subject.Role != identity.RoleAdmin {
		return ProbeResult{}, ErrForbidden
	}
	if len(id) > 128 {
		return ProbeResult{}, ErrInvalid
	}
	if input.ProviderModel == "" || len(input.ProviderModel) > 512 {
		return ProbeResult{}, ErrInvalid
	}
	base, err := normalizeProbeURL(input.BaseURL)
	if err != nil {
		return ProbeResult{}, ErrInvalid
	}
	input.BaseURL = base
	if id != "" {
		model, err := service.repository.Get(ctx, id)
		if err != nil {
			return ProbeResult{}, err
		}
		if !canWrite(subject, model) {
			return ProbeResult{}, ErrForbidden
		}
		if input.token == "" {
			saved, err := normalizeProbeURL(model.BaseURL)
			if err != nil || saved != base {
				return ProbeResult{}, ErrInvalid
			}
			input.token, err = service.keyring.OpenToken(model)
			if err != nil {
				return ProbeResult{}, ErrProbeUnavailable
			}
		}
	}
	if !validProbeToken(input.token) {
		return ProbeResult{}, ErrInvalid
	}
	release, err := service.probe.acquire(subject.UserID)
	if err != nil {
		return ProbeResult{}, err
	}
	defer release()
	if ctx.Err() != nil {
		return probeResult("timeout"), nil
	}
	event := audit.EventInput{Action: audit.Action("model.probe"), ResourceType: "model", ResourceID: id, Outcome: audit.OutcomePending, Metadata: map[string]any{"phase": "requested"}}
	if service.audits == nil || service.audits.Record(ctx, subject, event) != nil {
		return ProbeResult{}, ErrProbeUnavailable
	}
	result := service.probe.run(ctx, input)
	event.Outcome = audit.OutcomeFailure
	if result.Code == "ok" {
		event.Outcome = audit.OutcomeSuccess
	}
	event.Metadata = map[string]any{"phase": "completed", "code": result.Code}
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if service.audits.Record(auditCtx, subject, event) != nil {
		return probeResult("unavailable"), nil
	}
	return result, nil
}
