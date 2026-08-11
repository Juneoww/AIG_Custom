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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/common/agent"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	trpclog "trpc.group/trpc-go/trpc-go/log"
)

func TestDispatchTaskDoesNotLogAgentPayloadSecrets(t *testing.T) {
	tm, cleanup := newTestTaskManager(t)
	defer cleanup()

	const (
		sessionID        = "dispatch-log-session"
		username         = "dispatch-log-user"
		targetToken      = "dispatch-log-target-token"
		targetBaseURL    = "https://target.dispatch-log.invalid/v1"
		evaluatorToken   = "dispatch-log-evaluator-token"
		evaluatorBaseURL = "https://evaluator.dispatch-log.invalid/v1"
		sentinelContent  = "dispatch-log-sensitive-content"
	)

	require.NoError(t, tm.taskStore.CreateUser(&database.User{
		UserID: username + "-id", Username: username, Email: username + "@example.test", IsActive: true,
	}))
	require.NoError(t, tm.taskStore.CreateSession(&database.Session{
		ID: sessionID, Username: username, Title: "dispatch log test", TaskType: agent.TaskTypeModelRedteamReport,
		Content: sentinelContent, Status: TaskStatusTodo,
	}))

	tm.mu.Lock()
	tm.tasks[sessionID] = &TaskCreateRequest{
		ID: "dispatch-log-message", SessionID: sessionID, Username: username, Task: agent.TaskTypeModelRedteamReport,
		Content: sentinelContent, Params: map[string]interface{}{
			"model": []ModelParams{{
				Model: "dispatch-log-target", Token: targetToken, BaseUrl: targetBaseURL, Limit: 17,
			}},
			"eval_model": ModelParams{
				Model: "dispatch-log-evaluator", Token: evaluatorToken, BaseUrl: evaluatorBaseURL, Limit: 23,
			},
		},
	}
	tm.mu.Unlock()

	serverConnCh := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, upgradeErr := upgrader.Upgrade(w, r, nil)
		if upgradeErr != nil {
			http.Error(w, upgradeErr.Error(), http.StatusInternalServerError)
			return
		}
		serverConnCh <- conn
	}))
	defer server.Close()

	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer clientConn.Close()

	select {
	case serverConn := <-serverConnCh:
		defer serverConn.Close()
		tm.agentManager.mu.Lock()
		tm.agentManager.connections["dispatch-log-agent"] = &AgentConnection{
			conn: serverConn, agentID: "dispatch-log-agent", isActive: true,
		}
		tm.agentManager.mu.Unlock()
	case <-time.After(3 * time.Second):
		t.Fatal("agent websocket server connection was not established")
	}

	previousLogger := trpclog.GetDefaultLogger()
	capturedLogger := &capturedTaskDispatchLogger{Logger: previousLogger}
	trpclog.SetLogger(capturedLogger)
	defer trpclog.SetLogger(previousLogger)

	require.NoError(t, tm.dispatchTask(sessionID, "dispatch-log-trace"))
	clientConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, payload, err := clientConn.ReadMessage()
	require.NoError(t, err)
	var assigned struct {
		Type    string `json:"type"`
		Content struct {
			SessionID string `json:"sessionId"`
			TaskType  string `json:"taskType"`
			Content   string `json:"content"`
			Params    struct {
				Model     []ModelParams `json:"model"`
				EvalModel ModelParams   `json:"eval_model"`
			} `json:"params"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(payload, &assigned))
	assert.Equal(t, WSMsgTypeTaskAssign, assigned.Type)
	assert.Equal(t, sessionID, assigned.Content.SessionID)
	assert.Equal(t, agent.TaskTypeModelRedteamReport, assigned.Content.TaskType)
	assert.Equal(t, sentinelContent, assigned.Content.Content)
	require.Len(t, assigned.Content.Params.Model, 1)
	assert.Equal(t, targetToken, assigned.Content.Params.Model[0].Token)
	assert.Equal(t, targetBaseURL, assigned.Content.Params.Model[0].BaseUrl)
	assert.Equal(t, evaluatorToken, assigned.Content.Params.EvalModel.Token)
	assert.Equal(t, evaluatorBaseURL, assigned.Content.Params.EvalModel.BaseUrl)

	logs := capturedLogger.String()
	assert.NotContains(t, logs, targetToken)
	assert.NotContains(t, logs, targetBaseURL)
	assert.NotContains(t, logs, evaluatorToken)
	assert.NotContains(t, logs, evaluatorBaseURL)
	assert.NotContains(t, logs, sentinelContent)
}

type capturedTaskDispatchLogger struct {
	trpclog.Logger
	mu       sync.Mutex
	messages []string
}

func (logger *capturedTaskDispatchLogger) Infof(format string, args ...interface{}) {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	logger.messages = append(logger.messages, fmt.Sprintf(format, args...))
}

func (logger *capturedTaskDispatchLogger) String() string {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	return strings.Join(logger.messages, "\n")
}
