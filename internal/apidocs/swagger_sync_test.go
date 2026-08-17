package apidocs

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGeneratedSwaggerArtifactsStayInSync(t *testing.T) {
	embedded := decodeSwaggerJSON(t, []byte(SwaggerInfo.ReadDoc()))

	jsonData, err := os.ReadFile("swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	jsonDocument := decodeSwaggerJSON(t, jsonData)
	if difference := swaggerDocumentsDifference(embedded, jsonDocument); difference != "" {
		t.Fatalf("docs.go and swagger.json are not synchronized: %s", difference)
	}

	yamlData, err := os.ReadFile("swagger.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var rawYAMLDocument map[string]interface{}
	if err := yaml.Unmarshal(yamlData, &rawYAMLDocument); err != nil {
		t.Fatal(err)
	}
	normalizedYAML, err := json.Marshal(rawYAMLDocument)
	if err != nil {
		t.Fatal(err)
	}
	yamlDocument := decodeSwaggerJSON(t, normalizedYAML)
	if difference := swaggerDocumentsDifference(yamlDocument, jsonDocument); difference != "" {
		t.Fatalf("swagger.yaml and swagger.json are not synchronized: %s", difference)
	}
	for _, path := range [][]string{
		{"info", "title"},
		{"info", "description"},
		{"paths", "/api/v1/app/taskapi/status/{id}", "get", "description"},
		{"paths", "/api/v1/app/taskapi/result/{id}", "get", "description"},
	} {
		jsonValue := swaggerValue(t, jsonDocument, path...)
		yamlValue := swaggerValue(t, yamlDocument, path...)
		if !reflect.DeepEqual(jsonValue, yamlValue) {
			t.Fatalf("swagger authorization documentation differs at %s", strings.Join(path, "."))
		}
	}
	for _, document := range []interface{}{embedded, jsonDocument, yamlDocument} {
		resetRequest := swaggerValue(t, document, "paths", "/api/v1/auth/password-resets/{userID}", "post", "description")
		if !strings.Contains(resetRequest.(string), "never returns a reset token") {
			t.Fatal("password-reset request documentation must state that HTTP never returns a token")
		}
		resetConfirm := swaggerValue(t, document, "paths", "/api/v1/auth/password-resets/confirm", "post", "description")
		if !strings.Contains(resetConfirm.(string), "never echoed") {
			t.Fatal("password-reset confirmation documentation must state that the token is never echoed")
		}
		for _, endpoint := range []struct {
			path, method string
		}{
			{"/api/v1/platform/admin/users", "post"},
			{"/api/v1/platform/admin/audit-events", "get"},
			{"/api/v1/platform/admin/audit-events/prepared/{requestID}/finalize", "post"},
			{"/api/v1/platform/admin/audit-events/reconcile", "post"},
			{"/api/v1/platform/models", "get"},
			{"/api/v1/platform/models", "post"},
			{"/api/v1/platform/models/{modelID}", "get"},
			{"/api/v1/platform/tasks", "post"},
			{"/api/v1/platform/tasks", "get"},
			{"/api/v1/platform/tasks/{taskID}", "get"},
			{"/api/v1/platform/tasks/{taskID}/result", "get"},
			{"/api/v1/platform/tasks/{taskID}/cancel", "post"},
			{"/api/v1/platform/tasks/attachments", "post"},
			{"/api/v1/platform/tasks/attachments/chunked", "post"},
			{"/api/v1/platform/tasks/attachments/{attachmentID}/chunks", "post"},
			{"/api/v1/platform/tasks/attachments/{attachmentID}/merge", "post"},
			{"/api/v1/platform/tasks/attachments/{attachmentID}/download", "get"},
		} {
			_ = swaggerValue(t, document, "paths", endpoint.path, endpoint.method)
		}
		createDescription := swaggerValue(t, document, "paths", "/api/v1/platform/tasks", "post", "description").(string)
		for _, required := range []string{"Idempotency-Key", "Cookie", "raw model credentials", "opaque attachment IDs"} {
			if !strings.Contains(createDescription, required) {
				t.Fatalf("platform task create documentation must contain %q", required)
			}
		}
		for _, legacy := range []struct{ path, method string }{
			{"/api/v1/app/taskapi/tasks", "post"},
			{"/api/v1/app/taskapi/status/{id}", "get"},
			{"/api/v1/app/taskapi/result/{id}", "get"},
			{"/api/v1/app/taskapi/upload", "post"},
			{"/api/v1/app/taskapi/uploadChunk", "post"},
			{"/api/v1/app/taskapi/mergeChunks", "post"},
			{"/api/v1/app/tasks", "get"},
			{"/api/v1/app/tasks", "post"},
			{"/api/v1/app/tasks/{sessionId}", "get"},
			{"/api/v1/app/tasks/{sessionId}", "put"},
			{"/api/v1/app/tasks/{sessionId}", "delete"},
			{"/api/v1/app/tasks/sse/{sessionId}", "get"},
			{"/api/v1/app/tasks/share", "post"},
			{"/api/v1/app/tasks/uploadFile", "post"},
			{"/api/v1/app/tasks/uploadChunk", "post"},
			{"/api/v1/app/tasks/mergeChunks", "post"},
			{"/api/v1/app/tasks/{sessionId}/terminate", "post"},
		} {
			if swaggerValue(t, document, "paths", legacy.path, legacy.method, "deprecated") != true {
				t.Fatalf("legacy task %s %s must be marked deprecated", legacy.method, legacy.path)
			}
			responses := swaggerValue(t, document, "paths", legacy.path, legacy.method, "responses").(map[string]interface{})
			if len(responses) != 3 {
				t.Fatalf("legacy task %s %s responses = %v, want only 401/403/410", legacy.method, legacy.path, responses)
			}
			for _, status := range []string{"401", "403", "410"} {
				if _, ok := responses[status]; !ok {
					t.Fatalf("legacy task %s %s must document %s", legacy.method, legacy.path, status)
				}
			}
			description := swaggerValue(t, document, "paths", legacy.path, legacy.method, "description").(string)
			if !strings.Contains(description, "password-change") {
				t.Fatalf("legacy task %s %s must document the password-change gate", legacy.method, legacy.path)
			}
			if legacy.method != "get" && !strings.Contains(description, "CSRF") {
				t.Fatalf("legacy task %s %s must document the CSRF gate", legacy.method, legacy.path)
			}
		}
		serialized, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"sk-xxx", "Bearer token"} {
			if strings.Contains(string(serialized), forbidden) {
				t.Fatalf("Swagger must not retain executable raw credential example %q", forbidden)
			}
		}
		modelDescription := swaggerValue(t, document, "paths", "/api/v1/platform/models", "get", "description")
		if !strings.Contains(modelDescription.(string), "masked") {
			t.Fatal("platform model documentation must state that tokens are masked")
		}
		legacyModel := swaggerValue(t, document, "paths", "/api/v1/app/models", "get", "deprecated")
		if legacyModel != true {
			t.Fatal("legacy browser model API must be marked deprecated")
		}
		legacyCreateDescription := swaggerValue(t, document, "paths", "/api/v1/app/models", "post", "description").(string)
		for _, required := range []string{"read-only YAML", "cannot shadow", "fails closed", "status one"} {
			if !strings.Contains(legacyCreateDescription, required) {
				t.Fatalf("legacy model create documentation must contain %q", required)
			}
		}
		for _, endpoint := range []struct {
			path, method, responseRef string
		}{
			{"/api/v1/app/models", "get", "#/definitions/websocket.LegacyModelListEnvelope"},
			{"/api/v1/app/models", "post", "#/definitions/websocket.LegacyModelMutationEnvelope"},
			{"/api/v1/app/models", "delete", "#/definitions/websocket.LegacyModelMutationEnvelope"},
			{"/api/v1/app/models/{modelId}", "get", "#/definitions/websocket.LegacyModelDetailEnvelope"},
			{"/api/v1/app/models/{modelId}", "put", "#/definitions/websocket.LegacyModelMutationEnvelope"},
		} {
			if swaggerValue(t, document, "paths", endpoint.path, endpoint.method, "deprecated") != true {
				t.Fatalf("legacy model %s %s must be marked deprecated", endpoint.method, endpoint.path)
			}
			responseRef := swaggerValue(t, document, "paths", endpoint.path, endpoint.method, "responses", "200", "schema", "$ref")
			if responseRef != endpoint.responseRef {
				t.Fatalf("legacy model %s %s response schema = %v, want %s", endpoint.method, endpoint.path, responseRef, endpoint.responseRef)
			}
		}
		for _, request := range []struct {
			path, method, requestRef string
		}{
			{"/api/v1/app/models", "post", "#/definitions/websocket.LegacyModelCreateRequest"},
			{"/api/v1/app/models", "delete", "#/definitions/websocket.LegacyModelDeleteRequest"},
			{"/api/v1/app/models/{modelId}", "put", "#/definitions/websocket.LegacyModelUpdateRequest"},
		} {
			if bodyRef := swaggerBodyParameterRef(t, document, request.path, request.method); bodyRef != request.requestRef {
				t.Fatalf("legacy model %s %s request schema = %s, want %s", request.method, request.path, bodyRef, request.requestRef)
			}
		}
		if modelRef := swaggerValue(t, document, "definitions", "websocket.LegacyModelCreateRequest", "properties", "model", "$ref"); modelRef != "#/definitions/websocket.LegacyModelInfo" {
			t.Fatalf("legacy create request must preserve nested model shape, got %v", modelRef)
		}
		if dataRef := swaggerValue(t, document, "definitions", "websocket.LegacyModelDetailEnvelope", "properties", "data", "$ref"); dataRef != "#/definitions/websocket.LegacyModelView" {
			t.Fatalf("legacy detail response must preserve nested envelope, got %v", dataRef)
		}
		if modelRef := swaggerValue(t, document, "definitions", "websocket.LegacyModelView", "properties", "model", "$ref"); modelRef != "#/definitions/websocket.LegacyModelViewInfo" {
			t.Fatalf("legacy response must preserve nested model shape, got %v", modelRef)
		}
		if defaultType := swaggerValue(t, document, "definitions", "websocket.LegacyModelView", "properties", "default", "type"); defaultType != "array" {
			t.Fatalf("legacy response default must be a string array, got %v", defaultType)
		}
		if itemType := swaggerValue(t, document, "definitions", "websocket.LegacyModelView", "properties", "default", "items", "type"); itemType != "string" {
			t.Fatalf("legacy response default items must be strings, got %v", itemType)
		}
		defaultDescription := swaggerValue(t, document, "definitions", "websocket.LegacyModelView", "properties", "default", "description")
		if !strings.Contains(defaultDescription.(string), "YAML") || !strings.Contains(defaultDescription.(string), "empty array") {
			t.Fatal("legacy response default must document YAML values and the platform empty-array behavior")
		}
		maskedToken := swaggerValue(t, document, "definitions", "websocket.LegacyModelViewInfo", "properties", "token", "description")
		if !strings.Contains(strings.ToLower(maskedToken.(string)), "masked") {
			t.Fatal("legacy model response token must be documented as masked")
		}
	}
}

func TestSwaggerFullDocumentGateRejectsInMemoryYAMLSchemaDrift(t *testing.T) {
	documents := loadSwaggerDocuments(t)
	encoded, err := json.Marshal(documents["yaml"])
	if err != nil {
		t.Fatal(err)
	}
	drifted := decodeSwaggerJSON(t, encoded)
	status := swaggerValue(t, drifted, "definitions", "tasks.TaskDetail", "properties", "status").(map[string]interface{})
	status["description"] = "in-memory fixture drift outside the old spot checks"

	difference := swaggerDocumentsDifference(drifted, documents["json"])
	if difference == "" {
		t.Fatal("full-document gate accepted an in-memory YAML schema drift")
	}
	if !strings.Contains(difference, "tasks.TaskDetail") || !strings.Contains(difference, "status") {
		t.Fatalf("full-document gate reported an unhelpful difference: %s", difference)
	}
}

func TestSwaggerDocumentsImmutableReportAndBrandContracts(t *testing.T) {
	embedded := decodeSwaggerJSON(t, []byte(SwaggerInfo.ReadDoc()))
	jsonData, err := os.ReadFile("swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	jsonDocument := decodeSwaggerJSON(t, jsonData)
	yamlData, err := os.ReadFile("swagger.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var rawYAMLDocument map[string]interface{}
	if err := yaml.Unmarshal(yamlData, &rawYAMLDocument); err != nil {
		t.Fatal(err)
	}
	normalizedYAML, err := json.Marshal(rawYAMLDocument)
	if err != nil {
		t.Fatal(err)
	}
	yamlDocument := decodeSwaggerJSON(t, normalizedYAML)

	for name, document := range map[string]interface{}{
		"embedded": embedded,
		"json":     jsonDocument,
		"yaml":     yamlDocument,
	} {
		t.Run(name, func(t *testing.T) {
			endpoints := []struct {
				path, method, responseStatus, responseRef string
				arrayResponse                             bool
				requiredStatuses                          []string
				descriptionTerms                          []string
			}{
				{
					path: "/api/v1/platform/reports", method: "get", responseStatus: "200", responseRef: "#/definitions/reports.ReportListResponse",
					requiredStatuses: []string{"200", "400", "401", "403", "500"},
					descriptionTerms: []string{"page", "page_size", "Users", "auditors", "administrators", "safe summary", "raw engine results", "Logo bytes"},
				},
				{
					path: "/api/v1/platform/reports/trends", method: "get", responseStatus: "200", responseRef: "#/definitions/reports.TrendPoint", arrayResponse: true,
					requiredStatuses: []string{"200", "400", "401", "403"},
					descriptionTerms: []string{"UTC", "30", "Users", "auditors", "administrators"},
				},
				{
					path: "/api/v1/platform/reports/{reportID}", method: "get", responseStatus: "200", responseRef: "#/definitions/reports.ReportDetail",
					requiredStatuses: []string{"200", "401", "403", "404"},
					descriptionTerms: []string{"immutable RenderModel", "raw engine results", "Logo bytes", "Users", "auditors", "administrators"},
				},
				{
					path: "/api/v1/platform/admin/reports/backfill", method: "post", responseStatus: "201", responseRef: "#/definitions/reports.ReportDetail",
					requiredStatuses: []string{"201", "400", "401", "403", "500"},
					descriptionTerms: []string{"administrator", "CSRF", "durable", "audit", "missing snapshot"},
				},
				{
					path: "/api/v1/platform/brand", method: "get", responseStatus: "200", responseRef: "#/definitions/brand.Config",
					requiredStatuses: []string{"200", "401"},
					descriptionTerms: []string{"authenticated", "user", "auditor", "administrator"},
				},
				{
					path: "/api/v1/platform/brand", method: "put", responseStatus: "200", responseRef: "#/definitions/brand.Config",
					requiredStatuses: []string{"200", "400", "401", "403", "500"},
					descriptionTerms: []string{"administrator", "CSRF", "1 MiB", "PNG", "JPEG", "4096", "audit"},
				},
			}
			for _, endpoint := range endpoints {
				operation := swaggerValue(t, document, "paths", endpoint.path, endpoint.method).(map[string]interface{})
				description, _ := operation["description"].(string)
				for _, term := range endpoint.descriptionTerms {
					if !strings.Contains(description, term) {
						t.Errorf("%s %s description does not contain %q", endpoint.method, endpoint.path, term)
					}
				}
				responses, ok := operation["responses"].(map[string]interface{})
				if !ok {
					t.Fatalf("%s %s responses are missing", endpoint.method, endpoint.path)
				}
				for _, status := range endpoint.requiredStatuses {
					if _, ok := responses[status]; !ok {
						t.Errorf("%s %s does not document status %s", endpoint.method, endpoint.path, status)
					}
				}
				responseSchema := swaggerValue(t, document, "paths", endpoint.path, endpoint.method, "responses", endpoint.responseStatus, "schema")
				if endpoint.arrayResponse {
					if itemRef := swaggerNestedRef(t, responseSchema, "items"); itemRef != endpoint.responseRef {
						t.Errorf("%s %s response item schema = %q, want %q", endpoint.method, endpoint.path, itemRef, endpoint.responseRef)
					}
				} else if responseRef := swaggerNestedRef(t, responseSchema); responseRef != endpoint.responseRef {
					t.Errorf("%s %s response schema = %q, want %q", endpoint.method, endpoint.path, responseRef, endpoint.responseRef)
				}
			}

			pdf := swaggerValue(t, document, "paths", "/api/v1/platform/reports/{reportID}/exports/pdf", "post").(map[string]interface{})
			pdfDescription, _ := pdf["description"].(string)
			for _, term := range []string{"CSRF", "Users", "auditors", "administrators", "durable", "pending", "completion", "outbox", "same immutable snapshot"} {
				if !strings.Contains(pdfDescription, term) {
					t.Errorf("PDF export description does not contain %q", term)
				}
			}
			for _, status := range []string{"200", "401", "403", "404", "500"} {
				_ = swaggerValue(t, pdf, "responses", status)
			}
			if schemaType := swaggerValue(t, pdf, "responses", "200", "schema", "type"); schemaType != "file" {
				t.Errorf("PDF success schema type = %v, want file", schemaType)
			}

			if bodyRef := swaggerBodyParameterRef(t, document, "/api/v1/platform/admin/reports/backfill", "post"); bodyRef != "#/definitions/reports.BackfillRequest" {
				t.Errorf("backfill body schema = %q", bodyRef)
			}
			if bodyRef := swaggerBodyParameterRef(t, document, "/api/v1/platform/brand", "put"); bodyRef != "#/definitions/brand.UpdateRequest" {
				t.Errorf("brand update body schema = %q", bodyRef)
			}

			for _, definition := range []string{"reports.ReportSummary", "reports.ReportDetail", "reports.RenderModel"} {
				properties := swaggerValue(t, document, "definitions", definition, "properties").(map[string]interface{})
				for _, forbidden := range []string{"raw_result", "render_data", "owner_user_id", "logo", "updated_by"} {
					if _, exists := properties[forbidden]; exists {
						t.Errorf("safe wire definition %s exposes %s", definition, forbidden)
					}
				}
			}
			if renderRef := swaggerNestedRef(t, swaggerValue(t, document, "definitions", "reports.ReportDetail", "properties", "render")); renderRef != "#/definitions/reports.RenderModel" {
				t.Errorf("report detail render schema = %q", renderRef)
			}
			renderProperties := swaggerValue(t, document, "definitions", "reports.RenderModel", "properties").(map[string]interface{})
			for _, required := range []string{
				"render_version", "mapping_version", "generated_at", "completed_at", "task_id", "task_type",
				"product_name", "primary_color", "watermark", "risk", "score_explanation", "risk_trend",
				"risk_distribution", "top_risks", "technical_findings", "recommendations", "coverage", "conclusion",
			} {
				if _, exists := renderProperties[required]; !exists {
					t.Errorf("immutable RenderModel does not document %s", required)
				}
			}
			trendSchema := renderProperties["risk_trend"]
			if min := swaggerValue(t, trendSchema, "minItems"); min != float64(30) && min != 30 {
				t.Errorf("RenderModel risk_trend minItems = %v, want 30", min)
			}
			if max := swaggerValue(t, trendSchema, "maxItems"); max != float64(30) && max != 30 {
				t.Errorf("RenderModel risk_trend maxItems = %v, want 30", max)
			}
			if itemRef := swaggerNestedRef(t, trendSchema, "items"); itemRef != "#/definitions/reports.TrendPoint" {
				t.Errorf("RenderModel risk_trend item schema = %q", itemRef)
			}
			if pageSizeMax := swaggerParameterValue(t, document, "/api/v1/platform/reports", "get", "page_size", "maximum"); pageSizeMax != float64(100) && pageSizeMax != 100 {
				t.Errorf("page_size maximum = %v, want 100", pageSizeMax)
			}
			if pageMax := swaggerParameterValue(t, document, "/api/v1/platform/reports", "get", "page", "maximum"); pageMax != float64(1000) && pageMax != 1000 {
				t.Errorf("page maximum = %v, want 1000", pageMax)
			}
			for _, definition := range []string{"brand.Config", "brand.UpdateRequest"} {
				properties := swaggerValue(t, document, "definitions", definition, "properties").(map[string]interface{})
				for property, want := range map[string]int{"product_name": 128, "watermark": 64} {
					value := swaggerValue(t, properties[property], "maxLength")
					if value != float64(want) && value != want {
						t.Errorf("%s.%s maxLength = %v, want %d", definition, property, value, want)
					}
				}
			}
		})
	}
}

func TestSwaggerDocumentsEnterpriseConsoleContracts(t *testing.T) {
	documents := loadSwaggerDocuments(t)
	for name, document := range documents {
		t.Run(name, func(t *testing.T) {
			operations := []struct {
				path, method, successStatus, responseRef string
				statuses                                 []string
			}{
				{"/api/v1/auth/csrf", "get", "200", "#/definitions/identity.CSRFResponse", []string{"200", "426", "500"}},
				{"/api/v1/auth/login", "post", "200", "#/definitions/identity.LoginResponse", []string{"200", "400", "401", "403", "426", "500"}},
				{"/api/v1/auth/me", "get", "200", "#/definitions/identity.CurrentSubjectResponse", []string{"200", "401", "426"}},
				{"/api/v1/auth/password-resets/confirm", "post", "", "", []string{"204", "400", "401", "403", "426"}},
				{"/api/v1/public/brand", "get", "200", "#/definitions/brand.PublicConfig", []string{"200", "500"}},
				{"/api/v1/version", "get", "200", "#/definitions/websocket.SafeVersionResponse", []string{"200"}},
				{"/api/v1/platform/dashboard", "get", "200", "#/definitions/dashboard.View", []string{"200", "401", "403", "500"}},
				{"/api/v1/platform/tasks", "get", "200", "#/definitions/tasks.TaskListResponse", []string{"200", "400", "401", "403", "500"}},
				{"/api/v1/platform/tasks/{taskID}", "get", "200", "#/definitions/tasks.TaskDetail", []string{"200", "401", "403", "404", "500"}},
				{"/api/v1/platform/tasks/{taskID}/result", "get", "", "", []string{"401", "403", "410"}},
				{"/api/v1/platform/reports", "get", "200", "#/definitions/reports.ReportListResponse", []string{"200", "400", "401", "403", "500"}},
				{"/api/v1/platform/admin/users", "get", "200", "#/definitions/admin.UserListResponse", []string{"200", "400", "401", "403", "500"}},
				{"/api/v1/platform/admin/audit-events", "get", "200", "#/definitions/admin.AuditListResponse", []string{"200", "400", "401", "403", "500"}},
				{"/api/v1/platform/models", "get", "200", "#/definitions/models.CatalogPage", []string{"200", "400", "401", "403", "500"}},
				{"/api/v1/platform/tasks/attachments/{attachmentID}/download", "get", "200", "", []string{"200", "400", "401", "403", "404", "500"}},
			}
			for _, operation := range operations {
				for _, status := range operation.statuses {
					_ = swaggerValue(t, document, "paths", operation.path, operation.method, "responses", status)
				}
				if operation.responseRef != "" {
					got := swaggerNestedRef(t, swaggerValue(t, document, "paths", operation.path, operation.method, "responses", operation.successStatus, "schema"))
					if got != operation.responseRef {
						t.Errorf("%s %s response schema = %q, want %q", operation.method, operation.path, got, operation.responseRef)
					}
				}
			}

			for _, request := range []struct{ path, method, ref string }{
				{"/api/v1/auth/login", "post", "#/definitions/identity.LoginRequest"},
				{"/api/v1/auth/password-resets/confirm", "post", "#/definitions/identity.PasswordResetConfirmRequest"},
			} {
				if got := swaggerBodyParameterRef(t, document, request.path, request.method); got != request.ref {
					t.Errorf("%s %s body schema = %q, want %q", request.method, request.path, got, request.ref)
				}
			}

			for _, path := range []string{
				"/api/v1/platform/tasks", "/api/v1/platform/reports",
				"/api/v1/platform/admin/users", "/api/v1/platform/admin/audit-events", "/api/v1/platform/models",
			} {
				assertPaginationContract(t, document, path)
			}
			for _, definition := range []string{
				"tasks.TaskListResponse", "reports.ReportListResponse", "admin.UserListResponse", "admin.AuditListResponse", "models.CatalogPage",
			} {
				if got := swaggerValue(t, document, "definitions", definition, "properties", "total", "format"); got != "int64" {
					t.Errorf("%s.total format = %v, want int64", definition, got)
				}
			}

			assertExactProperties(t, document, "identity.CSRFResponse", "csrf_token")
			assertExactProperties(t, document, "identity.CurrentSubjectResponse", "id", "username", "role", "must_change_password")
			assertExactProperties(t, document, "brand.PublicConfig", "product_name", "primary_color", "logo_data_url")
			assertExactProperties(t, document, "websocket.SafeVersionResponse", "version", "commit", "build_time")
			assertExactProperties(t, document, "dashboard.View", "has_data", "security_score", "mapping_versions", "risk", "trend", "recent_tasks", "attention")
			assertExactProperties(t, document, "dashboard.AttentionItem", "report_id", "task_id", "task_type", "completed_at", "score", "high", "medium", "low")
			assertExactProperties(t, document, "tasks.TaskSummary", "id", "owner", "task_type", "status", "created_at", "updated_at")
			assertExactProperties(t, document, "tasks.TaskDetail", "id", "owner", "task_type", "status", "created_at", "updated_at", "input_summary")

			if got := swaggerValue(t, document, "definitions", "dashboard.View", "properties", "security_score", "x-nullable"); got != true {
				t.Errorf("dashboard security_score x-nullable = %v, want true", got)
			}
			trend := swaggerValue(t, document, "definitions", "dashboard.View", "properties", "trend")
			for _, field := range []string{"minItems", "maxItems"} {
				if got := swaggerValue(t, trend, field); got != float64(30) && got != 30 {
					t.Errorf("dashboard trend %s = %v, want 30", field, got)
				}
			}
			for _, definition := range []string{"tasks.TaskSummary", "tasks.TaskDetail"} {
				status := swaggerValue(t, document, "definitions", definition, "properties", "status")
				if got := swaggerValue(t, status, "enum").([]interface{}); len(got) != 8 {
					t.Errorf("%s.status enum has %d values, want 8", definition, len(got))
				}
			}
			for _, definition := range []string{"models.CatalogView", "models.View"} {
				tokenDescription := strings.ToLower(swaggerValue(t, document, "definitions", definition, "properties", "token", "description").(string))
				if !strings.Contains(tokenDescription, "masked") || !strings.Contains(tokenDescription, "********") {
					t.Errorf("%s token description must state the fixed mask", definition)
				}
			}
			for _, field := range []string{"source", "read_only"} {
				_ = swaggerValue(t, document, "definitions", "models.CatalogView", "properties", field)
			}
			if got := swaggerValue(t, document, "definitions", "models.CatalogView", "properties", "source", "enum").([]interface{}); !reflect.DeepEqual(got, []interface{}{"platform", "yaml"}) {
				t.Errorf("models.CatalogView.source enum = %v", got)
			}

			resultDescription := swaggerValue(t, document, "paths", "/api/v1/platform/tasks/{taskID}/result", "get", "description").(string)
			for _, term := range []string{"always returns 410", "authentication", "password-change"} {
				if !strings.Contains(resultDescription, term) {
					t.Errorf("retired platform result description does not contain %q", term)
				}
			}
			for _, path := range []string{"/api/v1/auth/login", "/api/v1/auth/password-resets/confirm"} {
				if !strings.Contains(swaggerValue(t, document, "paths", path, "post", "description").(string), "CSRF") {
					t.Errorf("%s must document its pre-authentication CSRF prerequisite", path)
				}
			}
			csrfDescription := swaggerValue(t, document, "paths", "/api/v1/auth/csrf", "get", "description").(string)
			for _, term := range []string{"anonymous", "aig_csrf", "SameSite=Lax", "no session"} {
				if !strings.Contains(csrfDescription, term) {
					t.Errorf("CSRF bootstrap description does not contain %q", term)
				}
			}
			brandLogoDescription := swaggerValue(t, document, "definitions", "brand.PublicConfig", "properties", "logo_data_url", "description").(string)
			for _, term := range []string{"data:image/png;base64", "data:image/jpeg;base64", "empty string"} {
				if !strings.Contains(brandLogoDescription, term) {
					t.Errorf("public Logo description does not contain %q", term)
				}
			}
			brandLogoPattern := swaggerValue(t, document, "definitions", "brand.PublicConfig", "properties", "logo_data_url", "pattern").(string)
			for _, term := range []string{"^", "png", "jpeg", "$"} {
				if !strings.Contains(brandLogoPattern, term) {
					t.Errorf("public Logo pattern does not contain %q", term)
				}
			}
			versionDescription := swaggerValue(t, document, "paths", "/api/v1/version", "get", "description").(string)
			if strings.Contains(strings.ToLower(versionDescription), "github") || strings.Contains(strings.ToLower(versionDescription), "update check") {
				t.Error("public version endpoint must not be described as a network update check")
			}
			dashboardDescription := swaggerValue(t, document, "paths", "/api/v1/platform/dashboard", "get", "description").(string)
			for _, term := range []string{"30 UTC", "Users", "auditors", "administrators", "has_data=false", "security_score=null"} {
				if !strings.Contains(dashboardDescription, term) {
					t.Errorf("dashboard description does not contain %q", term)
				}
			}
			attachmentDescription := swaggerValue(t, document, "paths", "/api/v1/platform/tasks/attachments/{attachmentID}/download", "get", "description").(string)
			for _, term := range []string{"owner", "404", "auditors", "403", "administrators", "attachment.download_authorized", "sanitized"} {
				if !strings.Contains(attachmentDescription, term) {
					t.Errorf("attachment download description does not contain %q", term)
				}
			}

			for _, definition := range []string{
				"identity.CSRFResponse", "identity.CurrentSubjectResponse", "brand.PublicConfig", "websocket.SafeVersionResponse",
				"dashboard.View", "dashboard.TrendPoint", "dashboard.AttentionItem", "tasks.TaskSummary", "tasks.TaskDetail",
				"tasks.TaskListResponse", "reports.ReportListResponse", "admin.UserResponse", "admin.UserListResponse",
				"admin.AuditListResponse", "models.CatalogView", "models.CatalogPage",
			} {
				properties := swaggerValue(t, document, "definitions", definition, "properties").(map[string]interface{})
				for _, forbidden := range []string{"raw_result", "render_data", "logo", "password_hash", "token_hash", "storage_name", "engine_session_id", "dispatch_error", "attachment_ids"} {
					if _, exists := properties[forbidden]; exists {
						t.Errorf("safe wire definition %s exposes %s", definition, forbidden)
					}
				}
			}
		})
	}
}

func TestSwaggerDocumentsTaskCreateAndLegacySecurityCorrections(t *testing.T) {
	for name, document := range loadSwaggerDocuments(t) {
		t.Run(name, func(t *testing.T) {
			createPath := "/api/v1/platform/tasks"
			if got := swaggerNestedRef(t, swaggerValue(t, document, "paths", createPath, "post", "responses", "202", "schema")); got != "#/definitions/tasks.TaskDetail" {
				t.Errorf("task create 202 schema = %q, want safe TaskDetail", got)
			}
			if got := swaggerNestedRef(t, swaggerValue(t, document, "paths", createPath, "post", "responses", "503", "schema")); got != "#/definitions/tasks.TaskCreateErrorResponse" {
				t.Errorf("task create 503 schema = %q, want safe TaskCreateErrorResponse", got)
			}
			if got := swaggerNestedRef(t, swaggerValue(t, document, "paths", createPath, "post", "responses", "400", "schema")); got != "#/definitions/tasks.TaskCreateBadRequestResponse" {
				t.Errorf("task create 400 schema = %q, want safe TaskCreateBadRequestResponse", got)
			}
			assertExactProperties(t, document, "tasks.TaskCreateErrorResponse", "error", "task")
			assertExactProperties(t, document, "tasks.TaskCreateBadRequestResponse", "error")
			badRequestRequired := swaggerValue(t, document, "definitions", "tasks.TaskCreateBadRequestResponse", "required").([]interface{})
			if !reflect.DeepEqual(badRequestRequired, []interface{}{"error"}) {
				t.Errorf("task create 400 required fields = %v, want [error]", badRequestRequired)
			}
			if got := swaggerNestedRef(t, swaggerValue(t, document, "definitions", "tasks.TaskCreateErrorResponse", "properties", "task")); got != "#/definitions/tasks.TaskDetail" {
				t.Errorf("task create error task schema = %q, want TaskDetail", got)
			}
			badRequestEnum := swaggerValue(t, document, "definitions", "tasks.TaskCreateBadRequestResponse", "properties", "error", "enum").([]interface{})
			if !reflect.DeepEqual(badRequestEnum, []interface{}{"invalid task request", "attachment unavailable"}) {
				t.Errorf("task create 400 error enum = %v", badRequestEnum)
			}
			createDescription := strings.ToLower(swaggerValue(t, document, "paths", createPath, "post", "description").(string))
			for _, term := range []string{"breaking", "security hardening", "taskdetail", "input_summary"} {
				if !strings.Contains(createDescription, term) {
					t.Errorf("task create description lacks %q", term)
				}
			}
			badRequestDescription := strings.ToLower(swaggerValue(t, document, "paths", createPath, "post", "responses", "400", "description").(string))
			for _, term := range []string{"invalid task request", "attachment unavailable", "missing", "not ready"} {
				if !strings.Contains(badRequestDescription, term) {
					t.Errorf("task create 400 description lacks %q", term)
				}
			}

			mergeResponses := swaggerValue(t, document, "paths", "/api/v1/platform/tasks/attachments/{attachmentID}/merge", "post", "responses").(map[string]interface{})
			if _, ok := mergeResponses["401"]; !ok {
				t.Error("attachment merge must document unauthenticated 401")
			}

			for _, operation := range []struct {
				path, method string
				mutation     bool
				roleTerm     string
			}{
				{"/api/v1/app/models", "get", false, "read scope"},
				{"/api/v1/app/models", "post", true, "user or administrator"},
				{"/api/v1/app/models", "delete", true, "owner or administrator"},
				{"/api/v1/app/models/{modelId}", "get", false, "read scope"},
				{"/api/v1/app/models/{modelId}", "put", true, "owner or administrator"},
			} {
				description := strings.ToLower(swaggerValue(t, document, "paths", operation.path, operation.method, "responses", "403", "description").(string))
				for _, term := range []string{"must-change-password", operation.roleTerm} {
					if !strings.Contains(description, term) {
						t.Errorf("legacy model %s %s 403 description lacks %q", operation.method, operation.path, term)
					}
				}
				if operation.mutation && !strings.Contains(description, "csrf") {
					t.Errorf("legacy model mutation %s %s 403 description must include CSRF", operation.method, operation.path)
				}
			}

			definitions := swaggerValue(t, document, "definitions").(map[string]interface{})
			for _, obsolete := range []string{"websocket.APIResponse", "websocket.TaskCreateResponse", "websocket.TaskStatusResponse"} {
				if _, exists := definitions[obsolete]; exists {
					t.Errorf("unreferenced retired task schema %s must be removed", obsolete)
				}
			}
		})
	}
}

func TestSwaggerDocumentsTaskListExactFilters(t *testing.T) {
	for name, document := range loadSwaggerDocuments(t) {
		t.Run(name, func(t *testing.T) {
			path := "/api/v1/platform/tasks"
			status := swaggerParameterValue(t, document, path, "get", "status", "enum").([]interface{})
			if !reflect.DeepEqual(status, []interface{}{"pending", "dispatching", "running", "succeeded", "failed", "dispatch_failed", "dispatch_unknown", "cancelled"}) {
				t.Errorf("task status filter enum = %v", status)
			}
			taskType := swaggerParameterValue(t, document, path, "get", "task_type", "enum").([]interface{})
			if !reflect.DeepEqual(taskType, []interface{}{"mcp_scan", "ai_infra_scan", "model_redteam_report", "agent_scan"}) {
				t.Errorf("task_type filter enum = %v", taskType)
			}
			description := swaggerValue(t, document, "paths", path, "get", "responses", "400", "description").(string)
			for _, term := range []string{"status", "task_type", "exact"} {
				if !strings.Contains(description, term) {
					t.Errorf("task list 400 description lacks %q", term)
				}
			}
		})
	}
}

func TestAPIGuidesDocumentHTTPSBootstrapPasswordChangeAndTaskCreateMigration(t *testing.T) {
	for _, guide := range []struct {
		path     string
		required []string
	}{
		{"../../docs/api/reference.en.md", []string{
			"### Security-hardening task-create response migration", "Breaking change", "202", "TaskDetail", "owner_user_id", "input_summary", "503",
			`"error": "task dispatch unavailable"`, "BASE_URL = \"https://localhost:8443\"", `CA_BUNDLE = "<trusted-local-ca.pem>"`,
			`f"{BASE_URL}/api/v1/auth/me"`, `f"{BASE_URL}/api/v1/auth/change-password"`, `"old_password": "<current-password>"`,
			`login_with_password("<new-password>")`, `--cacert "$CA_BUNDLE"`, "trusted local TLS-terminating reverse proxy", "production `Secure` cookies",
			`ME_JSON="$(curl -fsS --cacert "$CA_BUNDLE" -b "$COOKIE_JAR" "$BASE_URL/api/v1/auth/me")"`,
			`-d '{"username":"<username>","password":"<new-password>"}'`,
		}},
		{"../../docs/api/reference.md", []string{
			"### 任务创建响应的安全加固迁移", "破坏性变更", "202", "TaskDetail", "owner_user_id", "input_summary", "503",
			`"error": "task dispatch unavailable"`, "BASE_URL = \"https://localhost:8443\"", `CA_BUNDLE = "<trusted-local-ca.pem>"`,
			`f"{BASE_URL}/api/v1/auth/me"`, `f"{BASE_URL}/api/v1/auth/change-password"`, `"old_password": "<current-password>"`,
			`login_with_password("<new-password>")`, `--cacert "$CA_BUNDLE"`, "可信本地 TLS 终止反向代理", "生产 `Secure` Cookie",
			`ME_JSON="$(curl -fsS --cacert "$CA_BUNDLE" -b "$COOKIE_JAR" "$BASE_URL/api/v1/auth/me")"`,
			`-d '{"username":"<username>","password":"<new-password>"}'`,
		}},
	} {
		contents, err := os.ReadFile(guide.path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(contents)
		for _, required := range guide.required {
			if !strings.Contains(text, required) {
				t.Errorf("%s does not document %q", guide.path, required)
			}
		}
		for _, forbidden := range []string{"curl -k", "--insecure", "http://localhost:8088", `base_url = "http://localhost:8088"`} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s retains insecure example %q", guide.path, forbidden)
			}
		}
	}
}

func TestAPIGuidesDocumentEnterpriseConsoleContracts(t *testing.T) {
	for _, guide := range []struct {
		path     string
		required []string
	}{
		{"../../docs/api/reference.en.md", []string{
			"## Browser identity, CSRF, and public bootstrap", "/api/v1/auth/csrf", "/api/v1/auth/me", "/api/v1/public/brand", "/api/v1/version",
			"## Enterprise console collections", "/api/v1/platform/dashboard", "exactly 30 UTC", "security_score=null",
			"TaskListResponse", "ReportListResponse", "UserListResponse", "AuditListResponse", "CatalogPage", "page=1..1000", "page_size=1..100",
			"attachment.download_authorized", "other users receive `404`", "auditors receive `403`", "schema reaches v8",
			"GET `/api/v1/auth/csrf` before login", "persistent cookie jar", "session.cookies.get(\"aig_csrf\")", "X-CSRF-Token", `-b "$COOKIE_JAR"`,
			"idx_platform_tasks_updated_at", "idx_platform_tasks_owner_updated_at",
		}},
		{"../../docs/api/reference.md", []string{
			"## 浏览器身份、CSRF 与公开初始化", "/api/v1/auth/csrf", "/api/v1/auth/me", "/api/v1/public/brand", "/api/v1/version",
			"## 企业控制台集合契约", "/api/v1/platform/dashboard", "恰好 30 个 UTC", "security_score=null",
			"TaskListResponse", "ReportListResponse", "UserListResponse", "AuditListResponse", "CatalogPage", "page=1..1000", "page_size=1..100",
			"attachment.download_authorized", "其他普通用户得到 `404`", "审计员得到 `403`", "schema 到达 v8",
			"登录前先 GET `/api/v1/auth/csrf`", "持久 Cookie jar", "session.cookies.get(\"aig_csrf\")", "X-CSRF-Token", `-b "$COOKIE_JAR"`,
			"idx_platform_tasks_updated_at", "idx_platform_tasks_owner_updated_at",
		}},
	} {
		contents, err := os.ReadFile(guide.path)
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range guide.required {
			if !strings.Contains(string(contents), required) {
				t.Errorf("%s does not document %q", guide.path, required)
			}
		}
		if strings.Contains(string(contents), "schema reaches v7") || strings.Contains(string(contents), "schema 到达 v7") {
			t.Errorf("%s retains the obsolete schema v7 statement", guide.path)
		}
	}
}

func TestAPIGuidesDocumentLegacyModelAndMigrationBoundaries(t *testing.T) {
	for _, guide := range []struct {
		path     string
		required []string
	}{
		{
			path: "../../docs/api/reference.en.md",
			required: []string{
				"/api/v1/app/models/{modelId}", "collection DELETE", "{status,message,data}",
				"HTTP `200`", "`401`", "`403`", "masked", "/api/v1/platform/models",
				"cannot shadow", "fails closed",
				"Only `aig migrate` may apply database DDL", "schema reaches v8", "empty legacy table",
				"/api/v1/platform/tasks", "Idempotency-Key", "opaque attachment IDs", "410 Gone", "password-change and CSRF checks",
				"/api/v1/platform/reports", "page_size", "safe summary", "immutable RenderModel", "30 fixed UTC day buckets",
				"/api/v1/platform/reports/{reportID}/exports/pdf", "durable pending/completion audit outbox",
				"/api/v1/platform/admin/reports/backfill", "/api/v1/platform/brand", "PNG or JPEG", "1 MiB", "4096",
				"raw engine results and Logo bytes are never exposed",
			},
		},
		{
			path: "../../docs/api/reference.md",
			required: []string{
				"/api/v1/app/models/{modelId}", "集合 DELETE", "{status,message,data}",
				"HTTP `200`", "`401`", "`403`", "始终脱敏", "/api/v1/platform/models",
				"不能遮蔽", "失败关闭",
				"只有 `aig migrate` 可以执行数据库 DDL", "schema 到达 v8", "旧表为空",
				"/api/v1/platform/tasks", "Idempotency-Key", "opaque 附件 ID", "410 Gone", "首次改密与 CSRF 校验",
				"/api/v1/platform/reports", "page_size", "安全摘要", "不可变 RenderModel", "30 个固定 UTC 日桶",
				"/api/v1/platform/reports/{reportID}/exports/pdf", "持久化 pending/completion 审计 outbox",
				"/api/v1/platform/admin/reports/backfill", "/api/v1/platform/brand", "PNG 或 JPEG", "1 MiB", "4096",
				"绝不暴露原始引擎结果与 Logo 字节",
			},
		},
	} {
		contents, err := os.ReadFile(guide.path)
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range guide.required {
			if !strings.Contains(string(contents), required) {
				t.Errorf("%s does not document %q", guide.path, required)
			}
		}
		for _, executableLegacyTaskExample := range []string{
			"/api/v1/app/taskapi/upload",
			"/api/v1/app/taskapi/tasks",
			"/api/v1/app/taskapi/status/",
			"/api/v1/app/taskapi/result/",
			"/api/v1/app/tasks/uploadChunk",
			"/api/v1/app/tasks/mergeChunks",
			"http://localhost:8088/api/v1/app/taskapi",
		} {
			if strings.Contains(string(contents), executableLegacyTaskExample) {
				t.Errorf("%s retains executable legacy task example %q", guide.path, executableLegacyTaskExample)
			}
		}
	}
}

func loadSwaggerDocuments(t *testing.T) map[string]interface{} {
	t.Helper()
	embedded := decodeSwaggerJSON(t, []byte(SwaggerInfo.ReadDoc()))
	jsonData, err := os.ReadFile("swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	yamlData, err := os.ReadFile("swagger.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var rawYAMLDocument map[string]interface{}
	if err := yaml.Unmarshal(yamlData, &rawYAMLDocument); err != nil {
		t.Fatal(err)
	}
	normalizedYAML, err := json.Marshal(rawYAMLDocument)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]interface{}{
		"embedded": embedded,
		"json":     decodeSwaggerJSON(t, jsonData),
		"yaml":     decodeSwaggerJSON(t, normalizedYAML),
	}
}

func assertPaginationContract(t *testing.T, document interface{}, path string) {
	t.Helper()
	for _, parameter := range []struct {
		name                  string
		minimum, maximum, def float64
	}{
		{"page", 1, 1000, 1},
		{"page_size", 1, 100, 20},
	} {
		for field, want := range map[string]float64{"minimum": parameter.minimum, "maximum": parameter.maximum, "default": parameter.def} {
			got := swaggerParameterValue(t, document, path, "get", parameter.name, field)
			if got != want && got != int(want) {
				t.Errorf("%s %s %s = %v, want %v", path, parameter.name, field, got, want)
			}
		}
	}
}

func assertExactProperties(t *testing.T, document interface{}, definition string, expected ...string) {
	t.Helper()
	properties := swaggerValue(t, document, "definitions", definition, "properties").(map[string]interface{})
	actual := make([]string, 0, len(properties))
	for property := range properties {
		actual = append(actual, property)
	}
	sort.Strings(actual)
	sort.Strings(expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("%s properties = %v, want %v", definition, actual, expected)
	}
}

func swaggerBodyParameterRef(t *testing.T, document interface{}, path, method string) string {
	t.Helper()
	parameters, ok := swaggerValue(t, document, "paths", path, method, "parameters").([]interface{})
	if !ok {
		t.Fatalf("Swagger parameters at %s.%s are not an array", path, method)
	}
	for _, parameter := range parameters {
		object, ok := parameter.(map[string]interface{})
		if !ok || object["in"] != "body" {
			continue
		}
		schema, ok := object["schema"].(map[string]interface{})
		if !ok {
			t.Fatalf("Swagger body schema at %s.%s is not an object", path, method)
		}
		ref, _ := schema["$ref"].(string)
		return ref
	}
	t.Fatalf("Swagger body parameter at %s.%s is missing", path, method)
	return ""
}

func swaggerNestedRef(t *testing.T, value interface{}, path ...string) string {
	t.Helper()
	for _, key := range path {
		object, ok := value.(map[string]interface{})
		if !ok {
			t.Fatalf("Swagger nested value at %s is not an object", strings.Join(path, "."))
		}
		value, ok = object[key]
		if !ok {
			t.Fatalf("Swagger nested value at %s is missing", strings.Join(path, "."))
		}
	}
	object, ok := value.(map[string]interface{})
	if !ok {
		t.Fatal("Swagger schema is not an object")
	}
	ref, _ := object["$ref"].(string)
	return ref
}

func swaggerParameterValue(t *testing.T, document interface{}, path, method, parameterName, field string) interface{} {
	t.Helper()
	parameters, ok := swaggerValue(t, document, "paths", path, method, "parameters").([]interface{})
	if !ok {
		t.Fatalf("Swagger parameters at %s.%s are not an array", path, method)
	}
	for _, parameter := range parameters {
		object, ok := parameter.(map[string]interface{})
		if ok && object["name"] == parameterName {
			return object[field]
		}
	}
	t.Fatalf("Swagger parameter %s at %s.%s is missing", parameterName, path, method)
	return nil
}

func swaggerValue(t *testing.T, document interface{}, path ...string) interface{} {
	t.Helper()
	value := document
	for _, key := range path {
		object, ok := value.(map[string]interface{})
		if !ok {
			t.Fatalf("Swagger value at %s is not an object", strings.Join(path, "."))
		}
		value, ok = object[key]
		if !ok {
			t.Fatalf("Swagger value at %s is missing", strings.Join(path, "."))
		}
	}
	return value
}

func firstSwaggerDifference(path string, left, right interface{}) string {
	leftMap, leftIsMap := left.(map[string]interface{})
	rightMap, rightIsMap := right.(map[string]interface{})
	if leftIsMap || rightIsMap {
		if !leftIsMap || !rightIsMap {
			return fmt.Sprintf("%s has different types", path)
		}
		keys := make([]string, 0, len(leftMap)+len(rightMap))
		seen := make(map[string]bool)
		for key := range leftMap {
			keys = append(keys, key)
			seen[key] = true
		}
		for key := range rightMap {
			if !seen[key] {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			leftValue, leftOK := leftMap[key]
			rightValue, rightOK := rightMap[key]
			if !leftOK || !rightOK {
				return fmt.Sprintf("%s.%s exists on only one side", path, key)
			}
			if !reflect.DeepEqual(leftValue, rightValue) {
				return firstSwaggerDifference(path+"."+key, leftValue, rightValue)
			}
		}
	}
	leftSlice, leftIsSlice := left.([]interface{})
	rightSlice, rightIsSlice := right.([]interface{})
	if leftIsSlice || rightIsSlice {
		if !leftIsSlice || !rightIsSlice || len(leftSlice) != len(rightSlice) {
			return fmt.Sprintf("%s has different arrays", path)
		}
		for index := range leftSlice {
			if !reflect.DeepEqual(leftSlice[index], rightSlice[index]) {
				return firstSwaggerDifference(fmt.Sprintf("%s[%d]", path, index), leftSlice[index], rightSlice[index])
			}
		}
	}
	return fmt.Sprintf("%s differs: %v != %v", path, left, right)
}

func swaggerDocumentsDifference(left, right interface{}) string {
	if reflect.DeepEqual(left, right) {
		return ""
	}
	return firstSwaggerDifference("$", left, right)
}

func decodeSwaggerJSON(t *testing.T, data []byte) interface{} {
	t.Helper()
	var document interface{}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if root, ok := document.(map[string]interface{}); ok {
		if root["host"] == "" {
			delete(root, "host")
		}
		if schemes, ok := root["schemes"].([]interface{}); ok && len(schemes) == 0 {
			delete(root, "schemes")
		}
	}
	return document
}
