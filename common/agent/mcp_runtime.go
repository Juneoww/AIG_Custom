// 功能：验证服务端签发的 MCP 运行时，并生成仅经 stdin 传输的最小配置。
package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var errMCPRuntime = errors.New("MCP runtime configuration invalid")
var mcpOpaqueID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)

type mcpPrivateModel struct {
	Model   string `json:"model"`
	Token   string `json:"token"`
	BaseURL string `json:"base_url"`
}
type mcpPrivateConfig struct {
	ProxyURL   string           `json:"mcp_proxy_url,omitempty"`
	Capability string           `json:"task_capability,omitempty"`
	Transport  string           `json:"effective_transport,omitempty"`
	Model      *mcpPrivateModel `json:"model,omitempty"`
	ArchiveRef string           `json:"archive_ref,omitempty"`
}
type mcpTaskRuntime struct {
	mcpPrivateConfig
	SourceKind        string          `json:"source_kind"`
	Authorized        bool            `json:"authorization_confirmed"`
	ModelID           json.RawMessage `json:"model_id"`
	Thread            json.RawMessage `json:"thread"`
	ConnectionID      json.RawMessage `json:"connection_config_id"`
	ConnectionVersion json.RawMessage `json:"connection_config_version"`
}

func mcpServerURL(server string) (*url.URL, error) {
	if !strings.Contains(server, "://") {
		server = "http://" + server
	}
	u, err := url.Parse(server)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errMCPRuntime
	}
	u.Path = ""
	return u, nil
}

func parseMCPRuntime(server string, request TaskRequest) (mcpTaskRuntime, error) {
	var p mcpTaskRuntime
	if request.Content != "" || len(request.Attachments) != 0 || len(request.Params) == 0 || len(request.Params) > 65536 || bytes.Equal(bytes.TrimSpace(request.Params), []byte("null")) {
		return p, errMCPRuntime
	}
	dec := json.NewDecoder(bytes.NewReader(request.Params))
	dec.DisallowUnknownFields()
	if dec.Decode(&p) != nil || dec.Decode(&struct{}{}) != io.EOF {
		return p, errMCPRuntime
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(request.Params, &fields) != nil {
		return p, errMCPRuntime
	}
	if _, provided := fields["model"]; provided {
		if p.Model == nil || p.Model.Model == "" || p.Model.Token == "" || p.Model.BaseURL == "" {
			return p, errMCPRuntime
		}
		modelURL, err := url.Parse(p.Model.BaseURL)
		if err != nil || modelURL.Host == "" || modelURL.User != nil || (modelURL.Scheme != "http" && modelURL.Scheme != "https") {
			return p, errMCPRuntime
		}
	}
	switch p.SourceKind {
	case "service":
		trusted, err := mcpServerURL(server)
		if err != nil {
			return p, errMCPRuntime
		}
		gateway, err := url.Parse(p.ProxyURL)
		if err != nil || gateway.Scheme != trusted.Scheme || gateway.Host != trusted.Host || gateway.User != nil || gateway.RawQuery != "" || gateway.ForceQuery || gateway.Fragment != "" || gateway.RawPath != "" || !strings.HasPrefix(gateway.Path, "/api/internal/mcp-egress/") || !mcpOpaqueID.MatchString(strings.TrimPrefix(gateway.Path, "/api/internal/mcp-egress/")) || p.ArchiveRef != "" || !p.Authorized || p.Capability == "" || len(p.Capability) > 8192 || strings.ContainsAny(p.Capability, "\r\n") {
			return p, errMCPRuntime
		}
		// 服务端枚举 http 在私有协议中固定为 Python MCP SDK 的 streamable-http。
		if p.Transport == "http" {
			p.Transport = "streamable-http"
		}
		if p.Transport != "streamable-http" && p.Transport != "sse" {
			return p, errMCPRuntime
		}
	case "repository":
		if !strings.HasPrefix(p.ArchiveRef, "archive:") || !mcpOpaqueID.MatchString(strings.TrimPrefix(p.ArchiveRef, "archive:")) || p.ProxyURL != "" || p.Capability != "" || p.Transport != "" {
			return p, errMCPRuntime
		}
	default:
		return p, errMCPRuntime
	}
	return p, nil
}

func mcpRuntimeRedactor(p mcpTaskRuntime) func(string) string {
	values := []string{p.ProxyURL, p.Capability, p.ArchiveRef, os.Getenv("AIG_AGENT_TOKEN")}
	if p.Model != nil {
		values = append(values, p.Model.Token, p.Model.BaseURL)
	}
	var pairs []string
	for _, v := range values {
		if v != "" {
			pairs = append(pairs, v, "[REDACTED]")
			encoded, _ := json.Marshal(v)
			if string(encoded[1:len(encoded)-1]) != v {
				pairs = append(pairs, string(encoded[1:len(encoded)-1]), "[REDACTED]")
			}
		}
	}
	replace := strings.NewReplacer(pairs...)
	return func(line string) string {
		// JSON 转义必须先解码，否则 ParseStdoutLine 会在脱敏之后还原原始秘密。
		var value any
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		if decoder.Decode(&value) == nil && decoder.Decode(&struct{}{}) == io.EOF {
			var clean func(any) any
			clean = func(v any) any {
				switch item := v.(type) {
				case string:
					return replace.Replace(item)
				case []any:
					for i := range item {
						item[i] = clean(item[i])
					}
					return item
				case map[string]any:
					out := make(map[string]any, len(item))
					for k, x := range item {
						out[replace.Replace(k)] = clean(x)
					}
					return out
				default:
					return item
				}
			}
			if encoded, err := json.Marshal(clean(value)); err == nil {
				return string(encoded)
			}
		}
		return replace.Replace(line)
	}
}
