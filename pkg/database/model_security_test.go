package database

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyModelSecretNeverAppearsInJSONOrStructuredFormatting(t *testing.T) {
	model := Model{ModelID: "legacy", ModelName: "demo", Token: "legacy-plain-token", BaseURL: "https://models.invalid"}
	encoded, err := json.Marshal(model)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "legacy-plain-token")
	assert.NotContains(t, fmt.Sprintf("%+v", model), "legacy-plain-token")
}
