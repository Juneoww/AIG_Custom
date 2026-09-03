package mcpconnections

import (
	"encoding/json"
	"time"
)

type Scope string

const (
	ScopePrivate Scope = "private"
	ScopeGlobal  Scope = "global"
)

type Transport string

const (
	TransportHTTP  Transport = "http"
	TransportSSE   Transport = "sse"
	TransportStdio Transport = "stdio"
)

type ProbeStatus string

const (
	// ProbeStatusNotTested 表示连接材料尚未经过任何可用性测试。
	// 新版本必须回到这一状态，不能沿用旧版本的测试结论。
	ProbeStatusNotTested ProbeStatus = "not_tested"
	ProbeStatusPassed    ProbeStatus = "passed"
	ProbeStatusFailed    ProbeStatus = "failed"
)

type AuthenticationKind string

const (
	AuthenticationNone          AuthenticationKind = "none"
	AuthenticationBearer        AuthenticationKind = "bearer"
	AuthenticationAPIKeyHeader  AuthenticationKind = "api_key_header"
	AuthenticationCustomHeaders AuthenticationKind = "custom_headers"
)

const MaskedSecret = "********"

// Header 仅在待加密的连接载荷中使用；Value 不得投影到管理端视图。
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// MarshalJSON 让 Header 作为独立公开类型使用时同样默认安全，不能依赖外层
// ConnectionPayload 的脱敏逻辑来保护 Value。
func (header Header) MarshalJSON() ([]byte, error) {
	value := header.Value
	if value != "" {
		value = MaskedSecret
	}
	return json.Marshal(struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}{Name: header.Name, Value: value})
}

// Authentication 是待加密的认证材料。HeaderName 可以作为未来管理详情的
// 最小元数据，但 Secret 永远只能存在于加密载荷的解密结果中。
type Authentication struct {
	Kind       AuthenticationKind `json:"kind"`
	HeaderName string             `json:"header_name,omitempty"`
	Secret     string             `json:"secret,omitempty"`
}

// MarshalJSON 让认证材料脱离 ConnectionPayload 被序列化时仍不暴露 Secret。
func (authentication Authentication) MarshalJSON() ([]byte, error) {
	secret := authentication.Secret
	if secret != "" {
		secret = MaskedSecret
	}
	return json.Marshal(struct {
		Kind       AuthenticationKind `json:"kind"`
		HeaderName string             `json:"header_name,omitempty"`
		Secret     string             `json:"secret,omitempty"`
	}{Kind: authentication.Kind, HeaderName: authentication.HeaderName, Secret: secret})
}

// ConnectionPayload 是连接版本的明文工作对象，持久化前必须由 Keyring 加密。
// Endpoint、认证材料和自定义 Header 值绝不能成为数据库模型的明文字段。
type ConnectionPayload struct {
	Endpoint       string         `json:"endpoint"`
	Authentication Authentication `json:"authentication"`
	Headers        []Header       `json:"headers,omitempty"`
}

// MarshalJSON 是明文工作对象的最后一道日志/响应保护。它只保留 Header 名等
// 管理端允许使用的最小元数据，endpoint、认证材料和 Header 值均须脱敏。
// 加密实现使用专用 wire 类型绕过该方法保存完整明文到密文中。
func (payload ConnectionPayload) MarshalJSON() ([]byte, error) {
	authentication := payload.Authentication
	if authentication.Secret != "" {
		authentication.Secret = MaskedSecret
	}
	headers := make([]Header, len(payload.Headers))
	for index, header := range payload.Headers {
		headers[index] = header
		if headers[index].Value != "" {
			headers[index].Value = MaskedSecret
		}
	}
	endpoint := payload.Endpoint
	if endpoint != "" {
		endpoint = MaskedSecret
	}
	return json.Marshal(struct {
		Endpoint       string         `json:"endpoint"`
		Authentication Authentication `json:"authentication"`
		Headers        []Header       `json:"headers,omitempty"`
	}{Endpoint: endpoint, Authentication: authentication, Headers: headers})
}

// BindingEncryptionContext 由任务所属的受信任上下文提供。绑定表刻意不复制
// owner/scope，因此解密时调用方必须给出相同的归属范围，避免跨任务重放密文。
type BindingEncryptionContext struct {
	OwnerUserID string
	Scope       Scope
	Version     int
}

