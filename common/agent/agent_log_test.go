package agent

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/gologger"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// TestAgentReceiveDoesNotLogPayload 通过真实 WebSocket 接收任务，验证日志不记录凭据和原始说明。
func TestAgentReceiveDoesNotLogPayload(t *testing.T) {
	const secret = "controlled-dispatch-secret"
	const instruction = "private workflow instructions"
	var output bytes.Buffer
	logger := gologger.StdLogger.Logrus()
	oldOutput := logger.Out
	logger.SetOutput(&output)
	defer logger.SetOutput(oldOutput)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.WriteMessage(websocket.TextMessage, []byte(`{"type":"task_assign","content":{"sessionId":"local-task","taskType":"unregistered","content":"`+instruction+`","params":{"token":"`+secret+`","agent_data":"private provider"}}}`))
	}))
	defer server.Close()
	agent := NewAgent(AgentConfig{ServerURL: strings.Replace(server.URL, "http://", "ws://", 1), AgentToken: "local-agent-token"})
	require.NoError(t, agent.connect())
	defer agent.Stop()
	done := make(chan struct{})
	go func() { agent.handleReceive(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("receive did not finish")
	}
	require.NotContains(t, output.String(), secret)
	require.NotContains(t, output.String(), instruction)
	require.NotContains(t, output.String(), "private provider")
}
