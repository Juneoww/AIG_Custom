package targetcredentials

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/models"
	"github.com/Juneoww/AIG_Custom/pkg/httpx"
	"github.com/google/uuid"
)

type Service struct {
	repository Repository
	keys       *models.Keyring
	audits     audit.Recorder
}

func NewService(r Repository, keys *models.Keyring, audits audit.Recorder) *Service {
	return &Service{r, keys, audits}
}
func allowed(s identity.Subject) bool {
	return s.UserID != "" && (s.Role == identity.RoleUser || s.Role == identity.RoleAdmin)
}

func (s *Service) List(ctx context.Context, subject identity.Subject) ([]View, error) {
	if !allowed(subject) {
		return nil, ErrForbidden
	}
	rows, err := s.repository.List(ctx, subject.UserID)
	if err != nil {
		return nil, err
	}
	result := make([]View, 0, len(rows))
	for i := range rows {
		result = append(result, viewOf(&rows[i]))
	}
	return result, nil
}
func (s *Service) Get(ctx context.Context, subject identity.Subject, id string) (View, error) {
	if !allowed(subject) {
		return View{}, ErrForbidden
	}
	c, err := s.repository.Get(ctx, subject.UserID, id)
	if err != nil {
		return View{}, err
	}
	return viewOf(c), nil
}
func validText(value string, max int, required bool) bool {
	return len(value) <= max && (!required || strings.TrimSpace(value) != "") && !strings.ContainsFunc(value, unicode.IsControl)
}
func normalize(input Input) (Input, error) {
	if !validText(input.Name, 160, true) || input.Name != strings.TrimSpace(input.Name) || !validText(input.Secret, 8192, false) || !validText(input.Username, 256, false) {
		return input, ErrInvalid
	}
	origin, err := httpx.NormalizeTargetOrigin(input.Origin, true)
	if err != nil {
		return input, ErrInvalid
	}
	input.Origin = origin
	switch input.AuthType {
	case "bearer":
		if input.HeaderName != "" || input.Username != "" {
			return input, ErrInvalid
		}
	case "api_key":
		if input.Username != "" || !httpx.ValidCredentialHeader(input.HeaderName) || strings.EqualFold(input.HeaderName, "Authorization") || strings.EqualFold(input.HeaderName, "Cookie") {
			return input, ErrInvalid
		}
		input.HeaderName = http.CanonicalHeaderKey(input.HeaderName)
	case "basic":
		if input.HeaderName != "" || strings.Contains(input.Username, ":") {
			return input, ErrInvalid
		}
	case "cookie":
		if input.HeaderName != "" || input.Username != "" {
			return input, ErrInvalid
		}
	default:
		return input, ErrInvalid
	}
	return input, nil
}
func (s *Service) Create(ctx context.Context, subject identity.Subject, input Input) (View, error) {
	if !allowed(subject) {
		return View{}, ErrForbidden
	}
	input, err := normalize(input)
	if err != nil || input.Secret == "" {
		return View{}, ErrInvalid
	}
	now := time.Now().UTC()
	c := &Credential{ID: uuid.NewString(), OwnerUserID: subject.UserID, Name: input.Name, Origin: input.Origin, AuthType: input.AuthType, HeaderName: input.HeaderName, Disabled: input.Disabled, Revision: 1, CreatedAt: now, UpdatedAt: now}
	payload := secretPayload{Username: input.Username, Secret: input.Secret}
	if _, err := runtimeOf(c, payload); err != nil {
		return View{}, ErrInvalid
	}
	if err := s.seal(c, payload); err != nil {
		return View{}, ErrUnavailable
	}
	err = s.mutate(ctx, subject, c, "created", func(tx context.Context) error { return s.repository.Create(tx, c) })
	if err != nil {
		return View{}, err
	}
	return viewOf(c), nil
}
func (s *Service) Update(ctx context.Context, subject identity.Subject, id string, revision int64, input Input) (View, error) {
	if !allowed(subject) {
		return View{}, ErrForbidden
	}
	c, err := s.repository.Get(ctx, subject.UserID, id)
	if err != nil {
		return View{}, err
	}
	if revision <= 0 || c.Revision != revision {
		return View{}, ErrConflict
	}
	input, err = normalize(input)
	if err != nil {
		return View{}, ErrInvalid
	}
	payload := secretPayload{Username: input.Username, Secret: input.Secret}
	if input.Secret == "" {
		if c.Origin != input.Origin || c.AuthType != input.AuthType || c.HeaderName != input.HeaderName || input.Username != "" {
			return View{}, ErrInvalid
		}
		payload, err = s.open(c)
		if err != nil {
			return View{}, ErrUnavailable
		}
	}
	c.Name = input.Name
	c.Origin = input.Origin
	c.AuthType = input.AuthType
	c.HeaderName = input.HeaderName
	c.Disabled = input.Disabled
	c.Revision++
	c.UpdatedAt = time.Now().UTC()
	if _, err := runtimeOf(c, payload); err != nil {
		return View{}, ErrInvalid
	}
	if err := s.seal(c, payload); err != nil {
		return View{}, ErrUnavailable
	}
	err = s.mutate(ctx, subject, c, "updated", func(tx context.Context) error { return s.repository.Replace(tx, c, revision) })
	if err != nil {
		return View{}, err
	}
	return viewOf(c), nil
}
func (s *Service) Delete(ctx context.Context, subject identity.Subject, id string, revision int64) error {
	if !allowed(subject) {
		return ErrForbidden
	}
	c, err := s.repository.Get(ctx, subject.UserID, id)
	if err != nil {
		return err
	}
	if revision <= 0 || c.Revision != revision {
		return ErrConflict
	}
	return s.mutate(ctx, subject, c, "deleted", func(tx context.Context) error { return s.repository.Delete(tx, subject.UserID, id, revision) })
}
func (s *Service) mutate(ctx context.Context, subject identity.Subject, c *Credential, action string, apply func(context.Context) error) error {
	metadata := map[string]any{"revision": c.Revision, "auth_type": c.AuthType, "disabled": c.Disabled, "allow_insecure_http": usesHTTP(c)}
	mutation, err := audit.BeginMutation(ctx, s.audits, subject, audit.EventInput{Action: audit.Action("target_credential." + action), ResourceType: "target_credential", ResourceID: c.ID, Metadata: metadata})
	if err != nil {
		return err
	}
	return mutation.Run(ctx, c.ID, metadata, apply)
}

