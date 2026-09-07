package mcpconnections

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
)

// EditableConnectionDetail 是仅在管理者详情路由使用的明确例外，其他投影不得
// 嵌入此类型。Header 值和任何认证 secret 都没有可序列化字段。
type EditableConnectionDetail struct {
	ConnectionSummary
	ServerURL                string             `json:"server_url"`
	AuthenticationHeaderName string             `json:"authentication_header_name,omitempty"`
	AuthenticationConfigured bool               `json:"authentication_configured"`
	Headers                  []ConfiguredHeader `json:"headers"`
}
type ConfiguredHeader struct {
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
}
type AuthenticationPatch struct {
	Kind       *AuthenticationKind `json:"kind,omitempty"`
	HeaderName *string             `json:"header_name,omitempty"`
	Secret     *string             `json:"secret,omitempty"`
}
type HeaderPatch struct {
	Name  string  `json:"name"`
	Value *string `json:"value,omitempty"`
}
type UpdateConnectionInput struct {
	Name           *string              `json:"name,omitempty"`
	Description    *string              `json:"description,omitempty"`
	ServerURL      *string              `json:"server_url,omitempty"`
	Transport      *Transport           `json:"transport,omitempty"`
	Authentication *AuthenticationPatch `json:"authentication,omitempty"`
	Headers        *[]HeaderPatch       `json:"headers,omitempty"`
	Enabled        *bool                `json:"enabled,omitempty"`
}

// 写入 patch 不可作为日志/错误上下文泄露；幂等层仅哈希原始严格 JSON。
func (patch UpdateConnectionInput) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Redacted bool `json:"redacted"`
	}{true})
}
func (patch AuthenticationPatch) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Redacted bool `json:"redacted"`
	}{true})
}
func (patch HeaderPatch) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Redacted bool `json:"redacted"`
	}{true})
}

func (service *Service) GetEditableDetail(ctx context.Context, subject identity.Subject, id string) (*EditableConnectionDetail, error) {
	config, version, err := service.visibleCurrentVersion(ctx, subject, id)
	if err != nil {
		return nil, err
	}
	if !canManage(subject, config) {
		return nil, ErrForbidden
	}
	if service.keyring == nil {
		return nil, ErrInvalid
	}
	payload, err := service.keyring.OpenConnectionPayload(config, version)
	if err != nil {
		return nil, ErrInvalid
	}
	detail := &EditableConnectionDetail{ConnectionSummary: summaryOf(config, version, &payload), ServerURL: payload.Endpoint, AuthenticationHeaderName: payload.Authentication.HeaderName, AuthenticationConfigured: authenticationConfigured(payload), Headers: []ConfiguredHeader{}}
	for _, header := range payload.Headers {
		detail.Headers = append(detail.Headers, ConfiguredHeader{Name: header.Name, Configured: header.Value != ""})
	}
	return detail, nil
}

// Update 在解密后应用显式 patch。保留只适用于同一认证种类/同名 Header；
// 变更材料创建新不可变版本，仓储在配置锁内再次验证 If-Match。
func (service *Service) Update(ctx context.Context, subject identity.Subject, id, expectedRevision string, patch UpdateConnectionInput) (*ConnectionSummary, error) {
	config, version, err := service.visibleCurrentVersion(ctx, subject, id)
	if err != nil {
		return nil, err
	}
	if !canManage(subject, config) {
		return nil, ErrForbidden
	}
	if expectedRevision == "" || config.ResourceRevision != expectedRevision {
		return nil, ErrConflict
	}
	hasFields := patch.Name != nil || patch.Description != nil || patch.ServerURL != nil || patch.Transport != nil || patch.Authentication != nil || patch.Headers != nil
	if patch.Enabled != nil {
		if hasFields {
			return nil, ErrInvalid
		}
		return service.setEnabled(ctx, subject, id, *patch.Enabled, expectedRevision)
	}
	if !hasFields || service.keyring == nil {
		return nil, ErrInvalid
	}
	previous, err := service.keyring.OpenConnectionPayload(config, version)
	if err != nil {
		return nil, ErrInvalid
	}
	input := CreateConnectionInput{Name: config.Name, Description: config.Description, Scope: config.Scope, Transport: version.Transport, ServerURL: previous.Endpoint, Authentication: previous.Authentication, Headers: append([]Header(nil), previous.Headers...)}
	if patch.Name != nil {
		input.Name = *patch.Name
	}
	if patch.Description != nil {
		input.Description = *patch.Description
	}
	if patch.ServerURL != nil {
		input.ServerURL = *patch.ServerURL
	}
	if patch.Transport != nil {
		input.Transport = *patch.Transport
	}
	if auth := patch.Authentication; auth != nil {
		if auth.Kind != nil && *auth.Kind != input.Authentication.Kind {
			input.Authentication = Authentication{Kind: *auth.Kind}
		}
		if auth.HeaderName != nil {
			if !strings.EqualFold(*auth.HeaderName, input.Authentication.HeaderName) {
				input.Authentication.Secret = ""
			}
			input.Authentication.HeaderName = *auth.HeaderName
		}
		if auth.Secret != nil {
			input.Authentication.Secret = *auth.Secret
		}
	}
	if patch.Headers != nil {
		input.Headers = make([]Header, 0, len(*patch.Headers))
		for _, value := range *patch.Headers {
			next := Header{Name: strings.TrimSpace(value.Name)}
			if value.Value != nil {
				next.Value = *value.Value
			} else {
				found := false
				for _, old := range previous.Headers {
					if strings.EqualFold(old.Name, next.Name) {
						next.Value = old.Value
						found = true
						break
					}
				}
				if !found {
					return nil, ErrInvalid
				}
			}
			input.Headers = append(input.Headers, next)
		}
	}
	if !validCreateInput(input) {
		return nil, ErrInvalid
	}
	payload := connectionPayloadFromInput(input)
	changed := !reflect.DeepEqual(previous, payload) || version.Transport != input.Transport
	var next *ConnectionVersion
	if changed {
		if service.policy == nil || service.policy.ValidateServerURL(ctx, input.ServerURL) != nil {
			return nil, ErrOutboundDenied
		}
		next = &ConnectionVersion{ID: service.newID(), ConnectionConfigID: id, Version: version.Version + 1, Transport: input.Transport, ProbeStatus: ProbeStatusNotTested, CreatedAt: service.now()}
		if service.keyring.SealConnectionPayload(config, next, payload) != nil {
			return nil, ErrInvalid
		}
	}
	repository, ok := service.repository.(interface {
		UpdateConditional(context.Context, string, int, string, string, string, *ConnectionVersion) (*ConnectionConfig, error)
	})
	if !ok {
		return nil, ErrInvalid
	}
	updated, err := repository.UpdateConditional(ctx, id, config.CurrentVersion, expectedRevision, strings.TrimSpace(input.Name), strings.TrimSpace(input.Description), next)
	if err != nil {
		return nil, mapServiceRepositoryError(err)
	}
	if next != nil {
		version = next
	}
	summary := summaryOf(updated, version, &payload)
	return &summary, nil
}

func (service *Service) ProbeConditional(ctx context.Context, subject identity.Subject, id, revision string) (*ConnectionSummary, error) {
	if revision == "" {
		return nil, ErrConflict
	}
	return service.probe(ctx, subject, id, revision)
}
