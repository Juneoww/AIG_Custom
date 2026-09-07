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

package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Juneoww/AIG_Custom/common/agent"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	platformmodels "github.com/Juneoww/AIG_Custom/internal/platform/models"
	platformtasks "github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskModelResolverPreservesAuthorizedYAMLFallbackForDirectDefaultAndTitle(t *testing.T) {
	ctx := context.Background()
	identityRepository := identity.NewMemoryRepository()
	identityService := identity.NewService(identityRepository)
	user, err := identityService.CreateUser(ctx, identity.CreateUserInput{
		Username: "yaml-user", Password: "password", Role: identity.RoleUser,
	})
	require.NoError(t, err)
	keyring, err := platformmodels.NewKeyring("yaml-test", bytes.Repeat([]byte{0x23}, 32), nil)
	require.NoError(t, err)

	taskManager := NewTaskManager(NewAgentManager(), nil, nil, nil, NewSSEManager())
	taskManager.SetModelResolver(platformmodels.NewScannerResolver(
		platformmodels.NewMemoryRepository(), identityRepository, keyring,
	))
	taskManager.SetYAMLModelSource(&stubYAMLModelSource{models: []*database.Model{{
		ModelID: "yaml-safe", ModelName: "yaml-provider", Token: "yaml-plaintext-token",
		BaseURL: "https://yaml.invalid/v1", Limit: 29,
	}}})

	direct, err := taskManager.resolveTaskModel(ctx, user.Username, "yaml-safe")
	require.NoError(t, err)
	assert.Equal(t, "yaml-provider", direct.Model)
	assert.Equal(t, "yaml-plaintext-token", direct.Token)
	assert.Equal(t, "https://yaml.invalid/v1", direct.BaseUrl)
	assert.Equal(t, 29, direct.Limit)

	defaultModel, err := taskManager.resolveDefaultTaskModel(ctx, user.Username)
	require.NoError(t, err)
	require.NotNil(t, defaultModel)
	assert.Equal(t, "yaml-provider", defaultModel.Model)
	assert.Equal(t, "yaml-plaintext-token", defaultModel.Token)

	title := taskManager.generateTaskTitle(&TaskCreateRequest{
		Username: user.Username,
		Task:     agent.TaskTypeModelJailbreak,
		Params:   map[string]interface{}{"model_id": "yaml-safe"},
	})
	assert.Contains(t, title, "yaml-provider")
	assert.NotContains(t, title, "yaml-plaintext-token")

	_, err = taskManager.resolveTaskModel(ctx, "missing-user", "yaml-safe")
	assert.ErrorIs(t, err, platformmodels.ErrForbidden, "identity rejection must not fall through to YAML")

	references, err := json.Marshal(map[string]any{"model_id": []string{"yaml-safe"}, "eval_model_id": "yaml-safe"})
	require.NoError(t, err)
	require.NoError(t, taskManager.ValidateTaskReferences(ctx, platformtasks.EngineTask{
		OwnerUsername: user.Username, TaskType: "model_redteam_report", Params: references,
	}))
	unknown, err := json.Marshal(map[string]any{"model_id": []string{"yaml-safe", "sk-browser-sensitive-value"}, "eval_model_id": "yaml-safe"})
	require.NoError(t, err)
	require.ErrorIs(t, taskManager.ValidateTaskReferences(ctx, platformtasks.EngineTask{
		OwnerUsername: user.Username, TaskType: "model_redteam_report", Params: unknown,
	}), platformtasks.ErrInvalid)
}

func TestTaskReferenceValidationUsesGovernedAgentConfigRegistry(t *testing.T) {
	workingDirectory, err := os.Getwd()
	require.NoError(t, err)
	temporary := t.TempDir()
	require.NoError(t, os.Chdir(temporary))
	t.Cleanup(func() { _ = os.Chdir(workingDirectory) })
	require.NoError(t, os.MkdirAll(filepath.Join("data", "agents", PublicUser), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join("data", "agents", PublicUser, "safe-agent.yaml"), []byte(validWorkflowProvider), 0o600))

	taskManager := NewTaskManager(NewAgentManager(), nil, nil, nil, NewSSEManager())
	require.NoError(t, taskManager.ValidateTaskReferences(context.Background(), platformtasks.EngineTask{
		OwnerUsername: "alice", TaskType: "agent_scan",
		Params: json.RawMessage(`{"agent_id":"safe-agent"}`),
	}))
	require.ErrorIs(t, taskManager.ValidateTaskReferences(context.Background(), platformtasks.EngineTask{
		OwnerUsername: "alice", TaskType: "agent_scan",
		Params: json.RawMessage(`{"agent_id":"../private"}`),
	}), platformtasks.ErrInvalid)
}

type stubYAMLModelSource struct {
	models    []*database.Model
	loadErr   error
	loadCalls int
	getCalls  int
}

func (source *stubYAMLModelSource) GetYamlModel(modelID string) *database.Model {
	source.getCalls++
	for _, model := range source.models {
		if model.ModelID == modelID {
			copy := *model
			return &copy
		}
	}
	return nil
}

func (source *stubYAMLModelSource) LoadYamlModels() ([]*database.Model, error) {
	source.loadCalls++
	if source.loadErr != nil {
		return nil, source.loadErr
	}
	models := make([]*database.Model, 0, len(source.models))
	for _, model := range source.models {
		copy := *model
		models = append(models, &copy)
	}
	return models, nil
}
