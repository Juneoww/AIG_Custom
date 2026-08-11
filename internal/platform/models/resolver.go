// Copyright (c) 2024-2026 Tencent Zhuque Lab. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
)

type scannerIdentityLookup interface {
	UserByUsername(context.Context, string) (*identity.User, error)
}

// ScannerModel is the narrow plaintext boundary used immediately before an
// authenticated task is sent to a scanner. Its fields are private, and both
// formatting and JSON serialization redact the token.
type ScannerModel struct {
	providerModel string
	token         string
	baseURL       string
	limit         int
}

func NewScannerModel(providerModel, token, baseURL string, limit int) (*ScannerModel, error) {
	providerModel = strings.TrimSpace(providerModel)
	baseURL = strings.TrimSpace(baseURL)
	if providerModel == "" || token == "" {
		return nil, ErrInvalid
	}
	return &ScannerModel{providerModel: providerModel, token: token, baseURL: baseURL, limit: limit}, nil
}

func (model *ScannerModel) ProviderModel() string { return model.providerModel }
func (model *ScannerModel) Token() string         { return model.token }
func (model *ScannerModel) BaseURL() string       { return model.baseURL }
func (model *ScannerModel) Limit() int            { return model.limit }

func (model *ScannerModel) String() string {
	if model == nil {
		return "<nil>"
	}
	return fmt.Sprintf("{ProviderModel:%q Token:%s BaseURL:%q Limit:%d}", model.providerModel, MaskedToken, model.baseURL, model.limit)
}

func (model *ScannerModel) GoString() string { return model.String() }

func (model *ScannerModel) MarshalJSON() ([]byte, error) {
	if model == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		ProviderModel string `json:"provider_model"`
		Token         string `json:"token"`
		BaseURL       string `json:"base_url"`
		Limit         int    `json:"limit"`
	}{ProviderModel: model.providerModel, Token: MaskedToken, BaseURL: model.baseURL, Limit: model.limit})
}

type ScannerResolver struct {
	models  Repository
	users   scannerIdentityLookup
	keyring *Keyring
}

func NewScannerResolver(models Repository, users scannerIdentityLookup, keyring *Keyring) *ScannerResolver {
	return &ScannerResolver{models: models, users: users, keyring: keyring}
}

func (resolver *ScannerResolver) Resolve(ctx context.Context, username, modelID string) (*ScannerModel, error) {
	user, model, err := resolver.authorizedModel(ctx, username, modelID)
	if err != nil {
		return nil, err
	}
	return resolver.resolveAuthorized(user, model)
}

// Describe performs the same fresh authorization as Resolve without opening
// the token. Task titles use this path and never bring plaintext into scope.
func (resolver *ScannerResolver) Describe(ctx context.Context, username, modelID string) (string, error) {
	_, model, err := resolver.authorizedModel(ctx, username, modelID)
	if err != nil {
		return "", err
	}
	return model.ProviderModel, nil
}

func (resolver *ScannerResolver) ResolveDefault(ctx context.Context, username string) (*ScannerModel, error) {
	user, err := resolver.activeUser(ctx, username)
	if err != nil {
		return nil, err
	}
	models, err := resolver.models.List(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(models, func(left, right int) bool {
		if models[left].CreatedAt.Equal(models[right].CreatedAt) {
			return models[left].ID > models[right].ID
		}
		return models[left].CreatedAt.After(models[right].CreatedAt)
	})
	for index := range models {
		resolved, resolveErr := resolver.resolveAuthorized(user, &models[index])
		if resolveErr == nil {
			return resolved, nil
		}
		if !errors.Is(resolveErr, ErrForbidden) {
			return nil, resolveErr
		}
	}
	return nil, ErrNotFound
}

func (resolver *ScannerResolver) authorizedModel(ctx context.Context, username, modelID string) (*identity.User, *Model, error) {
	user, err := resolver.activeUser(ctx, username)
	if err != nil {
		return nil, nil, err
	}
	modelID = strings.TrimSpace(modelID)
	if len(modelID) == 0 || len(modelID) > maxCompatibilityModelIDLength || !compatibilityModelIDPattern.MatchString(modelID) {
		return nil, nil, ErrInvalid
	}
	model, err := resolver.models.Get(ctx, modelID)
	if err != nil {
		return nil, nil, err
	}
	if model.Disabled || !canResolveForScanner(user, model) {
		return nil, nil, ErrForbidden
	}
	return user, model, nil
}

func (resolver *ScannerResolver) activeUser(ctx context.Context, username string) (*identity.User, error) {
	if resolver == nil || resolver.models == nil || resolver.users == nil || resolver.keyring == nil {
		return nil, ErrInvalid
	}
	user, err := resolver.users.UserByUsername(ctx, strings.TrimSpace(username))
	if err != nil || !user.Active || user.Role == identity.RoleAuditor {
		return nil, ErrForbidden
	}
	return user, nil
}

func (resolver *ScannerResolver) resolveAuthorized(user *identity.User, model *Model) (*ScannerModel, error) {
	if model.Disabled || !canResolveForScanner(user, model) {
		return nil, ErrForbidden
	}
	plaintext, err := resolver.keyring.OpenToken(model)
	if err != nil {
		return nil, err
	}
	return NewScannerModel(model.ProviderModel, plaintext, model.BaseURL, model.Limit)
}

func canResolveForScanner(user *identity.User, model *Model) bool {
	switch user.Role {
	case identity.RoleAdmin:
		return model.Scope == ScopeGlobal
	case identity.RoleUser:
		return model.Scope == ScopeGlobal || model.Scope == ScopePrivate && model.OwnerUserID == user.ID
	default:
		return false
	}
}