// RepositorySourceSnapshot 是待加密的仓库来源快照；RepositoryURL 不允许直接
// 映射到数据库列或浏览器 JSON 投影。
type RepositorySourceSnapshot struct {
	RepositoryURL string `json:"repository_url"`
}

// MarshalJSON 防止仓库来源快照被日志或管理端响应意外序列化为明文。
func (snapshot RepositorySourceSnapshot) MarshalJSON() ([]byte, error) {
	value := snapshot.RepositoryURL
	if value != "" {
		value = MaskedSecret
	}
	return json.Marshal(struct {
		RepositoryURL string `json:"repository_url"`
	}{RepositoryURL: value})
}

type ConnectionConfig struct {
	ID               string    `gorm:"primaryKey;column:id" json:"id"`
	OwnerUserID      string    `gorm:"not null;column:owner_user_id" json:"owner_user_id"`
	Scope            Scope     `gorm:"not null;column:scope" json:"scope"`
	Name             string    `gorm:"not null;column:name" json:"name"`
	Description      string    `gorm:"not null;column:description" json:"description"`
	CurrentVersion   int       `gorm:"not null;column:current_version" json:"current_version"`
	ResourceRevision string    `gorm:"not null;column:resource_revision" json:"resource_revision"`
	Enabled          bool      `gorm:"not null;column:enabled" json:"enabled"`
	CreatedAt        time.Time `gorm:"not null;column:created_at" json:"created_at"`
	UpdatedAt        time.Time `gorm:"not null;column:updated_at" json:"updated_at"`
}

func (ConnectionConfig) TableName() string { return "platform_mcp_connection_configs" }

// ConnectionVersion 只保留加密后的连接材料。EncryptedPayload、PayloadNonce
// 和 KeyID 不通过 JSON 输出，避免管理端或结构化日志泄露可用于解密的材料。
type ConnectionVersion struct {
	ID                 string      `gorm:"primaryKey;column:id" json:"id"`
	ConnectionConfigID string      `gorm:"not null;column:connection_config_id" json:"connection_config_id"`
	Version            int         `gorm:"not null;column:version" json:"version"`
	EncryptedPayload   []byte      `gorm:"not null;column:encrypted_payload" json:"-"`
	PayloadNonce       []byte      `gorm:"not null;column:payload_nonce" json:"-"`
	KeyID              string      `gorm:"not null;column:key_id" json:"-"`
	Transport          Transport   `gorm:"not null;column:transport" json:"transport"`
	DetectedTransport  Transport   `gorm:"not null;column:detected_transport" json:"detected_transport,omitempty"`
	ProbeStatus        ProbeStatus `gorm:"not null;column:probe_status" json:"probe_status"`
	CreatedAt          time.Time   `gorm:"not null;column:created_at" json:"created_at"`
}

func (ConnectionVersion) TableName() string { return "platform_mcp_connection_versions" }

// TaskBinding 存储任务和不可变连接版本或仓库快照之间的关系。仓库来源字段
// 与连接载荷相同，必须由专用 Keyring 加密，不能借用模型的加密域。
type TaskBinding struct {
	ID                      string    `gorm:"primaryKey;column:id" json:"id"`
	TaskID                  string    `gorm:"not null;column:task_id" json:"task_id"`
	SourceKind              string    `gorm:"not null;column:source_kind" json:"source_kind"`
	ConnectionConfigID      *string   `gorm:"column:connection_config_id" json:"connection_config_id,omitempty"`
	ConnectionConfigVersion *int      `gorm:"column:connection_config_version" json:"connection_config_version,omitempty"`
	EncryptedRepositoryURL  []byte    `gorm:"column:encrypted_repository_url" json:"-"`
	RepositoryURLNonce      []byte    `gorm:"column:repository_url_nonce" json:"-"`
	RepositoryURLKeyID      string    `gorm:"column:repository_url_key_id" json:"-"`
	CreatedAt               time.Time `gorm:"not null;column:created_at" json:"created_at"`
	UpdatedAt               time.Time `gorm:"not null;column:updated_at" json:"updated_at"`
}

func (TaskBinding) TableName() string { return "platform_mcp_task_bindings" }
