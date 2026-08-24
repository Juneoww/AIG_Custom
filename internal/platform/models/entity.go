package models

import (
	"encoding/json"
	"fmt"
	"time"
)

type Scope string

const (
	ScopePrivate Scope = "private"
	ScopeGlobal  Scope = "global"
	MaskedToken        = "********"

	DefaultCatalogPageSize = 20
	MaxCatalogPageSize     = 100
	MaxCatalogPage         = 1000
)

type CatalogSource string

const (
	CatalogSourcePlatform CatalogSource = "platform"
	CatalogSourceYAML     CatalogSource = "yaml"
)

type Model struct {
	ID             string    `gorm:"primaryKey;column:id" json:"id"`
	OwnerUserID    string    `gorm:"index" json:"owner_user_id,omitempty"`
	Scope          Scope     `gorm:"index;not null" json:"scope"`
	Name           string    `gorm:"not null" json:"name"`
	ProviderModel  string    `gorm:"not null" json:"provider_model"`
	BaseURL        string    `gorm:"not null" json:"base_url"`
	Note           string    `json:"note,omitempty"`
	Limit          int       `json:"limit,omitempty"`
	Disabled       bool      `gorm:"not null;default:false" json:"disabled"`
	EncryptedToken []byte    `gorm:"not null" json:"-"`
	TokenNonce     []byte    `gorm:"not null" json:"-"`
	KeyID          string    `gorm:"not null" json:"-"`
	CreatedAt      time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt      time.Time `gorm:"not null" json:"updated_at"`
}

func (Model) TableName() string { return "platform_models" }

type CreateInput struct {
	Name          string `json:"name"`
	ProviderModel string `json:"provider_model"`
	BaseURL       string `json:"base_url"`
	Token         string `json:"token"`
	Scope         Scope  `json:"scope"`
	Note          string `json:"note,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

// String prevents accidental structured log formatting from exposing Token.
func (input CreateInput) String() string {
	return fmt.Sprintf("{Name:%q ProviderModel:%q BaseURL:%q Token:%s Scope:%q Note:%q Limit:%d}",
		input.Name, input.ProviderModel, input.BaseURL, MaskedToken, input.Scope, input.Note, input.Limit)
}

func (input CreateInput) GoString() string { return input.String() }

// MarshalJSON prevents structured loggers from serializing a plaintext token.
// Request decoding is unaffected because the API only needs the default
// UnmarshalJSON behavior.
func (input CreateInput) MarshalJSON() ([]byte, error) {
	type safeCreateInput CreateInput
	copy := safeCreateInput(input)
	if copy.Token != "" {
		copy.Token = MaskedToken
	}
	return json.Marshal(copy)
}

type UpdateInput struct {
	Name          *string `json:"name,omitempty"`
	ProviderModel *string `json:"provider_model,omitempty"`
	BaseURL       *string `json:"base_url,omitempty"`
	Token         *string `json:"token,omitempty"`
	Note          *string `json:"note,omitempty"`
	Limit         *int    `json:"limit,omitempty"`
	Disabled      *bool   `json:"disabled,omitempty"`
}

func (input UpdateInput) String() string {
	token := "<unchanged>"
	if input.Token != nil {
		token = MaskedToken
	}
	return fmt.Sprintf("{Name:%v ProviderModel:%v BaseURL:%v Token:%s Note:%v Limit:%v Disabled:%v}",
		input.Name, input.ProviderModel, input.BaseURL, token, input.Note, input.Limit, input.Disabled)
}

func (input UpdateInput) GoString() string { return input.String() }

func (input UpdateInput) MarshalJSON() ([]byte, error) {
	type safeUpdateInput UpdateInput
	copy := safeUpdateInput(input)
	if copy.Token != nil {
		masked := MaskedToken
		copy.Token = &masked
	}
	return json.Marshal(copy)
}

type View struct {
	ID            string    `json:"id"`
	OwnerUserID   string    `json:"owner_user_id,omitempty"`
	Scope         Scope     `json:"scope"`
	Name          string    `json:"name"`
	ProviderModel string    `json:"provider_model"`
	BaseURL       string    `json:"base_url"`
	Note          string    `json:"note,omitempty"`
	Limit         int       `json:"limit,omitempty"`
	Disabled      bool      `json:"disabled"`
	Token         string    `json:"token"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type CatalogView struct {
	ID            string        `json:"id"`
	OwnerUserID   string        `json:"owner_user_id,omitempty"`
	Scope         Scope         `json:"scope"`
	Name          string        `json:"name"`
	ProviderModel string        `json:"provider_model"`
	BaseURL       string        `json:"base_url"`
	Note          string        `json:"note,omitempty"`
	Limit         int           `json:"limit,omitempty"`
	Disabled      bool          `json:"disabled"`
	Token         string        `json:"token"`
	Source        CatalogSource `json:"source"`
	ReadOnly      bool          `json:"read_only"`
	CreatedAt     time.Time     `json:"created_at,omitempty"`
	UpdatedAt     time.Time     `json:"updated_at,omitempty"`
}

type CatalogPage struct {
	Items    []CatalogView `json:"items"`
	Total    int64         `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}

func viewOf(model *Model) View {
	return View{
		ID: model.ID, OwnerUserID: model.OwnerUserID, Scope: model.Scope, Name: model.Name,
		ProviderModel: model.ProviderModel, BaseURL: model.BaseURL, Note: model.Note,
		Limit: model.Limit, Disabled: model.Disabled, Token: MaskedToken,
		CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
	}
}

func catalogViewOf(model *Model, readOnly bool) CatalogView {
	return CatalogView{
		ID: model.ID, OwnerUserID: model.OwnerUserID, Scope: model.Scope, Name: model.Name,
		ProviderModel: model.ProviderModel, BaseURL: model.BaseURL, Note: model.Note,
		Limit: model.Limit, Disabled: model.Disabled, Token: MaskedToken,
		Source: CatalogSourcePlatform, ReadOnly: readOnly,
		CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
	}
}
