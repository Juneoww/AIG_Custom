package tasks

import (
	"context"
	"encoding/json"

	"github.com/Juneoww/AIG_Custom/pkg/httpx"
)

type TargetCredentials interface {
	ValidateReference(context.Context, string, string, int64, string) error
	Resolve(context.Context, string, string, int64, string) (*httpx.TargetAuth, error)
}

func (service *Service) SetTargetCredentials(credentials TargetCredentials) {
	service.targetCredentials = credentials
}

func (service *Service) validateTargetCredential(ctx context.Context, owner, content string, attachments []string, raw json.RawMessage) error {
	params, _, valid := decodeInfrastructureTaskParams(raw)
	if !valid {
		return ErrInvalid
	}
	if params.TargetCredentialID == "" {
		return nil
	}
	if service.targetCredentials == nil || len(attachments) > 0 {
		return ErrInvalid
	}
	if err := service.targetCredentials.ValidateReference(ctx, owner, params.TargetCredentialID, params.TargetCredentialRevision, content); err != nil {
		return ErrInvalid
	}
	return nil
}
func (service *Service) targetRuntimeIssuer(task *Task) RuntimeParamsIssuer {
	return func(ctx context.Context) (map[string]any, error) {
		params, _, valid := decodeInfrastructureTaskParams(task.Params)
		if !valid || service.targetCredentials == nil || params.TargetCredentialID == "" {
			return nil, ErrInvalid
		}
		auth, err := service.targetCredentials.Resolve(ctx, task.OwnerUserID, params.TargetCredentialID, params.TargetCredentialRevision, task.Content)
		if err != nil {
			return nil, ErrInvalid
		}
		return map[string]any{"target_auth": auth}, nil
	}
}
func HasInfrastructureTargetCredential(raw json.RawMessage) bool {
	p, _, ok := decodeInfrastructureTaskParams(raw)
	return ok && p.TargetCredentialID != ""
}
func ValidInfrastructureSafeTaskParams(raw json.RawMessage) bool {
	_, _, ok := decodeInfrastructureTaskParams(raw)
	return ok
}

func InfrastructureRuntimeAuth(runtime map[string]any) (*httpx.TargetAuth, error) {
	if len(runtime) != 1 || runtime["target_auth"] == nil {
		return nil, ErrInvalid
	}
	raw, err := json.Marshal(runtime["target_auth"])
	if err != nil {
		return nil, ErrInvalid
	}
	var auth httpx.TargetAuth
	if !decodeExactJSON(raw, &auth) || auth.Validate() != nil {
		return nil, ErrInvalid
	}
	return &auth, nil
}
func ValidInfrastructureRuntimeAssignment(safe json.RawMessage, runtime map[string]any) bool {
	params, _, valid := decodeInfrastructureTaskParams(safe)
	if !valid {
		return false
	}
	if params.TargetCredentialID == "" {
		return len(runtime) == 0
	}
	auth, err := InfrastructureRuntimeAuth(runtime)
	return err == nil && auth.CredentialID == params.TargetCredentialID && auth.Revision == params.TargetCredentialRevision
}
