package httpx

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

const targetAuthRedacted = "[REDACTED]"

var (
	errTargetAuthOrigin  = errors.New("invalid target credential origin")
	errTargetAuthInvalid = errors.New("invalid target authentication")
	errTargetAuthScope   = errors.New("target authentication requires same-origin requests with an allowed scheme")
	errTargetAuthRequest = errors.New("authenticated target request failed")
	errTargetAuthUnsafe  = errors.New("target authentication does not allow unsafe requests")
)

// TargetAuth 仅用于私有执行通道；常规任务和配置 JSON 必须显式排除此字段。
type TargetAuth struct {
	CredentialID      string            `json:"credential_id"`
	Revision          int64             `json:"revision"`
	Origin            string            `json:"origin"`
	AllowInsecureHTTP bool              `json:"allow_insecure_http"`
	Headers           map[string]string `json:"headers"`
}

// String 避免格式化日志输出凭据；私有 assignment 仍可正常 JSON 编解码。
func (TargetAuth) String() string { return targetAuthRedacted }

// GoString 同时保护 %#v 形式的调试输出。
func (TargetAuth) GoString() string { return targetAuthRedacted }

func containsControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}

// NormalizeTargetOrigin 规范化协议、主机和有效端口；HTTP 需要显式许可，缺省仅允许 HTTPS。
func NormalizeTargetOrigin(raw string, allowHTTP ...bool) (string, error) {
	u, origin, err := parseTargetURL(raw, len(allowHTTP) > 0 && allowHTTP[0])
	if err != nil || (u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery {
		return "", errTargetAuthOrigin
	}
	return origin, nil
}

func parseTargetURL(raw string, allowHTTP bool) (*url.URL, string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || containsControl(raw) || strings.ContainsAny(raw, "#\\") {
		return nil, "", errTargetAuthOrigin
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, "", errTargetAuthOrigin
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "https" && (scheme != "http" || !allowHTTP)) || u.Opaque != "" || u.User != nil || u.Host == "" || containsControl(u.Path) {
		return nil, "", errTargetAuthOrigin
	}
	if decoded, err := url.QueryUnescape(u.RawQuery); err != nil || containsControl(decoded) {
		return nil, "", errTargetAuthOrigin
	}
	host := strings.ToLower(u.Hostname())
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" {
			return nil, "", errTargetAuthOrigin
		}
		host = addr.String()
		if addr.Is6() {
			if !strings.HasPrefix(u.Host, "[") {
				return nil, "", errTargetAuthOrigin
			}
			host = "[" + host + "]"
		} else if strings.HasPrefix(u.Host, "[") {
			return nil, "", errTargetAuthOrigin
		}
	} else if strings.HasPrefix(u.Host, "[") || !validTargetDNSName(host) {
		return nil, "", errTargetAuthOrigin
	}
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return nil, "", errTargetAuthOrigin
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, "", errTargetAuthOrigin
		}
		defaultPort := 443
		if scheme == "http" {
			defaultPort = 80
		}
		if n != defaultPort {
			host += ":" + strconv.Itoa(n)
		}
	}
	return u, scheme + "://" + host, nil
}

func validTargetDNSName(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	last := labels[len(labels)-1]
	// 拒绝旧式十进制、八进制和十六进制 IP 写法，防止 DNS/URL 解析差异。
	if strings.Trim(last, "0123456789") == "" {
		return false
	}
	if strings.HasPrefix(last, "0x") {
		if len(last) > 2 && strings.Trim(last[2:], "0123456789abcdef") == "" {
			return false
		}
	}
	return true
}

// ValidateTargetURLs 要求完整 URL 严格同协议、同主机和同有效端口；HTTP 需要显式许可。
func ValidateTargetURLs(origin string, targets []string, allowHTTP ...bool) error {
	normalized, err := NormalizeTargetOrigin(origin, allowHTTP...)
	if err != nil || len(targets) == 0 {
		return errTargetAuthScope
	}
	for _, target := range targets {
		_, targetOrigin, err := parseTargetURL(target, len(allowHTTP) > 0 && allowHTTP[0])
		if err != nil || targetOrigin != normalized {
			return errTargetAuthScope
		}
	}
	return nil
}

// ValidCredentialHeader 允许认证头和普通 API key 头，拒绝路由、代理与传输控制头。
func ValidCredentialHeader(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && !strings.ContainsRune("!#$%&'*+-.^_`|~", c) {
			return false
		}
	}
	normalized := strings.ToLower(name)
	if normalized == "authorization" || normalized == "cookie" {
		return true
	}
	for _, prefix := range []string{"proxy-", "sec-", "content-", "accept", "if-", "access-control-", "forwarded-", "x-forwarded-", "x-original-", "x-rewrite-", "x-http-method", "x-method-", "x-accel-"} {
		if strings.HasPrefix(normalized, prefix) {
			return false
		}
	}
	switch normalized {
	case "host", "connection", "keep-alive", "upgrade", "te", "trailer", "transfer-encoding", "expect",
		"forwarded", "via", "location", "refresh", "set-cookie", "set-cookie2", "user-agent", "useragent",
		"origin", "referer", "referrer", "range", "cache-control", "pragma", "date", "warning", "allow",
		"server", "retry-after", "www-authenticate", "authentication-info", "alt-svc", "early-data", "max-forwards",
		"x-host", "x-real-ip", "x-client-ip", "true-client-ip", "proxy", "http2-settings",
		"x-http-host-override", "x-url-scheme", "front-end-https":
		return false
	}
	return true
}

