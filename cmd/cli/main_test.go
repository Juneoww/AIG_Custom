package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/require"
)

func TestCreatePasswordResetCommandDeliversOneTimeTokenOnlyToSensitiveStdout(t *testing.T) {
	service := identity.NewService(identity.NewMemoryRepository())
	ctx := context.Background()
	_, err := service.CreateUser(ctx, identity.CreateUserInput{Username: "alice", Password: "old-password", Role: identity.RoleUser})
	require.NoError(t, err)
	auditService := audit.NewService(audit.NewMemoryRepository())

	var stdout bytes.Buffer
	require.NoError(t, runCreatePasswordResetForService(ctx, service, auditService, []string{"--username", "alice"}, &stdout))

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], "SENSITIVE")
	require.Contains(t, lines[0], "do not log")
	token := lines[1]
	require.NotEmpty(t, token)
	require.NotContains(t, lines[0], token)
	events, err := auditService.Query(ctx, identity.Subject{Role: identity.RoleAuditor}, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, audit.OutcomePending, events[0].Outcome)
	require.Equal(t, audit.OutcomeSuccess, events[1].Outcome)
	encodedEvents, err := json.Marshal(events)
	require.NoError(t, err)
	require.NotContains(t, string(encodedEvents), token)
	require.NoError(t, service.ResetPassword(ctx, token, "new-temporary-password"))
	require.Error(t, service.ResetPassword(ctx, token, "another-temporary-password"))
}
