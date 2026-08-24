package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	platformaudit "github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	platformtasks "github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskManagerImplementsNarrowPlatformEngineAdapter(t *testing.T) {
	var _ platformtasks.EngineAdapter = (*TaskManager)(nil)
}

func TestPlatformTaskTypesMapToExactLegacyEngineAliases(t *testing.T) {
	expected := map[string]string{
		"mcp_scan":             "Mcp-Scan",
		"ai_infra_scan":        "AI-Infra-Scan",
		"model_redteam_report": "Model-Redteam-Report",
		"agent_scan":           "Agent-Scan",
	}
	for canonical, alias := range expected {
		actual, ok := platformEngineTaskType(canonical)
		require.True(t, ok, canonical)
		assert.Equal(t, alias, actual)
	}
	_, ok := platformEngineTaskType("future_task")
	assert.False(t, ok)
}

func TestTaskManagerPlatformSubmitPreservesLegacyEnginePayloadAndPrivateSession(t *testing.T) {
	taskManager, cleanup := newTestTaskManager(t)
	defer cleanup()
	serverConnection := websocketConnectionPair(t)
	taskManager.agentManager.mu.Lock()
	taskManager.agentManager.connections["agent-1"] = NewAgentConnection(serverConnection.server)
	taskManager.agentManager.connections["agent-1"].agentID = "agent-1"
	taskManager.agentManager.mu.Unlock()

	request := platformtasks.EngineTask{
		PlatformTaskID: "platform-task-submit", OwnerUsername: "alice", TaskType: "mcp_scan",
		Content: "scan this", Params: json.RawMessage(`{"thread":4}`),
		Attachments: []string{"internal-ref-1"}, CountryIsoCode: "en",
	}
	sessionID, err := taskManager.SubmitTask(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, request.PlatformTaskID, sessionID)

	require.NoError(t, serverConnection.client.SetReadDeadline(time.Now().Add(2*time.Second)))
	var assigned WSMessage
	require.NoError(t, serverConnection.client.ReadJSON(&assigned))
	assert.Equal(t, WSMsgTypeTaskAssign, assigned.Type)
	payload, err := json.Marshal(assigned.Content)
	require.NoError(t, err)
	var content TaskContent
	require.NoError(t, json.Unmarshal(payload, &content))
	assert.Equal(t, request.PlatformTaskID, content.SessionID)
	assert.Equal(t, "Mcp-Scan", content.TaskType)
	assert.Equal(t, request.Content, content.Content)
	assert.Equal(t, request.Attachments, content.Attachments)
	assert.Equal(t, float64(4), content.Params["thread"])
	assert.Equal(t, request.CountryIsoCode, content.CountryIsoCode)

	session, err := taskManager.taskStore.GetSession(request.PlatformTaskID)
	require.NoError(t, err)
	assert.Equal(t, "alice", session.Username)
	assert.Equal(t, "Mcp-Scan", session.TaskType)
	assert.False(t, session.Share)
}

func TestTaskManagerPlatformStatusAndResultUseEngineSessionMapping(t *testing.T) {
	taskManager, cleanup := newTestTaskManager(t)
	defer cleanup()
	completedAt := time.Date(2026, 8, 10, 9, 30, 0, 0, time.UTC)
	completedAtMillis := completedAt.UnixMilli()
	require.NoError(t, taskManager.taskStore.CreateSession(&database.Session{
		ID: "mapped-engine-session", Username: "alice", TaskType: "mcp_scan", Content: "scan",
		Status: TaskStatusDone, Share: false, CompletedAt: &completedAtMillis,
	}))
	require.NoError(t, taskManager.taskStore.StoreEvent(
		"result-1", "mapped-engine-session", WSMsgTypeResultUpdate,
		map[string]any{"result": map[string]any{"safe": true}}, time.Now().UnixMilli(),
	))

	status, err := taskManager.GetTaskStatus(context.Background(), "mapped-engine-session")
	require.NoError(t, err)
	assert.Equal(t, platformtasks.EngineStateSucceeded, status.State)
	assert.Equal(t, completedAt, status.CompletedAt)
	result, err := taskManager.GetResult(context.Background(), "mapped-engine-session")
	require.NoError(t, err)
	assert.JSONEq(t, `{"result":{"safe":true}}`, string(result))
	_, err = taskManager.GetTaskStatus(context.Background(), "browser-supplied-unmapped-id")
	require.ErrorIs(t, err, platformtasks.ErrEngineTaskNotFound)
}

