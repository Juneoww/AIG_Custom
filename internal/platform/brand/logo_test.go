package brand

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	return testPNGDimensions(t, 1, 1)
}

func testPNGDimensions(t *testing.T, width, height int) []byte {
	t.Helper()
	logo := image.NewGray(image.Rect(0, 0, width, height))
	logo.SetGray(0, 0, color.Gray{Y: 0xff})
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, logo))
	return encoded.Bytes()
}

func testJPEG(t *testing.T) []byte {
	t.Helper()
	logo := image.NewGray(image.Rect(0, 0, 1, 1))
	logo.SetGray(0, 0, color.Gray{Y: 0xff})
	var encoded bytes.Buffer
	require.NoError(t, jpeg.Encode(&encoded, logo, nil))
	return encoded.Bytes()
}