// Validate 校验来自私有 assignment 的凭据结构，错误中不包含任何原始输入。
func (a *TargetAuth) Validate() error {
	if a == nil || strings.TrimSpace(a.CredentialID) == "" || len(a.CredentialID) > 128 || containsControl(a.CredentialID) || a.Revision < 1 || len(a.Headers) != 1 {
		return errTargetAuthInvalid
	}
	if _, err := NormalizeTargetOrigin(a.Origin, a.AllowInsecureHTTP); err != nil {
		return errTargetAuthInvalid
	}
	for name, value := range a.Headers {
		if !ValidCredentialHeader(name) || strings.TrimSpace(value) == "" || len(value) > 16*1024 || containsControl(value) {
			return errTargetAuthInvalid
		}
	}
	return nil
}

func (a *TargetAuth) clone() *TargetAuth {
	copy := *a
	copy.Headers = make(map[string]string, len(a.Headers))
	for name, value := range a.Headers {
		copy.Headers[name] = value
	}
	copy.Origin, _ = NormalizeTargetOrigin(a.Origin, a.AllowInsecureHTTP)
	return &copy
}

// Redact 替换已知认证值及其常见 URL、JSON、HTML 反射，不依赖下游日志策略。
func (a *TargetAuth) Redact(text string) string {
	if a == nil || text == "" {
		return text
	}
	secrets := make(map[string]struct{})
	add := func(value string) {
		if value != "" {
			secrets[value] = struct{}{}
		}
	}
	for name, value := range a.Headers {
		add(value)
		add(strings.TrimSpace(value))
		if strings.EqualFold(name, "authorization") {
			parts := strings.SplitN(strings.TrimSpace(value), " ", 2)
			if len(parts) == 2 {
				material := strings.TrimSpace(parts[1])
				add(material)
				if strings.EqualFold(parts[0], "basic") {
					for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
						if decoded, err := encoding.DecodeString(material); err == nil {
							add(string(decoded))
							if _, password, ok := strings.Cut(string(decoded), ":"); ok {
								add(password)
							}
							break
						}
					}
				}
			}
		}
		if strings.EqualFold(name, "cookie") {
			for _, cookie := range strings.Split(value, ";") {
				if _, material, ok := strings.Cut(cookie, "="); ok {
					add(strings.Trim(strings.TrimSpace(material), `"`))
				}
			}
		}
	}
	variants := make(map[string]struct{}, len(secrets)*8)
	for secret := range secrets {
		encoded, _ := json.Marshal(secret)
		jsonEscaped := string(encoded[1 : len(encoded)-1])
		var buffer bytes.Buffer
		encoder := json.NewEncoder(&buffer)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(secret)
		jsonNoHTML := buffer.String()[1 : buffer.Len()-2]
		values := []string{secret, url.QueryEscape(secret), url.PathEscape(secret), strings.ReplaceAll(url.QueryEscape(secret), "+", "%20"), html.EscapeString(secret)}
		for _, escaped := range []string{jsonEscaped, jsonNoHTML, asciiJSON(jsonEscaped), asciiJSON(jsonNoHTML)} {
			values = append(values, escaped, strings.ReplaceAll(escaped, "/", `\/`))
		}
		for _, value := range values {
			variants[value] = struct{}{}
			variants[lowerPercentEscapes(value)] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(variants))
	for value := range variants {
		ordered = append(ordered, value)
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	pairs := make([]string, 0, len(ordered)*2)
	for _, value := range ordered {
		pairs = append(pairs, value, targetAuthRedacted)
	}
	return strings.NewReplacer(pairs...).Replace(text)
}

// asciiJSON 覆盖常见 JSON 编码器的 ensure_ascii 输出（包括 UTF-16 代理对）。
func asciiJSON(value string) string {
	var result strings.Builder
	for _, r := range value {
		if r < 0x80 {
			result.WriteRune(r)
		} else if r <= 0xffff {
			fmt.Fprintf(&result, `\u%04x`, r)
		} else {
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&result, `\u%04x\u%04x`, hi, lo)
		}
	}
	return result.String()
}

func lowerPercentEscapes(value string) string {
	result := []byte(value)
	for i := 0; i+2 < len(result); i++ {
		if result[i] == '%' {
			for j := i + 1; j <= i+2; j++ {
				if result[j] >= 'A' && result[j] <= 'F' {
					result[j] += 'a' - 'A'
				}
			}
			i += 2
		}
	}
	return string(result)
}
