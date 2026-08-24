package brand

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicBrandWhitelistAndEmptyLogo(t *testing.T) {
	view, err := NewService(NewMemoryRepository()).GetPublic(context.Background())
	require.NoError(t, err)

	payload, err := json.Marshal(view)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(payload, &fields))
	assert.ElementsMatch(t, []string{"product_name", "primary_color", "logo_data_url"}, mapKeys(fields))
	assert.Equal(t, "AI 安全治理平台", fields["product_name"])
	assert.Equal(t, "#1677FF", fields["primary_color"])
	assert.Equal(t, "", fields["logo_data_url"])
}

func TestPublicBrandEncodesValidatedPNGAndJPEG(t *testing.T) {
	for _, testCase := range []struct {
		name string
		mime string
		logo func(*testing.T) []byte
	}{
		{name: "png", mime: "image/png", logo: testPNG},
		{name: "jpeg", mime: "image/jpeg", logo: testJPEG},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := NewService(NewMemoryRepository())
			logo := testCase.logo(t)
			_, err := service.Update(context.Background(), identity.Subject{Role: identity.RoleAdmin}, Config{
				ProductName: "configured", PrimaryColor: "#010203", Logo: logo, LogoMIME: testCase.mime,
				Watermark: "private", UpdatedBy: "private-admin",
			})
			require.NoError(t, err)

			view, err := service.GetPublic(context.Background())
			require.NoError(t, err)
			assert.Equal(t, "data:"+testCase.mime+";base64,"+base64.StdEncoding.EncodeToString(logo), view.LogoDataURL)
		})
	}
}

func TestPublicBrandPreservesAdministratorConfiguration(t *testing.T) {
	repository := NewMemoryRepository()
	service := NewService(repository)
	_, err := service.Update(context.Background(), identity.Subject{Role: identity.RoleAdmin}, Config{
		ProductName: "管理员品牌", PrimaryColor: "#AABBCC",
	})
	require.NoError(t, err)

	view, err := service.GetPublic(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "管理员品牌", view.ProductName)
	assert.Equal(t, "#AABBCC", view.PrimaryColor)
}

func mapKeys(fields map[string]any) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	return keys
}
