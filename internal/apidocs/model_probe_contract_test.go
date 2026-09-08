package apidocs

import (
	"strings"
	"testing"
)

func TestSwaggerModelProbeContract(t *testing.T) {
	for name, doc := range loadSwaggerDocuments(t) {
		t.Run(name, func(t *testing.T) {
			for _, path := range []string{"/api/v1/platform/models/test", "/api/v1/platform/models/{modelID}/test"} {
				op := swaggerValue(t, doc, "paths", path, "post")
				for _, status := range []string{"200", "400", "401", "403", "429", "500"} {
					_ = swaggerValue(t, op, "responses", status)
				}
				desc := swaggerValue(t, op, "description").(string)
				for _, term := range []string{"HTTP", "30", "CSRF", "Token", "redirect"} {
					if !strings.Contains(desc, term) {
						t.Errorf("%s misses %s", path, term)
					}
				}
			}
			fields := swaggerValue(t, doc, "definitions", "models.ProbeResult", "properties").(map[string]interface{})
			for _, field := range []string{"status", "code", "message", "elapsed_ms"} {
				if fields[field] == nil {
					t.Errorf("missing %s", field)
				}
			}
			for _, field := range []string{"token", "raw_response", "headers"} {
				if fields[field] != nil {
					t.Errorf("unsafe field %s", field)
				}
			}
			desc := swaggerValue(t, doc, "paths", "/api/v1/platform/models/{modelID}", "put", "description").(string)
			if !strings.Contains(desc, "new Token") {
				t.Error("URL change must require a new Token")
			}
		})
	}
}