func TestTaskManagerPlatformSubmitNeverResendsExistingAcceptedSession(t *testing.T) {
	for _, testCase := range []struct {
		status        string
		assignedAgent string
		wantUnknown   bool
	}{
		{status: TaskStatusTodo, assignedAgent: "agent-idempotent", wantUnknown: true},
		{status: TaskStatusDoing, assignedAgent: "agent-idempotent"},
		{status: TaskStatusDispatchUnknown, assignedAgent: "agent-idempotent"},
		{status: TaskStatusDone, assignedAgent: "agent-idempotent"},
		{status: TaskStatusError, assignedAgent: "agent-idempotent"},
		{status: TaskStatusTerminated, assignedAgent: "agent-idempotent"},
	} {
		t.Run(testCase.status, func(t *testing.T) {
			taskManager, cleanup := newTestTaskManager(t)
			defer cleanup()
			pair := websocketConnectionPair(t)
			connection := NewAgentConnection(pair.server)
			connection.agentID = "agent-idempotent"
			taskManager.agentManager.mu.Lock()
			taskManager.agentManager.connections[connection.agentID] = connection
			taskManager.agentManager.mu.Unlock()
			sessionID := "existing-" + strings.ReplaceAll(testCase.status, "_", "-")
			require.NoError(t, taskManager.taskStore.CreateSession(&database.Session{
				ID: sessionID, Username: "alice", TaskType: "mcp_scan", Content: "scan",
				Status: testCase.status, AssignedAgent: testCase.assignedAgent, Share: false,
			}))

			returnedID, err := taskManager.SubmitTask(context.Background(), platformtasks.EngineTask{
				PlatformTaskID: sessionID, OwnerUsername: "alice", TaskType: "mcp_scan", Content: "scan",
			})
			if testCase.wantUnknown {
				require.ErrorIs(t, err, platformtasks.ErrSubmitAcknowledgementUnknown)
			} else {
				require.NoError(t, err)
				assert.Equal(t, sessionID, returnedID)
			}
			require.NoError(t, pair.client.SetReadDeadline(time.Now().Add(100*time.Millisecond)))
			var message WSMessage
			readErr := pair.client.ReadJSON(&message)
			assert.Error(t, readErr, "an existing accepted engine session must never receive a second assignment")
		})
	}
}

func TestPlatformDispatchWithoutAgentRetriesExistingUnassignedTodoBeforeFailing(t *testing.T) {
	taskManager, cleanup := newTestTaskManager(t)
	defer cleanup()
	repository := platformtasks.NewMemoryRepository()
	service := platformtasks.NewService(repository, taskManager, platformaudit.NewService(platformaudit.NewMemoryRepository()))
	subject := identity.Subject{UserID: "retry-owner-id", Username: "retry-owner", Role: identity.RoleUser}

	view, err := service.Create(context.Background(), subject, platformtasks.CreateInput{
		IdempotencyKey: "retry-without-agent", TaskType: "mcp_scan", Content: "scan",
	})
	require.ErrorIs(t, err, platformtasks.ErrDispatchFailed)
	assert.Equal(t, platformtasks.StatusDispatchFailed, view.Status)
	assert.Equal(t, platformtasks.MaxDispatchAttempts, view.DispatchAttempts)

	legacy, err := taskManager.taskStore.GetSession(view.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskStatusTodo, legacy.Status)
	assert.Empty(t, legacy.AssignedAgent)
}

func TestPlatformSubmitTreatsOnlyAssignedTodoAsUnknownAcknowledgement(t *testing.T) {
	taskManager, cleanup := newTestTaskManager(t)
	defer cleanup()
	require.NoError(t, taskManager.taskStore.CreateSession(&database.Session{
		ID: "assigned-pending", Username: "alice", TaskType: "mcp_scan", Content: "scan",
		Status: TaskStatusTodo, AssignedAgent: "agent-that-may-have-received-it", Share: false,
	}))

	_, err := taskManager.SubmitTask(context.Background(), platformtasks.EngineTask{
		PlatformTaskID: "assigned-pending", OwnerUsername: "alice", TaskType: "mcp_scan", Content: "scan",
	})
	require.ErrorIs(t, err, platformtasks.ErrSubmitAcknowledgementUnknown)
}

