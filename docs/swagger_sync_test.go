package docs

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
	if !reflect.DeepEqual(embedded, jsonDocument) {
		t.Fatalf("docs.go and swagger.json are not synchronized: %s", firstSwaggerDifference("$", embedded, jsonDocument))
	}

	yamlData, err := os.ReadFile("swagger.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var yamlDocument interface{}
	if err := yaml.Unmarshal(yamlData, &yamlDocument); err != nil {
		t.Fatal(err)
	}
	normalizedYAML, err := json.Marshal(yamlDocument)
	if err != nil {
		t.Fatal(err)
	}
	yamlDocument = decodeSwaggerJSON(t, normalizedYAML)
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
		} {
			if swaggerValue(t, document, "paths", legacy.path, legacy.method, "deprecated") != true {
				t.Fatalf("legacy task %s %s must be marked deprecated", legacy.method, legacy.path)
			}
			responses := swaggerValue(t, document, "paths", legacy.path, legacy.method, "responses").(map[string]interface{})
			if _, ok := responses["410"]; !ok {
				t.Fatalf("legacy task %s %s must document 410 Gone", legacy.method, legacy.path)
			}
			description := swaggerValue(t, document, "paths", legacy.path, legacy.method, "description").(string)
			if !strings.Contains(description, "password-change") {
				t.Fatalf("legacy task %s %s must document the password-change gate", legacy.method, legacy.path)
			}
			if legacy.method == "post" && !strings.Contains(description, "CSRF") {
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

func TestAPIGuidesDocumentLegacyModelAndMigrationBoundaries(t *testing.T) {
	for _, guide := range []struct {
		path     string
		required []string
	}{
		{
			path: "../api.md",
			required: []string{
				"/api/v1/app/models/{modelId}", "collection DELETE", "{status,message,data}",
				"HTTP `200`", "`401`", "`403`", "masked", "/api/v1/platform/models",
				"cannot shadow", "fails closed",
				"Only `aig migrate` may apply database DDL", "schema reaches v6", "empty legacy table",
				"/api/v1/platform/tasks", "Idempotency-Key", "opaque attachment IDs", "410 Gone", "password-change and CSRF checks",
			},
		},
		{
			path: "../api_zh.md",
			required: []string{
				"/api/v1/app/models/{modelId}", "集合 DELETE", "{status,message,data}",
				"HTTP `200`", "`401`", "`403`", "始终脱敏", "/api/v1/platform/models",
				"不能遮蔽", "失败关闭",
				"只有 `aig migrate` 可以执行数据库 DDL", "schema 到达 v6", "旧表为空",
				"/api/v1/platform/tasks", "Idempotency-Key", "opaque 附件 ID", "410 Gone", "首次改密与 CSRF 校验",
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
