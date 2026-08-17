package database

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadYamlModelsRejectsOversizedFileWithFixedError(t *testing.T) {
	root := enterYAMLModelTestDirectory(t)
	marker := "oversized-sensitive-marker"
	data := []byte(strings.Repeat(marker, (2<<20)/len(marker)+2))
	require.NoError(t, os.WriteFile(filepath.Join(root, YamlModelPath), data, 0o600))

	_, err := (&ModelStore{}).LoadYamlModels()
	if assert.Error(t, err) {
		assert.EqualError(t, err, "YAML模型配置超出大小限制")
		assert.NotContains(t, err.Error(), root)
		assert.NotContains(t, err.Error(), marker)
	}
}

func TestLoadYamlModelsRejectsTooManyEntriesWithFixedError(t *testing.T) {
	root := enterYAMLModelTestDirectory(t)
	var yamlData strings.Builder
	for index := 0; index < 1001; index++ {
		fmt.Fprintf(&yamlData, "- model_id: model-%04d\n  model_name: provider\n  token: secret\n", index)
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, YamlModelPath), []byte(yamlData.String()), 0o600))

	models, err := (&ModelStore{}).LoadYamlModels()
	assert.Nil(t, models)
	if assert.Error(t, err) {
		assert.EqualError(t, err, "YAML模型配置条目过多")
		assert.NotContains(t, err.Error(), root)
	}
}

func enterYAMLModelTestDirectory(t *testing.T) string {
	t.Helper()
	previous, err := os.Getwd()
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "db"), 0o700))
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { require.NoError(t, os.Chdir(previous)) })
	return root
}