func TestTaskManagerFailedAgentWriteLeavesObservableUnknownAssignment(t *testing.T) {
	taskManager, cleanup := newTestTaskManager(t)
	defer cleanup()
	pair := websocketConnectionPair(t)
	connection := NewAgentConnection(pair.server)
	connection.agentID = "failed-write"
	taskManager.agentManager.mu.Lock()
	taskManager.agentManager.connections[connection.agentID] = connection
	taskManager.agentManager.mu.Unlock()
	require.NoError(t, pair.server.Close())

	request := platformtasks.EngineTask{PlatformTaskID: "failed-agent-write", OwnerUsername: "alice", TaskType: "mcp_scan", Content: "scan"}
	_, err := taskManager.SubmitTask(context.Background(), request)
	require.ErrorIs(t, err, platformtasks.ErrSubmitAcknowledgementUnknown)
	session, err := taskManager.taskStore.GetSession(request.PlatformTaskID)
	require.NoError(t, err)
	assert.Equal(t, TaskStatusDispatchUnknown, session.Status)
	assert.Equal(t, connection.agentID, session.AssignedAgent)
}

func TestTaskManagerPayloadBuildFailureNeverClaimsAgentOrMarksRunning(t *testing.T) {
	taskManager, cleanup := newTestTaskManager(t)
	defer cleanup()
	pair := websocketConnectionPair(t)
	connection := NewAgentConnection(pair.server)
	connection.agentID = "available-agent"
	taskManager.agentManager.mu.Lock()
	taskManager.agentManager.connections[connection.agentID] = connection
	taskManager.agentManager.mu.Unlock()

	request := platformtasks.EngineTask{
		PlatformTaskID: "payload-build-failure", OwnerUsername: "alice", TaskType: "mcp_scan", Content: "scan",
		Params: json.RawMessage(`{"model_id":"missing-model"}`),
	}
	_, err := taskManager.SubmitTask(context.Background(), request)
	require.Error(t, err)
	session, loadErr := taskManager.taskStore.GetSession(request.PlatformTaskID)
	require.NoError(t, loadErr)
	assert.Equal(t, TaskStatusTodo, session.Status)
	assert.Empty(t, session.AssignedAgent)
}

func TestAgentEventsAreBoundToAuthenticatedAssignedConnection(t *testing.T) {
	taskManager, cleanup := newTestTaskManager(t)
	defer cleanup()
	ownerPair := websocketConnectionPair(t)
	attackerPair := websocketConnectionPair(t)
	owner := NewAgentConnection(ownerPair.server)
	owner.agentID = "assigned-agent"
	attacker := NewAgentConnection(attackerPair.server)
	attacker.agentID = "other-agent"
	taskManager.agentManager.mu.Lock()
	taskManager.agentManager.connections[owner.agentID] = owner
	taskManager.agentManager.connections[attacker.agentID] = attacker
	taskManager.agentManager.taskManager = taskManager
	taskManager.agentManager.mu.Unlock()

	require.NoError(t, taskManager.taskStore.CreateUser(&database.User{
		UserID: "event-owner-id", Username: "event-owner", Email: "event-owner@example.test", IsActive: true,
	}))
	mutationTypes := []string{
		WSMsgTypeLiveStatus,
		WSMsgTypePlanUpdate,
		WSMsgTypeNewPlanStep,
		WSMsgTypeStatusUpdate,
		WSMsgTypeToolUsed,
		WSMsgTypeActionLog,
		WSMsgTypeResultUpdate,
		WSMsgTypeError,
	}
	for _, eventType := range mutationTypes {
		t.Run(eventType, func(t *testing.T) {
			sessionID := "assigned-event-" + eventType
			require.NoError(t, taskManager.taskStore.CreateSession(&database.Session{
				ID: sessionID, Username: "event-owner", TaskType: "mcp_scan", Content: "scan",
				Status: TaskStatusDoing, AssignedAgent: owner.agentID, Share: false,
			}))
			attacker.handleAgentEvent(taskManager.agentManager, map[string]interface{}{
				"id": "forged-" + eventType, "type": eventType, "sessionId": sessionID,
				"timestamp": time.Now().UnixMilli(), "event": map[string]interface{}{"text": "forged"},
			}, eventType)

			events, err := taskManager.taskStore.GetSessionEvents(sessionID)
			require.NoError(t, err)
			assert.Empty(t, events, "an unassigned Agent must not persist %s", eventType)
			session, err := taskManager.taskStore.GetSession(sessionID)
			require.NoError(t, err)
			assert.Equal(t, TaskStatusDoing, session.Status)
			assert.Equal(t, owner.agentID, session.AssignedAgent)
		})
	}
}

