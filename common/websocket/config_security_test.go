package websocket

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFileUploadConfigUsesDeliveryUploadDir(t *testing.T) {
	t.Setenv("FILE_UPLOAD_DIR", "legacy-upload-dir")
	t.Setenv("UPLOAD_DIR", "delivery-upload-dir")
	assert.Equal(t, "delivery-upload-dir", LoadFileUploadConfigFromEnv().UploadDir)
}
