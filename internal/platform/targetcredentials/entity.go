// Package targetcredentials 管理只用于基础设施扫描目标的私有加密凭据。
package targetcredentials

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrInvalid     = errors.New("基础设施凭据或目标范围无效")
	ErrForbidden   = errors.New("无权访问基础设施凭据")
	ErrNotFound    = errors.New("基础设施凭据不存在")
	ErrConflict    = errors.New("基础设施凭据版本已变更")
	ErrUnavailable = errors.New("基础设施凭据不可用")
)

type Credential struct {
	ID              string    `gorm:"primaryKey" json:"-"`
	OwnerUserID     string    `gorm:"not null;index:idx_target_credentials_owner" json:"-"`
	Name            string    `gorm:"not null"`
	Origin          string    `gorm:"not null"`
	AuthType        string    `gorm:"not null"`
	HeaderName      string    `gorm:"not null"`
	Disabled        bool      `gorm:"not null"`
	Revision        int64     `gorm:"not null"`
	EncryptedSecret []byte    `gorm:"not null" json:"-"`
	SecretNonce     []byte    `gorm:"not null" json:"-"`
	KeyID           string    `gorm:"not null" json:"-"`
	CreatedAt       time.Time `gorm:"not null"`
	UpdatedAt       time.Time `gorm:"not null"`
}

func (Credential) TableName() string { return "platform_target_credentials" }
func (Credential) String() string    { return "target credential [redacted]" }
func (Credential) GoString() string  { return "target credential [redacted]" }

type View struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Origin            string    `json:"origin"`
	AuthType          string    `json:"auth_type"`
	HeaderName        string    `json:"header_name"`
	Disabled          bool      `json:"disabled"`
	Revision          int64     `json:"revision"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	AllowInsecureHTTP bool      `json:"allow_insecure_http"`
}

func viewOf(c *Credential) View {
	return View{c.ID, c.Name, c.Origin, c.AuthType, c.HeaderName, c.Disabled, c.Revision, c.CreatedAt, c.UpdatedAt, usesHTTP(c)}
}

type Input struct {
	Name       string `json:"name"`
	Origin     string `json:"origin"`
	AuthType   string `json:"auth_type"`
	HeaderName string `json:"header_name,omitempty"`
	Username   string `json:"username,omitempty"`
	Secret     string `json:"secret"`
	Disabled   bool   `json:"disabled"`
	// 兼容旧客户端输入；公共接口按 origin 支持 HTTP/HTTPS，不再以此字段授予许可。
	AllowInsecureHTTP bool `json:"allow_insecure_http"`
}

func (Input) String() string               { return "target credential input [redacted]" }
func (Input) GoString() string             { return "target credential input [redacted]" }
func (Input) MarshalJSON() ([]byte, error) { return json.Marshal("[redacted]") }

type secretPayload struct {
	Username string `json:"username"`
	Secret   string `json:"secret"`
}