func TestAssignedAgentEventCanResolveUnknownDispatch(t *testing.T) {
	taskManager, cleanup := newTestTaskManager(t)
	defer cleanup()
	require.NoError(t, taskManager.taskStore.CreateUser(&database.User{
		UserID: "unknown-owner-id", Username: "unknown-owner", Email: "unknown-owner@example.test", IsActive: true,
	}))
	require.NoError(t, taskManager.taskStore.CreateSession(&database.Session{
		ID: "unknown-assignment", Username: "unknown-owner", TaskType: "mcp_scan", Content: "scan",
		Status: TaskStatusDispatchUnknown, AssignedAgent: "assigned-agent", Share: false,
	}))

	accepted := taskManager.HandleAgentEvent("assigned-agent", "unknown-assignment", WSMsgTypeResultUpdate, map[string]interface{}{"result": "ok"})
	require.True(t, accepted)
	session, err := taskManager.taskStore.GetSession("unknown-assignment")
	require.NoError(t, err)
	assert.Equal(t, TaskStatusDone, session.Status)
}

func TestLegacyTerminateAndAssignedTerminalEventUseOneWayTerminalCAS(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		terminal    string
		eventType   string
		finalStatus string
	}{
		{name: "result wins before cancel", terminal: TaskStatusDone, eventType: WSMsgTypeResultUpdate, finalStatus: TaskStatusDone},
		{name: "error wins before cancel", terminal: TaskStatusError, eventType: WSMsgTypeError, finalStatus: TaskStatusError},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			taskManager, cleanup := newTestTaskManager(t)
			defer cleanup()
			const sessionID = "terminal-before-cancel"
			require.NoError(t, taskManager.taskStore.CreateSession(&database.Session{
				ID: sessionID, Username: "alice", TaskType: "mcp_scan", Content: "scan",
				Status: TaskStatusDoing, AssignedAgent: "assigned-agent", Share: false,
			}))
			accepted, err := taskManager.taskStore.StoreAssignedAgentEvent(
				"terminal-event", sessionID, "assigned-agent", testCase.eventType,
				map[string]any{"result": "terminal"}, time.Now().UnixMilli(), testCase.terminal,
			)
			require.NoError(t, err)
			require.True(t, accepted)

			require.NoError(t, taskManager.TerminateTask(sessionID, identity.Subject{Role: identity.RoleAdmin}, "cancel-after-terminal"))
			stored, err := taskManager.taskStore.GetSession(sessionID)
			require.NoError(t, err)
			assert.Equal(t, testCase.finalStatus, stored.Status)
		})
	}

	t.Run("cancel wins before late result", func(t *testing.T) {
		taskManager, cleanup := newTestTaskManager(t)
		defer cleanup()
		const sessionID = "cancel-before-terminal"
		require.NoError(t, taskManager.taskStore.CreateSession(&database.Session{
			ID: sessionID, Username: "alice", TaskType: "mcp_scan", Content: "scan",
			Status: TaskStatusDoing, AssignedAgent: "assigned-agent", Share: false,
		}))
		require.NoError(t, taskManager.TerminateTask(sessionID, identity.Subject{Role: identity.RoleAdmin}, "cancel-first"))
		accepted, err := taskManager.taskStore.StoreAssignedAgentEvent(
			"late-result", sessionID, "assigned-agent", WSMsgTypeResultUpdate,
			map[string]any{"result": "late"}, time.Now().UnixMilli(), TaskStatusDone,
		)
		require.NoError(t, err)
		assert.False(t, accepted)
		stored, err := taskManager.taskStore.GetSession(sessionID)
		require.NoError(t, err)
		assert.Equal(t, TaskStatusTerminated, stored.Status)
	})
}

