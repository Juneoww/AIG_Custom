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
		} {
			_ = swaggerValue(t, document, "paths", endpoint.path, endpoint.method)
		}
		modelDescription := swaggerValue(t, document, "paths", "/api/v1/platform/models", "get", "description")
		if !strings.Contains(modelDescription.(string), "masked") {
			t.Fatal("platform model documentation must state that tokens are masked")
		}
		legacyModel := swaggerValue(t, document, "paths", "/api/v1/app/models", "get", "deprecated")
		if legacyModel != true {
			t.Fatal("legacy browser model API must be marked deprecated")
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
				"Only `aig migrate` may apply database DDL", "schema reaches v4", "empty legacy table",
			},
		},
		{
			path: "../api_zh.md",
			required: []string{
				"/api/v1/app/models/{modelId}", "集合 DELETE", "{status,message,data}",
				"HTTP `200`", "`401`", "`403`", "始终脱敏", "/api/v1/platform/models",
				"只有 `aig migrate` 可以执行数据库 DDL", "schema 到达 v4", "旧表为空",
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
