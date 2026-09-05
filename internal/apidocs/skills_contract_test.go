package apidocs

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSwaggerDocumentsSkillsContract(t *testing.T) {
	for name, document := range loadSwaggerDocuments(t) {
		t.Run(name, func(t *testing.T) {
			for _, definition := range []string{"tasks.TaskSummary", "tasks.TaskDetail"} {
				properties := swaggerValue(t, document, "definitions", definition, "properties").(map[string]interface{})
				assertSwaggerStringEnum(t, properties, "task_type", []string{"mcp_scan", "ai_infra_scan", "model_redteam_report", "agent_scan", "skills_scan", "unknown"})
			}
			properties := swaggerValue(t, document, "definitions", "tasks.TaskInputSummary", "properties").(map[string]interface{})
			assertSwaggerStringEnum(t, properties, "scan_mode", []string{"static"})
			scanMode, exists := properties["scan_mode"].(map[string]interface{})
			if !exists || !strings.Contains(scanMode["description"].(string), "Only for skills_scan") {
				t.Error("scan_mode must be restricted to Skills details")
			}
			modelDescription := swaggerValue(t, properties, "model_id", "description").(string)
			if !strings.Contains(modelDescription, "Skills") {
				t.Error("model_id must document Skills model references")
			}
			for _, definition := range []string{"reports.ReportSummary", "reports.ReportDetail", "reports.RenderModel"} {
				description := swaggerValue(t, document, "definitions", definition, "properties", "task_type", "description").(string)
				for _, term := range []string{"skills_scan", "historical", "MCP"} {
					if !strings.Contains(description, term) {
						t.Errorf("%s.task_type must document %q", definition, term)
					}
				}
			}

			path := "/api/v1/platform/tasks"
			description := strings.ToLower(swaggerValue(t, document, "paths", path, "post", "description").(string))
			for _, term := range []string{"skills_scan", "only model_id", "required", "exactly one", "ready zip", "content must be empty", "20 mib", "100 mib", "5 mib", "2,000", "skill.md", "unknown", "static"} {
				if !strings.Contains(description, term) {
					t.Errorf("Skills create contract lacks %q", term)
				}
			}
			body := swaggerBodyParameterSchema(t, document, path, "post")
			countryCodes := swaggerValue(t, body, "properties", "country_iso_code", "enum").([]interface{})
			if !reflect.DeepEqual(countryCodes, []interface{}{"", "zh", "zh_CN", "en"}) {
				t.Error("Skills must not restrict the API language enum to the Chinese UI default")
			}
			for _, schema := range []interface{}{body, swaggerValue(t, document, "definitions", "tasks.TaskDetail")} {
				remark := swaggerValue(t, schema, "properties", "remark")
				if swaggerValue(t, remark, "maxLength") != float64(2000) {
					t.Error("remark must retain its 2,000 Unicode code point limit")
				}
				for _, field := range swaggerValue(t, schema, "required").([]interface{}) {
					if field == "remark" {
						t.Error("remark must remain optional")
					}
				}
			}
		})
	}
}

func TestAPIGuidesDocumentSkillsContract(t *testing.T) {
	for _, path := range []string{"../../docs/api/reference.md", "../../docs/api/reference.en.md"} {
		t.Run(path, func(t *testing.T) {
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, term := range []string{"skills_scan", "Skills-Scan", "SKILL.md", "20 MiB", "100 MiB", "5 MiB", "2,000", "scan_mode", "static", "remark", "model_id", "zh_CN"} {
				if !strings.Contains(string(contents), term) {
					t.Errorf("Skills guide contract lacks %q", term)
				}
			}
		})
	}
}