func TestTaskStoreTerminateCASAndAssignedTerminalEventHaveSingleWinner(t *testing.T) {
	for _, first := range []string{"terminal", "cancel"} {
		t.Run(first+" wins", func(t *testing.T) {
			taskManager, cleanup := newTestTaskManager(t)
			defer cleanup()
			const sessionID = "store-terminal-cas"
			require.NoError(t, taskManager.taskStore.CreateSession(&database.Session{
				ID: sessionID, Username: "alice", TaskType: "mcp_scan", Content: "scan",
				Status: TaskStatusDoing, AssignedAgent: "assigned-agent", Share: false,
			}))

			if first == "terminal" {
				accepted, err := taskManager.taskStore.StoreAssignedAgentEvent(
					"terminal-first", sessionID, "assigned-agent", WSMsgTypeResultUpdate,
					map[string]any{"result": "done"}, time.Now().UnixMilli(), TaskStatusDone,
				)
				require.NoError(t, err)
				require.True(t, accepted)
				terminated, current, err := taskManager.taskStore.TerminateSessionCAS(sessionID)
				require.NoError(t, err)
				assert.False(t, terminated)
				assert.Equal(t, TaskStatusDone, current.Status)
			} else {
				terminated, current, err := taskManager.taskStore.TerminateSessionCAS(sessionID)
				require.NoError(t, err)
				require.True(t, terminated)
				assert.Equal(t, TaskStatusTerminated, current.Status)
				accepted, err := taskManager.taskStore.StoreAssignedAgentEvent(
					"late-terminal", sessionID, "assigned-agent", WSMsgTypeResultUpdate,
					map[string]any{"result": "late"}, time.Now().UnixMilli(), TaskStatusDone,
				)
				require.NoError(t, err)
				assert.False(t, accepted)
			}

			stored, err := taskManager.taskStore.GetSession(sessionID)
			require.NoError(t, err)
			if first == "terminal" {
				assert.Equal(t, TaskStatusDone, stored.Status)
			} else {
				assert.Equal(t, TaskStatusTerminated, stored.Status)
			}
		})
	}
}

func TestTaskStoreConcurrentTerminateAndAssignedTerminalEventHaveExactlyOneWinner(t *testing.T) {
	taskManager, cleanup := newTestTaskManager(t)
	defer cleanup()
	const sessionID = "concurrent-terminal-cas"
	require.NoError(t, taskManager.taskStore.CreateSession(&database.Session{
		ID: sessionID, Username: "alice", TaskType: "mcp_scan", Content: "scan",
		Status: TaskStatusDoing, AssignedAgent: "assigned-agent", Share: false,
	}))

	start := make(chan struct{})
	type outcome struct {
		won bool
		err error
	}
	cancelResult := make(chan outcome, 1)
	terminalResult := make(chan outcome, 1)
	go func() {
		<-start
		won, _, err := taskManager.taskStore.TerminateSessionCAS(sessionID)
		cancelResult <- outcome{won: won, err: err}
	}()
	go func() {
		<-start
		won, err := taskManager.taskStore.StoreAssignedAgentEvent(
			"concurrent-result", sessionID, "assigned-agent", WSMsgTypeResultUpdate,
			map[string]any{"result": "done"}, time.Now().UnixMilli(), TaskStatusDone,
		)
		terminalResult <- outcome{won: won, err: err}
	}()
	close(start)
	cancel := <-cancelResult
	terminal := <-terminalResult
	require.NoError(t, cancel.err)
	require.NoError(t, terminal.err)
	assert.NotEqual(t, cancel.won, terminal.won, "exactly one terminal CAS must win")

	stored, err := taskManager.taskStore.GetSession(sessionID)
	require.NoError(t, err)
	if cancel.won {
		assert.Equal(t, TaskStatusTerminated, stored.Status)
	} else {
		assert.Equal(t, TaskStatusDone, stored.Status)
	}
}

