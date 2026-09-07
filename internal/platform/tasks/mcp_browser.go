package tasks

import (
	"context"
	"encoding/json"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
)

// MCPModelReference 只给专属 MCP 领域返回模型引用，不扩大通用任务 DTO。
func (service *Service) MCPModelReference(ctx context.Context, subject identity.Subject, id string) (string, error) {
	query, err := taskListQueryFor(subject)
	if err != nil {
		return "", err
	}
	task, err := service.repository.GetBrowser(ctx, id, query.OwnerUserID)
	if err != nil {
		return "", err
	}
	if canonicalTaskType(task.TaskType) != "mcp_scan" {
		return "", ErrNotFound
	}
	var input struct {
		ModelID string `json:"model_id"`
	}
	if len(task.Params) > MaxTaskParamsLength || json.Unmarshal(task.Params, &input) != nil {
		return "", nil
	}
	return safeModelID(input.ModelID), nil
}
