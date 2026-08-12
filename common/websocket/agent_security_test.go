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

	"github.com/gin-gonic/gin"
	gorilla "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testInternalAgentToken = "test-internal-agent-token"

func TestAgentWebSocketRequiresInternalTokenAndRejectsBrowserIdentityHeaders(t *testing.T) {
	manager, server := newAgentSecurityServer(t)
	_ = manager
	for _, fixture := range []struct {
		name       string
		token      string
		headers    http.Header
		wantStatus int
	}{
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "wrong", token: "wrong-token", wantStatus: http.StatusForbidden},
		{name: "browser headers", headers: http.Header{"username": []string{"admin"}, "role": []string{"admin"}, "Cookie": []string{"aig_session=fake"}}, wantStatus: http.StatusUnauthorized},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			headers := fixture.headers.Clone()
			if headers == nil {
				headers = http.Header{}
			}
			if fixture.token != "" {
				headers.Set(InternalAgentTokenHeader, fixture.token)
			}
			connection, response, err := gorilla.DefaultDialer.Dial(agentWebSocketURL(server.URL), headers)
			if connection != nil {
				connection.Close()
			}
			require.Error(t, err)
			require.NotNil(t, response)
			assert.Equal(t, fixture.wantStatus, response.StatusCode)
		})
	}
}

func TestInternalAgentTokenEnvironmentFailsClosedWithoutLeakingValue(t *testing.T) {
	t.Setenv("AIG_AGENT_TOKEN", "")
	_, err := LoadInternalAgentTokenFromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AIG_AGENT_TOKEN")

	const secret = "server-internal-secret"
	t.Setenv("AIG_AGENT_TOKEN", "  "+secret+"  ")
	token, err := LoadInternalAgentTokenFromEnv()
	require.NoError(t, err)
	assert.Equal(t, secret, token)
	assert.NotContains(t, fmt.Sprint(NewAgentManager(token)), secret)
}

func TestDuplicateAgentIDCannotReplaceActiveConnection(t *testing.T) {
	manager, server := newAgentSecurityServer(t)
	first := dialInternalAgent(t, server.URL)
	defer first.Close()
	registerAgent(t, first, "duplicate-id")
	assert.Equal(t, "register_ack", readAgentResponseType(t, first))

	manager.mu.RLock()
	original := manager.connections["duplicate-id"]
	manager.mu.RUnlock()
	require.NotNil(t, original)

	second := dialInternalAgent(t, server.URL)
	defer second.Close()
	registerAgent(t, second, "duplicate-id")
	assert.Equal(t, "error", readAgentResponseType(t, second))
	time.Sleep(50 * time.Millisecond)

	manager.mu.RLock()
	current := manager.connections["duplicate-id"]
	manager.mu.RUnlock()
	assert.Same(t, original, current)
	original.stateMu.RLock()
	assert.True(t, original.isActive)
	original.stateMu.RUnlock()
}

func TestConcurrentDuplicateAgentRegistrationAdmitsExactlyOne(t *testing.T) {
	manager, server := newAgentSecurityServer(t)
	connections := []*gorilla.Conn{dialInternalAgent(t, server.URL), dialInternalAgent(t, server.URL)}
	for _, connection := range connections {
		defer connection.Close()
	}
	start := make(chan struct{})
	responses := make(chan string, 2)
	var calls sync.WaitGroup
	for _, connection := range connections {
		calls.Add(1)
		go func(connection *gorilla.Conn) {
			defer calls.Done()
			<-start
			registerAgent(t, connection, "concurrent-id")
			responses <- readAgentResponseType(t, connection)
		}(connection)
	}
	close(start)
	calls.Wait()
	close(responses)
	counts := map[string]int{}
	for response := range responses {
		counts[response]++
	}
	assert.Equal(t, 1, counts["register_ack"])
	assert.Equal(t, 1, counts["error"])
	manager.mu.RLock()
	assert.Len(t, manager.connections, 1)
	manager.mu.RUnlock()
}

func newAgentSecurityServer(t *testing.T) (*AgentManager, *httptest.Server) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	manager := NewAgentManager(testInternalAgentToken)
	router := gin.New()
	router.GET("/api/v1/agents/ws", manager.HandleAgentWebSocket())
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return manager, server
}

func dialInternalAgent(t *testing.T, serverURL string) *gorilla.Conn {
	t.Helper()
	headers := http.Header{}
	headers.Set(InternalAgentTokenHeader, testInternalAgentToken)
	connection, _, err := gorilla.DefaultDialer.Dial(agentWebSocketURL(serverURL), headers)
	require.NoError(t, err)
	return connection
}

func agentWebSocketURL(serverURL string) string {
	return strings.Replace(serverURL, "http://", "ws://", 1) + "/api/v1/agents/ws"
}

func registerAgent(t *testing.T, connection *gorilla.Conn, id string) {
	t.Helper()
	require.NoError(t, connection.WriteJSON(WSMessage{Type: WSMsgTypeRegister, Content: AgentRegisterContent{
		AgentID: id, Hostname: "host", IP: "127.0.0.1", Version: "1",
	}}))
}

func readAgentResponseType(t *testing.T, connection *gorilla.Conn) string {
	t.Helper()
	require.NoError(t, connection.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, payload, err := connection.ReadMessage()
	require.NoError(t, err)
	var message WSMessage
	require.NoError(t, json.Unmarshal(payload, &message))
	return message.Type
}
