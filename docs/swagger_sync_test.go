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
	}
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