func TestPlatformCancelTreatsLegacyTerminalAsIdempotentAndConvergesTrustedState(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		legacyStatus string
		engineState  platformtasks.EngineState
		platformWant platformtasks.Status
	}{
		{name: "completed", legacyStatus: TaskStatusDone, engineState: platformtasks.EngineStateSucceeded, platformWant: platformtasks.StatusSucceeded},
		{name: "failed", legacyStatus: TaskStatusError, engineState: platformtasks.EngineStateFailed, platformWant: platformtasks.StatusEngineFailed},
		{name: "cancelled", legacyStatus: TaskStatusDoing, engineState: platformtasks.EngineStateCancelled, platformWant: platformtasks.StatusCancelled},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			taskManager, cleanup := newTestTaskManager(t)
			defer cleanup()
			repository := platformtasks.NewMemoryRepository()
			auditRepository := platformaudit.NewMemoryRepository()
			service := platformtasks.NewService(repository, taskManager, platformaudit.NewService(auditRepository))
			taskManager.SetPlatformTaskEventSink(service)
			const taskID = "legacy-terminal-platform-cancel"
			now := time.Now().UTC()
			_, _, err := repository.CreateOrGet(context.Background(), &platformtasks.Task{
				ID: taskID, OwnerUserID: "owner-id", OwnerUsername: "alice", IdempotencyKey: taskID,
				EngineSessionID: taskID, TaskType: "mcp_scan", Content: "scan", Params: json.RawMessage(`{}`),
				AttachmentRefs: json.RawMessage(`[]`), Status: platformtasks.StatusRunning, CreatedAt: now, UpdatedAt: now,
			})
			require.NoError(t, err)
			require.NoError(t, taskManager.taskStore.CreateSession(&database.Session{
				ID: taskID, Username: "alice", TaskType: "mcp_scan", Content: "scan",
				Status: testCase.legacyStatus, AssignedAgent: "assigned-agent", Share: false,
			}))

			require.NoError(t, service.Cancel(context.Background(), identity.Subject{UserID: "owner-id", Username: "alice", Role: identity.RoleUser}, taskID))
			require.NoError(t, service.RecordEngineEvent(context.Background(), taskID, testCase.engineState, ""))
			stored, err := repository.Get(context.Background(), taskID)
			require.NoError(t, err)
			assert.Equal(t, testCase.platformWant, stored.Status)

			auditEvents, err := auditRepository.List(context.Background(), platformaudit.Filter{ResourceID: taskID})
			require.NoError(t, err)
			cancelRequested := false
			changedSuccess := false
			for _, event := range auditEvents {
				assert.False(t, event.Action == platformaudit.ActionTaskCancelled && event.Outcome == platformaudit.OutcomeSuccess,
					"a terminal engine readback must not be audited as a successful cancellation")
				if event.Action == platformaudit.ActionTaskCancelRequested && event.Outcome == platformaudit.OutcomePending {
					cancelRequested = true
				}
				if event.Action == platformaudit.ActionTaskChanged && event.Outcome == platformaudit.OutcomeSuccess {
					changedSuccess = true
					assert.Contains(t, string(event.Metadata), string(testCase.platformWant))
					assert.Contains(t, string(event.Metadata), `"requested_action":"cancel"`)
				}
			}
			assert.True(t, cancelRequested, "the cancellation intent must be durably audited before invoking the engine")
			assert.True(t, changedSuccess, "the trusted terminal readback must have a truthful completion audit event")
		})
	}
}

func TestPlatformCancelCrossesDispatchBoundaryThroughLegacyEngineCAS(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		platformStatus platformtasks.Status
		legacyStatus   string
		platformWant   platformtasks.Status
		legacyWant     string
	}{
		{
			name: "unknown assignment is terminated", platformStatus: platformtasks.StatusDispatchUnknown,
			legacyStatus: TaskStatusDispatchUnknown, platformWant: platformtasks.StatusCancelled, legacyWant: TaskStatusTerminated,
		},
		{
			name: "dispatching assignment is terminated", platformStatus: platformtasks.StatusDispatching,
			legacyStatus: TaskStatusTodo, platformWant: platformtasks.StatusCancelled, legacyWant: TaskStatusTerminated,
		},
		{
			name: "completed engine wins", platformStatus: platformtasks.StatusDispatchUnknown,
			legacyStatus: TaskStatusDone, platformWant: platformtasks.StatusSucceeded, legacyWant: TaskStatusDone,
		},
		{
			name: "failed engine wins", platformStatus: platformtasks.StatusDispatchUnknown,
			legacyStatus: TaskStatusError, platformWant: platformtasks.StatusEngineFailed, legacyWant: TaskStatusError,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			taskManager, cleanup := newTestTaskManager(t)
			defer cleanup()
			repository := platformtasks.NewMemoryRepository()
			service := platformtasks.NewService(repository, taskManager, platformaudit.NewService(platformaudit.NewMemoryRepository()))
			taskManager.SetPlatformTaskEventSink(service)
			taskID := "cancel-dispatch-boundary-" + strings.ReplaceAll(testCase.name, " ", "-")
			now := time.Now().UTC()
			_, _, err := repository.CreateOrGet(context.Background(), &platformtasks.Task{
				ID: taskID, OwnerUserID: "dispatch-owner-id", OwnerUsername: "dispatch-owner", IdempotencyKey: taskID,
				EngineSessionID: taskID, TaskType: "mcp_scan", Content: "scan", Params: json.RawMessage(`{}`),
				AttachmentRefs: json.RawMessage(`[]`), Status: testCase.platformStatus, CreatedAt: now, UpdatedAt: now,
			})
			require.NoError(t, err)
			require.NoError(t, taskManager.taskStore.CreateSession(&database.Session{
				ID: taskID, Username: "dispatch-owner", TaskType: "mcp_scan", Content: "scan",
				Status: testCase.legacyStatus, AssignedAgent: "assigned-agent", Share: false,
			}))

			err = service.Cancel(context.Background(), identity.Subject{
				UserID: "dispatch-owner-id", Username: "dispatch-owner", Role: identity.RoleUser,
			}, taskID)
			require.NoError(t, err)
			platformTask, err := repository.Get(context.Background(), taskID)
			require.NoError(t, err)
			assert.Equal(t, testCase.platformWant, platformTask.Status)
			legacyTask, err := taskManager.taskStore.GetSession(taskID)
			require.NoError(t, err)
			assert.Equal(t, testCase.legacyWant, legacyTask.Status)

			accepted := taskManager.HandleAgentEvent("assigned-agent", taskID, WSMsgTypeResultUpdate, map[string]interface{}{"result": "late"})
			assert.False(t, accepted, "a terminal cancellation/readback must reject a late Agent result")
			platformTask, err = repository.Get(context.Background(), taskID)
			require.NoError(t, err)
			assert.Equal(t, testCase.platformWant, platformTask.Status)
		})
	}
}

