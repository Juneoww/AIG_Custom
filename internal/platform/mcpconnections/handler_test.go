package mcpconnections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/idempotency"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestHandlerMCPConnectionCreateReplayAndStrictManagerBoundary(t *testing.T) {
	db := openMCPConnectionPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	service := NewService(repository, testKeyring(t), nil, testPolicy(t, true))
	role := identity.RoleUser
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("identity_subject", identity.Subject{UserID: "alice", Role: role}) })
	NewHandler(service, idempotency.NewService(idempotency.NewGormRepository(db)), audit.NewService(audit.NewGormRepository(db))).Register(router.Group("/api/v1/platform"))
	call := func(method, path, key, revision, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "/api/v1/platform"+path, bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		request.Header.Set("If-Match", revision)
		result := httptest.NewRecorder()
		router.ServeHTTP(result, request)
		return result
	}
	body := `{"name":"内部服务","server_url":"https://safe.example.test/mcp","transport":"http","authentication":{"kind":"bearer","secret":"credential-sentinel"}}`
	missing := call("POST", "/mcp-connection-configs", "", "", body)
	require.Equal(t, 400, missing.Code, missing.Body.String())
	created := call("POST", "/mcp-connection-configs", "create-connection", "", body)
	require.Equal(t, 201, created.Code, created.Body.String())
	require.NotContains(t, created.Body.String(), "credential-sentinel")
	var mutation idempotency.SafeResponse
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &mutation))
	replay := call("POST", "/mcp-connection-configs", "create-connection", "", body)
	require.Equal(t, 200, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replay"))
	require.JSONEq(t, created.Body.String(), replay.Body.String())
	changed := call("POST", "/mcp-connection-configs", "create-connection", "", `{"name":"different","server_url":"https://safe.example.test/mcp","transport":"http","authentication":{"kind":"bearer","secret":"changed-secret"}}`)
	require.Equal(t, 409, changed.Code)
	list := call("GET", "/mcp-connection-configs", "", "", "")
	require.Equal(t, 200, list.Code)
	require.NotContains(t, list.Body.String(), "safe.example.test")
	require.NotContains(t, list.Body.String(), "credential-sentinel")
	detail := call("GET", "/mcp-connection-configs/"+mutation.ID, "", "", "")
	require.Equal(t, 200, detail.Code)
	require.Contains(t, detail.Body.String(), "safe.example.test")
	require.NotContains(t, detail.Body.String(), "credential-sentinel")
	missingMatch := call("PATCH", "/mcp-connection-configs/"+mutation.ID, "edit", "", `{"name":"新名称"}`)
	require.Equal(t, 428, missingMatch.Code)
	forged := call("POST", "/mcp-connection-configs", "forged", "", `{"scope":"global","name":"x"}`)
	require.Equal(t, 400, forged.Code)
	role = identity.RoleAuditor
	denied := call("GET", "/mcp-connection-configs/"+mutation.ID, "", "", "")
	require.NotEqual(t, 200, denied.Code)
	require.NotContains(t, denied.Body.String(), "safe.example.test")
	items, err := repository.ListConfigs(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
}

func TestMCPProbeResultRollsBackWithReplayCompletion(t *testing.T) {
	db := openMCPConnectionPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	service := NewService(repository, testKeyring(t), NewProbeEngine(&scriptedProbePort{}, ProbeOptions{}), testPolicy(t, true))
	ctx := context.Background()
	subject := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	created, err := service.Create(ctx, subject, serviceInput("atomic", ScopePrivate, TransportHTTP))
	require.NoError(t, err)
	prepared, err := service.prepareProbe(ctx, subject, created.ID, created.ResourceRevision)
	require.NoError(t, err)
	mutation, err := audit.BeginMutation(ctx, audit.NewService(audit.NewGormRepository(db)), subject, audit.EventInput{Action: audit.Action("mcp_connection.changed"), ResourceType: "mcp_connection", ResourceID: created.ID})
	require.NoError(t, err)
	err = mutation.Run(ctx, created.ID, nil, func(tx context.Context) error {
		_, err := service.completePreparedProbe(tx, prepared)
		if err != nil {
			return err
		}
		return errors.New("replay unavailable")
	})
	require.Error(t, err)
	current, err := repository.GetConfig(ctx, created.ID)
	require.NoError(t, err)
	version, err := repository.GetVersion(ctx, created.ID, current.CurrentVersion)
	require.NoError(t, err)
	require.Equal(t, ProbeStatusNotTested, version.ProbeStatus)
	require.Equal(t, prepared.attempt.Token, current.ResourceRevision)
	require.False(t, current.Enabled)
}
