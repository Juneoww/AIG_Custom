package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failOnRecord struct {
	delegate audit.Recorder
	failOn   int
	calls    int
}

func (recorder *failOnRecord) Record(ctx context.Context, subject identity.Subject, input audit.EventInput) error {
	recorder.calls++
	if recorder.calls == recorder.failOn {
		return errors.New("injected audit failure")
	}
	return recorder.delegate.Record(ctx, subject, input)
}

func TestOnlyAdministratorCanChangeKnowledgeAndEveryChangeIsAudited(t *testing.T) {
	ctx := context.Background()
	auditService := audit.NewService(audit.NewMemoryRepository())
	service := NewService(auditService)
	admin := identity.Subject{UserID: "admin-id", Username: "admin", Role: identity.RoleAdmin}
	user := identity.Subject{UserID: "user-id", Role: identity.RoleUser}
	auditor := identity.Subject{UserID: "auditor-id", Role: identity.RoleAuditor}
	content := "old"

	for _, subject := range []identity.Subject{user, auditor} {
		called := false
		err := service.Apply(ctx, subject, Change{Kind: KindFingerprint, Operation: OperationUpdate, ResourceID: "demo"}, func() error {
			called = true
			content = "forbidden"
			return nil
		})
		assert.ErrorIs(t, err, ErrForbidden)
		assert.False(t, called)
	}

	require.NoError(t, service.Apply(ctx, admin, Change{Kind: KindFingerprint, Operation: OperationUpdate, ResourceID: "demo"}, func() error {
		content = "new"
		return nil
	}))
	assert.Equal(t, "new", content)
	events, err := auditService.Query(ctx, admin, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, audit.OutcomePending, events[0].Outcome)
	assert.Equal(t, audit.OutcomeSuccess, events[1].Outcome)
	assert.Equal(t, audit.ActionKnowledgeChanged, events[1].Action)
	assert.Equal(t, "fingerprint", events[1].ResourceType)
	assert.Equal(t, "demo", events[1].ResourceID)
	assert.Equal(t, events[0].RequestID, events[1].RequestID)
}

func TestKnowledgeChangeDoesNotMutatePreviouslyCapturedReportContent(t *testing.T) {
	ctx := context.Background()
	auditService := audit.NewService(audit.NewMemoryRepository())
	service := NewService(auditService)
	admin := identity.Subject{UserID: "admin-id", Role: identity.RoleAdmin}
	currentRules := []byte("severity: high\n")
	reportSnapshot := append([]byte(nil), currentRules...)

	require.NoError(t, service.Apply(ctx, admin, Change{Kind: KindVulnerability, Operation: OperationUpdate, ResourceID: "CVE-TEST"}, func() error {
		currentRules = []byte("severity: low\n")
		return nil
	}))
	assert.Equal(t, "severity: high\n", string(reportSnapshot))
	assert.Equal(t, "severity: low\n", string(currentRules))
}

func TestKnowledgeMutationRequiresDurableAuditIntentBeforeChangingContent(t *testing.T) {
	ctx := context.Background()
	admin := identity.Subject{UserID: "admin-id", Role: identity.RoleAdmin}
	content := "old"
	service := NewService(&failOnRecord{delegate: audit.NewService(audit.NewMemoryRepository()), failOn: 1})

	err := service.Apply(ctx, admin, Change{Kind: KindFingerprint, Operation: OperationUpdate, ResourceID: "demo"}, func() error {
		content = "new"
		return nil
	})
	assert.Error(t, err)
	assert.Equal(t, "old", content)
}

func TestAsyncKnowledgeAuditSeparatesRequestFromActualCompletion(t *testing.T) {
	ctx := context.Background()
	repository := audit.NewMemoryRepository()
	auditService := audit.NewService(repository)
	service := NewService(auditService)
	admin := identity.Subject{UserID: "admin-id", Role: identity.RoleAdmin}

	completion, err := service.BeginAsync(ctx, admin, Change{Kind: KindSystemData, Operation: OperationUpdate, ResourceID: "system-data"})
	require.NoError(t, err)
	require.NoError(t, completion(ctx, true, map[string]any{"files_updated": 7}))

	events, err := auditService.Query(ctx, admin, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, audit.ActionKnowledgeChangeRequested, events[0].Action)
	assert.Equal(t, audit.OutcomePending, events[0].Outcome)
	assert.Equal(t, audit.ActionKnowledgeChanged, events[1].Action)
	assert.Equal(t, audit.OutcomeSuccess, events[1].Outcome)
}