func TestAgentFailureReasonIsSanitizedBeforeLegacyPlatformAndAuditPersistence(t *testing.T) {
	taskManager, cleanup := newTestTaskManager(t)
	defer cleanup()
	platformRepository := platformtasks.NewMemoryRepository()
	auditRepository := platformaudit.NewMemoryRepository()
	platformService := platformtasks.NewService(platformRepository, taskManager, platformaudit.NewService(auditRepository))
	taskManager.SetPlatformTaskEventSink(platformService)
	now := time.Now().UTC()
	platformTask := &platformtasks.Task{
		ID: "sanitized-agent-failure", OwnerUserID: "sanitized-owner-id", OwnerUsername: "sanitized-owner",
		IdempotencyKey: "sanitized-agent-failure", EngineSessionID: "sanitized-agent-failure",
		TaskType: "mcp_scan", Content: "scan", Params: json.RawMessage(`{}`), AttachmentRefs: json.RawMessage(`[]`),
		Status: platformtasks.StatusRunning, CreatedAt: now, UpdatedAt: now,
	}
	_, _, err := platformRepository.CreateOrGet(context.Background(), platformTask)
	require.NoError(t, err)
	require.NoError(t, taskManager.taskStore.CreateUser(&database.User{
		UserID: platformTask.OwnerUserID, Username: platformTask.OwnerUsername, Email: "sanitized-owner@example.test", IsActive: true,
	}))
	require.NoError(t, taskManager.taskStore.CreateSession(&database.Session{
		ID: platformTask.EngineSessionID, Username: platformTask.OwnerUsername, TaskType: "mcp_scan", Content: "scan",
		Status: TaskStatusDoing, AssignedAgent: "assigned-agent", Share: false,
	}))
	sentinel := `agent failed with token sk-secret-sentinel at C:\private\result.json`
	require.True(t, taskManager.HandleAgentEvent("assigned-agent", platformTask.EngineSessionID, WSMsgTypeError, map[string]interface{}{"message": sentinel, "stack": sentinel}))

	legacyEvents, err := taskManager.taskStore.GetSessionEventsByType(platformTask.EngineSessionID, WSMsgTypeError)
	require.NoError(t, err)
	require.Len(t, legacyEvents, 1)
	assert.NotContains(t, string(legacyEvents[0].EventData), sentinel)
	stored, err := platformRepository.Get(context.Background(), platformTask.ID)
	require.NoError(t, err)
	assert.Equal(t, platformtasks.StatusEngineFailed, stored.Status)
	assert.Equal(t, "agent reported task failure", stored.DispatchError)
	view, err := platformService.Get(context.Background(), identity.Subject{UserID: platformTask.OwnerUserID, Role: identity.RoleUser}, platformTask.ID)
	require.NoError(t, err)
	viewJSON, err := json.Marshal(view)
	require.NoError(t, err)
	assert.NotContains(t, string(viewJSON), sentinel)
	auditEvents, err := auditRepository.List(context.Background(), platformaudit.Filter{ResourceID: platformTask.ID})
	require.NoError(t, err)
	auditJSON, err := json.Marshal(auditEvents)
	require.NoError(t, err)
	assert.NotContains(t, string(auditJSON), sentinel)
}

