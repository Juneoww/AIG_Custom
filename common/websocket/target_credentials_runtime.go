package websocket

import (
	"encoding/json"

	"github.com/Juneoww/AIG_Custom/common/agent"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/Juneoww/AIG_Custom/pkg/httpx"
)

func isInfrastructureTask(taskType string) bool {
	return taskType == "ai_infra_scan" || taskType == agent.TaskTypeAIInfraScan
}
func hasTargetCredential(params map[string]interface{}) bool {
	_, exists := params["target_credential_id"]
	return exists
}
func supportsTargetCredentials(connection *AgentConnection) bool {
	connection.stateMu.RLock()
	defer connection.stateMu.RUnlock()
	for _, capability := range connection.capabilities {
		if capability == agent.TargetCredentialCapability {
			return true
		}
	}
	return false
}
func (tm *TaskManager) rememberTargetRedactor(sessionID string, runtime map[string]any) {
	auth, err := tasks.InfrastructureRuntimeAuth(runtime)
	if err == nil {
		tm.targetRedactors.Store(sessionID, auth)
	}
}

// 在任何事件存储、报告汇总或 SSE 输出前删除运行时认证值；重启失去上下文时关闭接收。
func (tm *TaskManager) redactTargetEvent(session *database.Session, event any) (any, bool) {
	if !isInfrastructureTask(session.TaskType) || !tasks.HasInfrastructureTargetCredential(json.RawMessage(session.Params)) {
		return event, true
	}
	value, ok := tm.targetRedactors.Load(session.ID)
	if !ok {
		return nil, false
	}
	auth, ok := value.(*httpx.TargetAuth)
	if !ok || auth == nil {
		return nil, false
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return nil, false
	}
	var data any
	if json.Unmarshal(raw, &data) != nil {
		return nil, false
	}
	var redact func(any) any
	redact = func(value any) any {
		switch item := value.(type) {
		case string:
			return auth.Redact(item)
		case []any:
			for i := range item {
				item[i] = redact(item[i])
			}
			return item
		case map[string]any:
			result := map[string]any{}
			for k, v := range item {
				result[auth.Redact(k)] = redact(v)
			}
			return result
		default:
			return value
		}
	}
	return redact(data), true
}
