package apidocs

import (
	"strings"
	"testing"
)

func TestSwaggerTargetCredentialContract(t *testing.T) {
	for name, document := range loadSwaggerDocuments(t) {
		t.Run(name, func(t *testing.T) {
			fields := swaggerValue(t, document, "definitions", "targetcredentials.View", "properties").(map[string]interface{})
			for _, secret := range []string{"secret", "username", "headers", "encrypted_secret"} {
				if _, ok := fields[secret]; ok {
					t.Fatalf("response exposes %s", secret)
				}
			}
			for _, method := range []string{"put", "delete"} {
				operation := swaggerValue(t, document, "paths", "/api/v1/platform/target-credentials/{id}", method)
				_ = swaggerValue(t, operation, "responses", "409")
				_ = swaggerValue(t, operation, "responses", "428")
			}
			description := swaggerValue(t, document, "paths", "/api/v1/platform/tasks", "post", "description").(string)
			for _, term := range []string{"target_credential_id", "target_credential_revision", "same-origin", "allow_insecure_http", "infra-target-auth-v2"} {
				if !strings.Contains(description, term) {
					t.Fatalf("missing %s", term)
				}
			}
			for _, definition := range []string{"targetcredentials.View", "targetcredentials.CreateInput", "targetcredentials.UpdateInput"} {
				_ = swaggerValue(t, document, "definitions", definition, "properties", "allow_insecure_http")
			}
			for _, definition := range []string{"targetcredentials.CreateInput", "targetcredentials.UpdateInput"} {
				origin := swaggerValue(t, document, "definitions", definition, "properties", "origin", "description").(string)
				legacyFlag := swaggerValue(t, document, "definitions", definition, "properties", "allow_insecure_http", "description").(string)
				if !strings.Contains(origin, "HTTP and HTTPS are accepted by default") || !strings.Contains(legacyFlag, "ignored") {
					t.Fatalf("%s must document default HTTP support and the ignored compatibility field", definition)
				}
			}
		})
	}
}
