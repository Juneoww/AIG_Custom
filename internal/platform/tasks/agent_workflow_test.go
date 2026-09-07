package tasks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/reports"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func agentWorkflowInput(key string) CreateInput {
	return CreateInput{IdempotencyKey: key, TaskType: "agent_scan", Content: "验证客服 Agent 的权限边界", CountryIsoCode: "zh_CN", Params: json.RawMessage(`{"agent_id":"customer agent","eval_model_id":"model-1"}`)}
}

func TestAgentWorkflowRejectsUnsupportedNewInputsBeforeResolvingCredentials(t *testing.T) {
	owner := identity.Subject{UserID: "alice-id", Username: "alice", Role: identity.RoleUser}
	for _, variant := range []string{"empty", "whitespace", "attachments", "invalid-utf8"} {
		t.Run(variant, func(t *testing.T) {
			engine := &controlledReferenceEngine{}
			service := NewService(NewMemoryRepository(), engine, audit.NewService(audit.NewMemoryRepository()))
			input := agentWorkflowInput(variant)
			switch variant {
			case "empty":
				input.Content = ""
			case "whitespace":
				input.Content = " \t\n"
			case "attachments":
				input.AttachmentIDs = []string{"attachment-1"}
			case "invalid-utf8":
				input.Content = string([]byte{0xff})
			}
			_, err := service.Create(context.Background(), owner, input)
			require.ErrorIs(t, err, ErrInvalid)
			assert.Zero(t, engine.referenceCalls.Load())
			assert.Zero(t, engine.submits.Load())
		})
	}
}

func TestAgentWorkflowHistoricalIdempotencyStillConfirmsExistingTask(t *testing.T) {
	ctx := context.Background()
	owner := identity.Subject{UserID: "alice-id", Username: "alice", Role: identity.RoleUser}
	engine := &controlledReferenceEngine{}
	repository := NewMemoryRepository()
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	input := agentWorkflowInput("legacy-agent")
	input.Content = ""
	input.AttachmentIDs = []string{"legacy-attachment"}
	id := uuid.NewSHA1(taskIDNamespace, []byte(owner.UserID+"\x00"+input.IdempotencyKey)).String()
	existing := &Task{ID: id, OwnerUserID: owner.UserID, OwnerUsername: owner.Username, IdempotencyKey: input.IdempotencyKey, EngineSessionID: id, TaskType: input.TaskType, Content: input.Content, Params: input.Params, AttachmentRefs: json.RawMessage(`["legacy-attachment"]`), CountryIsoCode: input.CountryIsoCode, Status: StatusSucceeded, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	_, _, err := repository.CreateOrGet(ctx, existing)
	require.NoError(t, err)
	confirmed, err := service.Create(ctx, owner, input)
	require.NoError(t, err)
	assert.Equal(t, id, confirmed.ID)
	assert.Zero(t, engine.referenceCalls.Load())
	assert.Zero(t, engine.submits.Load())
	input.Content = "changed"
	_, err = service.Create(ctx, owner, input)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestAgentWorkflowSummaryOnlyProjectsExactSafeReferences(t *testing.T) {
	for _, test := range []struct {
		name         string
		params       string
		agent, model string
	}{
		{"valid", `{"agent_id":"customer agent","eval_model_id":"model-1"}`, "customer agent", "model-1"},
		{"legacy-type", `{"agent_id":"agent-1","eval_model_id":"model-1"}`, "agent-1", "model-1"},
		{"raw-config", `{"agent_id":"agent-1","eval_model_id":"model-1","agent_data":"secret"}`, "", ""},
		{"credential", `{"agent_id":"sk-secret","eval_model_id":"ghp_secret"}`, "", ""},
		{"path", `{"agent_id":"../private","eval_model_id":"https://secret.invalid"}`, "", ""},
		{"missing", `{"agent_id":"agent-1"}`, "", ""},
		{"overlong", `{"agent_id":"` + strings.Repeat("a", 129) + `","eval_model_id":"model-1"}`, "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			taskType := "agent_scan"
			if test.name == "legacy-type" {
				taskType = "Agent-Scan"
			}
			detail := taskDetailOf(&Task{TaskType: taskType, CountryIsoCode: "zh_CN", Params: json.RawMessage(test.params)})
			wire, err := json.Marshal(detail.InputSummary)
			require.NoError(t, err)
			var fields map[string]any
			require.NoError(t, json.Unmarshal(wire, &fields))
			assert.Equal(t, "zh", fields["language"])
			if test.agent != "" {
				assert.Equal(t, test.agent, fields["agent_id"])
			} else {
				assert.NotContains(t, fields, "agent_id")
			}
			if test.model != "" {
				assert.Equal(t, test.model, fields["eval_model_id"])
			} else {
				assert.NotContains(t, fields, "eval_model_id")
			}
		})
	}
}

func TestAgentWorkflowDetailLinksOnlyAuthorizedReadySnapshot(t *testing.T) {
	ctx := context.Background()
	owner := identity.Subject{UserID: "alice-id", Username: "alice", Role: identity.RoleUser}
	engine := &recordingEngine{results: map[string]json.RawMessage{}}
	repository := NewMemoryRepository()
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	snapshots := reports.NewMemoryRepository()
	service.SetReportSnapshotService(reports.NewService(snapshots, brand.NewService(brand.NewMemoryRepository())))
	created, err := service.Create(ctx, owner, agentWorkflowInput("snapshot"))
	require.NoError(t, err)
	detail, err := service.BrowserGet(ctx, owner, created.ID)
	require.NoError(t, err)
	before, _ := json.Marshal(detail)
	assert.NotContains(t, string(before), "report_id")
	setEngineResult(engine, created.EngineSessionID, json.RawMessage(`{"id":"agent-result","type":"resultUpdate","timestamp":1,"result":{"schema_version":"agent-security-report@1","score":100,"results":[]}}`))
	require.NoError(t, service.RecordEngineEvent(ctx, created.EngineSessionID, EngineStateSucceeded, ""))
	snapshot, err := snapshots.GetByTaskID(ctx, created.ID)
	require.NoError(t, err)
	for _, subject := range []identity.Subject{owner, {UserID: "auditor", Role: identity.RoleAuditor}, {UserID: "admin", Role: identity.RoleAdmin}} {
		detail, err = service.BrowserGet(ctx, subject, created.ID)
		require.NoError(t, err)
		wire, _ := json.Marshal(detail)
		var fields map[string]any
		require.NoError(t, json.Unmarshal(wire, &fields))
		assert.Equal(t, snapshot.ID, fields["report_id"])
	}
	_, err = service.BrowserGet(ctx, identity.Subject{UserID: "bob", Role: identity.RoleUser}, created.ID)
	require.ErrorIs(t, err, ErrNotFound)
}