// 使用现有主密钥轮换能力；独立 AAD 前缀防止模型和目标凭据之间密文替换。
func envelope(c *Credential) *models.Model {
	return &models.Model{ID: "target-credential/v1/" + c.ID + "\x00" + c.Origin + "\x00" + c.AuthType + "\x00" + c.HeaderName + "\x00" + strconv.FormatInt(c.Revision, 10), Scope: models.ScopePrivate, OwnerUserID: c.OwnerUserID, EncryptedToken: c.EncryptedSecret, TokenNonce: c.SecretNonce, KeyID: c.KeyID}
}
func (s *Service) seal(c *Credential, p secretPayload) error {
	if s.keys == nil {
		return ErrUnavailable
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return ErrInvalid
	}
	model := envelope(c)
	if err := s.keys.SealToken(model, string(raw)); err != nil {
		return ErrUnavailable
	}
	c.EncryptedSecret = model.EncryptedToken
	c.SecretNonce = model.TokenNonce
	c.KeyID = model.KeyID
	return nil
}
func (s *Service) open(c *Credential) (secretPayload, error) {
	var payload secretPayload
	if s.keys == nil {
		return payload, ErrUnavailable
	}
	raw, err := s.keys.OpenToken(envelope(c))
	if err != nil {
		return payload, ErrUnavailable
	}
	if json.Unmarshal([]byte(raw), &payload) != nil || payload.Secret == "" {
		return payload, ErrUnavailable
	}
	return payload, nil
}

// HTTP 许可绑定已保存且参与 AAD 的协议，不增加可独立篡改的权限列。
func usesHTTP(c *Credential) bool { return strings.HasPrefix(c.Origin, "http://") }

func runtimeOf(c *Credential, p secretPayload) (*httpx.TargetAuth, error) {
	headers := map[string]string{}
	switch c.AuthType {
	case "bearer":
		headers["Authorization"] = "Bearer " + p.Secret
	case "api_key":
		headers[c.HeaderName] = p.Secret
	case "basic":
		if p.Username == "" {
			return nil, ErrInvalid
		}
		headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(p.Username+":"+p.Secret))
	case "cookie":
		cookies := (&http.Request{Header: http.Header{"Cookie": []string{p.Secret}}}).Cookies()
		if len(cookies) == 0 {
			return nil, ErrInvalid
		}
		headers["Cookie"] = p.Secret
	default:
		return nil, ErrInvalid
	}
	runtime := &httpx.TargetAuth{CredentialID: c.ID, Revision: c.Revision, Origin: c.Origin, Headers: headers, AllowInsecureHTTP: usesHTTP(c)}
	if err := runtime.Validate(); err != nil {
		return nil, ErrInvalid
	}
	return runtime, nil
}
func (s *Service) ValidateReference(ctx context.Context, owner, id string, revision int64, content string) error {
	c, err := s.repository.Get(ctx, owner, id)
	if err != nil {
		return ErrUnavailable
	}
	if c.Disabled || revision < 1 || c.Revision != revision {
		return ErrUnavailable
	}
	if err := httpx.ValidateTargetURLs(c.Origin, strings.Fields(content), usesHTTP(c)); err != nil {
		return ErrInvalid
	}
	return nil
}

// Resolve 仅在 Agent 分配前调用，不将解密结果写回任务或引擎 session。
func (s *Service) Resolve(ctx context.Context, owner, id string, revision int64, content string) (*httpx.TargetAuth, error) {
	c, err := s.repository.Get(ctx, owner, id)
	if err != nil {
		return nil, ErrUnavailable
	}
	if c.Disabled || revision < 1 || c.Revision != revision {
		return nil, ErrUnavailable
	}
	if err := httpx.ValidateTargetURLs(c.Origin, strings.Fields(content), usesHTTP(c)); err != nil {
		return nil, ErrInvalid
	}
	payload, err := s.open(c)
	if err != nil {
		return nil, err
	}
	return runtimeOf(c, payload)
}
