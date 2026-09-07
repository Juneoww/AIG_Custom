package mcpconnections

import (
	"encoding/json"
	"time"
)

// CreateConnectionInput 是仅用于受保护管理写入路径的输入模型。它没有 proxy、
// skip TLS、allowlist、OAuth/mTLS 私钥或 Git 凭据等字段；这些能力不能由客户端
// 借输入绕过服务端出站策略。
type CreateConnectionInput struct {
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Scope          Scope          `json:"scope"`
	Transport      Transport      `json:"transport"`
	ServerURL      string         `json:"server_url"`
	Authentication Authentication `json:"authentication"`
	Headers        []Header       `json:"headers,omitempty"`
}

// MarshalJSON 将领域写入输入作为日志或错误上下文序列化时保持 fail closed。HTTP
// handler 可以直接解码该输入；后续若需要发送到另一个受保护写入边界，必须使用
// 只在该边界内部定义的 wire 类型，而不能借用这个安全投影传递秘密材料。
func (input CreateConnectionInput) MarshalJSON() ([]byte, error) {
	serverURL := input.ServerURL
	if serverURL != "" {
		serverURL = MaskedSecret
	}
	return json.Marshal(struct {
		Name           string         `json:"name"`
		Description    string         `json:"description"`
		Scope          Scope          `json:"scope"`
		Transport      Transport      `json:"transport"`
		ServerURL      string         `json:"server_url"`
		Authentication Authentication `json:"authentication"`
		Headers        []Header       `json:"headers,omitempty"`
	}{
		Name:           input.Name,
		Description:    input.Description,
		Scope:          input.Scope,
		Transport:      input.Transport,
		ServerURL:      serverURL,
		Authentication: input.Authentication,
		Headers:        input.Headers,
	})
}

// ConnectionSummary 是所有浏览器列表和任务选项可复用的安全投影。它没有
// endpoint、认证 Header 名称/值、cookie 或 token。
type ConnectionSummary struct {
	ID                 string             `json:"id"`
	Name               string             `json:"name"`
	Description        string             `json:"description"`
	Scope              Scope              `json:"scope"`
	CurrentVersion     int                `json:"current_version"`
	Enabled            bool               `json:"enabled"`
	Transport          Transport          `json:"transport"`
	DetectedTransport  Transport          `json:"detected_transport,omitempty"`
	ProbeStatus        ProbeStatus        `json:"probe_status"`
	ResourceRevision   string             `json:"resource_revision"`
	AuthenticationKind AuthenticationKind `json:"authentication_kind"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

// ConnectionManagementDetail 给有管理权限的 owner 或管理员提供最小状态例外。
// 只表达材料是否已配置，不能将真实 URL、Header 名称或任何秘密重投影到响应。
type ConnectionManagementDetail struct {
	ConnectionSummary
	EndpointConfigured       bool               `json:"endpoint_configured"`
	AuthenticationConfigured bool               `json:"authentication_configured"`
	AuthenticationKind       AuthenticationKind `json:"authentication_kind,omitempty"`
	CustomHeadersConfigured  bool               `json:"custom_headers_configured"`
}

// TaskConnectionOption 供未来 Task 4 创建任务时读取。它只能引用已经保存且安全
// 可选择的版本，不能携带使 Agent 绕过 gateway 的 URL 或认证信息。
type TaskConnectionOption struct {
	ConnectionID       string             `json:"connection_id"`
	ConnectionVersion  int                `json:"connection_version"`
	Name               string             `json:"name"`
	Scope              Scope              `json:"scope"`
	Transport          Transport          `json:"transport"`
	AuthenticationKind AuthenticationKind `json:"authentication_kind"`
}
