package apidocs

import "testing"

func TestSwaggerDedicatedMCPBoundaries(t *testing.T) {
	for name, document := range loadSwaggerDocuments(t) {
		t.Run(name, func(t *testing.T) {
			for _, path := range []string{"/api/v1/platform/mcp-scans", "/api/v1/platform/mcp-scans/{taskID}", "/api/v1/platform/mcp-scans/{taskID}/cancel", "/api/v1/platform/mcp-connection-configs", "/api/v1/platform/mcp-connection-configs/{configID}", "/api/v1/platform/mcp-connection-configs/{configID}/test", "/api/v1/platform/mcp-connection-options", "/api/v1/platform/mcp-scan-attachments", "/api/v1/platform/mcp-scan-attachments/chunked", "/api/v1/platform/mcp-scan-attachments/{attachmentID}/chunks", "/api/v1/platform/mcp-scan-attachments/{attachmentID}/merge", "/api/v1/platform/mcp-scan-attachments/{attachmentID}"} {
				_ = swaggerValue(t, document, "paths", path)
			}
			assertExactProperties(t, document, "mcpscans.CreateRequest", "source_kind", "repository_url", "attachment_ids", "connection_config_id", "connection_config_version", "authorization_confirmed", "model_id", "thread")
			assertExactProperties(t, document, "mcpscans.MutationResponse", "task_id", "status")
			for _, path := range []string{"/api/v1/platform/tasks", "/api/v1/platform/tasks/{taskID}", "/api/v1/platform/tasks/{taskID}/cancel"} {
				operations := swaggerValue(t, document, "paths", path).(map[string]interface{})
				for _, operation := range operations {
					_ = swaggerValue(t, operation, "responses", "409")
				}
			}
		})
	}
}