func TestAgentCleanupFailsOnlyItsCurrentAssignmentsWithSanitizedReason(t *testing.T) {
	taskManager, cleanup := newTestTaskManager(t)
	defer cleanup()
	platformRepository := platformtasks.NewMemoryRepository()
	auditRepository := platformaudit.NewMemoryRepository()
	platformService := platformtasks.NewService(platformRepository, taskManager, platformaudit.NewService(auditRepository))
	taskManager.SetPlatformTaskEventSink(platformService)
	firstPair := websocketConnectionPair(t)
	secondPair := websocketConnectionPair(t)
	first := NewAgentConnection(firstPair.server)
	first.agentID = "disconnect-agent"
	second := NewAgentConnection(secondPair.server)
	second.agentID = "healthy-agent"
	taskManager.agentManager.mu.Lock()
	taskManager.agentManager.connections[first.agentID] = first
	taskManager.agentManager.connections[second.agentID] = second
	taskManager.agentManager.taskManager = taskManager
	taskManager.agentManager.mu.Unlock()
	require.NoError(t, taskManager.taskStore.CreateUser(&database.User{
		UserID: "disconnect-owner-id", Username: "disconnect-owner", Email: "disconnect-owner@example.test", IsActive: true,
	}))
	for id, agentID := range map[string]string{"lost-task": first.agentID, "healthy-task": second.agentID} {
		require.NoError(t, taskManager.taskStore.CreateSession(&database.Session{
			ID: id, Username: "disconnect-owner", TaskType: "mcp_scan", Content: "scan",
			Status: TaskStatusDoing, AssignedAgent: agentID, Share: false,
		}))
		platformTask := &platformtasks.Task{
			ID: id, OwnerUserID: "disconnect-owner-id", OwnerUsername: "disconnect-owner",
			IdempotencyKey: id, EngineSessionID: id, TaskType: "mcp_scan", Content: "scan",
			Params: json.RawMessage(`{}`), AttachmentRefs: json.RawMessage(`[]`), Status: platformtasks.StatusRunning,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		_, _, err := platformRepository.CreateOrGet(context.Background(), platformTask)
		require.NoError(t, err)
	}

	first.cleanup(taskManager.agentManager)
	lost, err := taskManager.taskStore.GetSession("lost-task")
	require.NoError(t, err)
	assert.Equal(t, TaskStatusError, lost.Status)
	healthy, err := taskManager.taskStore.GetSession("healthy-task")
	require.NoError(t, err)
	assert.Equal(t, TaskStatusDoing, healthy.Status)
	events, err := taskManager.taskStore.GetSessionEventsByType("lost-task", WSMsgTypeError)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Contains(t, string(events[0].EventData), "agent connection lost")
	assert.NotContains(t, string(events[0].EventData), "127.0.0.1")
	failedPlatformTask, err := platformRepository.Get(context.Background(), "lost-task")
	require.NoError(t, err)
	assert.Equal(t, platformtasks.StatusEngineFailed, failedPlatformTask.Status)
	assert.Equal(t, "agent connection lost", failedPlatformTask.DispatchError)
	healthyPlatformTask, err := platformRepository.Get(context.Background(), "healthy-task")
	require.NoError(t, err)
	assert.Equal(t, platformtasks.StatusRunning, healthyPlatformTask.Status)

	// A stale cleanup after a replacement registration must not fail the new
	// connection's assignment or emit platform/audit mutations.
	replacementPair := websocketConnectionPair(t)
	replacement := NewAgentConnection(replacementPair.server)
	replacement.agentID = first.agentID
	taskManager.agentManager.mu.Lock()
	taskManager.agentManager.connections[first.agentID] = replacement
	taskManager.agentManager.mu.Unlock()
	require.NoError(t, taskManager.taskStore.CreateSession(&database.Session{
		ID: "replacement-task", Username: "disconnect-owner", TaskType: "mcp_scan", Content: "scan",
		Status: TaskStatusDoing, AssignedAgent: first.agentID, Share: false,
	}))
	first.cleanup(taskManager.agentManager)
	replacementTask, err := taskManager.taskStore.GetSession("replacement-task")
	require.NoError(t, err)
	assert.Equal(t, TaskStatusDoing, replacementTask.Status)
}

type testWebSocketPair struct {
	server *websocket.Conn
	client *websocket.Conn
}

func websocketConnectionPair(t *testing.T) testWebSocketPair {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	serverConnections := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		serverConnections <- connection
	}))
	t.Cleanup(server.Close)
	client, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http://", "ws://", 1), nil)
	require.NoError(t, err)
	pair := testWebSocketPair{server: <-serverConnections, client: client}
	t.Cleanup(func() {
		pair.client.Close()
		pair.server.Close()
	})
	return pair
}

func init() { gin.SetMode(gin.TestMode) }
