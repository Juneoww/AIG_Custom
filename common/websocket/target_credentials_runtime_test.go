package websocket

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/common/agent"
	platformtasks "github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/Juneoww/AIG_Custom/pkg/httpx"
	"github.com/stretchr/testify/require"
)

func TestTargetCredentialDisconnectReleasesSecretsWithoutPlatformSink(t *testing.T) {
	tm, cleanup := newTestTaskManager(t)
	defer cleanup()
	pair := websocketConnectionPair(t)
	connection := NewAgentConnection(pair.server)
	connection.agentID = "lost-target-agent"
	tm.agentManager.connections[connection.agentID] = connection
	tm.agentManager.taskManager = tm
	require.NoError(t, tm.taskStore.CreateSession(&database.Session{ID: "lost-target-task", Username: "alice", TaskType: agent.TaskTypeAIInfraScan, Status: TaskStatusDoing, AssignedAgent: connection.agentID}))
	tm.targetRedactors.Store("lost-target-task", &httpx.TargetAuth{Headers: map[string]string{"Authorization": "Bearer disconnect-secret"}})
	connection.cleanup(tm.agentManager)
	_, exists := tm.targetRedactors.Load("lost-target-task")
	require.False(t, exists, "disconnected terminal task must release secret context")
}

func TestTargetCredentialPrivateAssignmentAndEventRedaction(t *testing.T) {
	tm, cleanup := newTestTaskManager(t)
	defer cleanup()
	pair := websocketConnectionPair(t)
	connection := NewAgentConnection(pair.server)
	connection.agentID = "target-agent"
	tm.agentManager.connections[connection.agentID] = connection
	const taskID = "target-runtime-task"
	const secret = "target-secret-sentinel"
	calls := 0
	request := platformtasks.EngineTask{PlatformTaskID: taskID, OwnerUsername: "alice", TaskType: "ai_infra_scan", Content: "https://example.com/api/version", Params: json.RawMessage(`{"target_credential_id":"credential","target_credential_revision":1}`), RuntimeIssuer: func(context.Context) (map[string]any, error) {
		calls++
		return map[string]any{"target_auth": &httpx.TargetAuth{CredentialID: "credential", Revision: 1, Origin: "https://example.com", Headers: map[string]string{"Authorization": "Bearer " + secret}}}, nil
	}}
	_, err := tm.SubmitTask(context.Background(), request)
	require.Error(t, err)
	require.Zero(t, calls, "old agent must never receive authentication material")
	connection.capabilities = []string{"infra-target-auth-v1"}
	_, err = tm.SubmitTask(context.Background(), request)
	require.Error(t, err)
	require.Zero(t, calls, "v1 agents must not receive v2 assignments")
	connection.capabilities = []string{agent.TargetCredentialCapability}
	_, err = tm.SubmitTask(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.NoError(t, pair.client.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, frame, err := pair.client.ReadMessage()
	require.NoError(t, err)
	require.Contains(t, string(frame), secret)
	session, err := tm.taskStore.GetSession(taskID)
	require.NoError(t, err)
	require.NotContains(t, string(session.Params), secret)
	inMemory, exists := tm.GetTask(taskID)
	require.True(t, exists)
	encoded, err := json.Marshal(inMemory)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), secret)
	require.False(t, tm.HandleAgentEvent("other", taskID, WSMsgTypeLiveStatus, map[string]any{"text": secret}))
	require.True(t, tm.HandleAgentEvent(connection.agentID, taskID, WSMsgTypeLiveStatus, map[string]any{"text": secret}))
	events, err := tm.taskStore.GetSessionEventsByType(taskID, WSMsgTypeLiveStatus)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.NotContains(t, string(events[0].EventData), secret)
	tm.targetRedactors.Delete(taskID)
	require.True(t, tm.HandleAgentEvent(connection.agentID, taskID, WSMsgTypeResultUpdate, map[string]any{"text": secret}))
	session, err = tm.taskStore.GetSession(taskID)
	require.NoError(t, err)
	require.Equal(t, TaskStatusError, session.Status)
}
