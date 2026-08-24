package brand

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceUpdatesBrandingForAdminsAndCopiesLogo(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	service := NewService(repository)
	admin := identity.Subject{Role: identity.RoleAdmin}
	logo := testPNG(t)

	updated, err := service.Update(ctx, admin, Config{
		ProductName:  "企业安全平台",
		PrimaryColor: "#1677FF",
		Logo:         logo,
		LogoMIME:     "image/png",
		Watermark:    "内部",
	})
	require.NoError(t, err)
	assert.Equal(t, "企业安全平台", updated.ProductName)
	assert.Equal(t, "#1677FF", updated.PrimaryColor)
	assert.Equal(t, logo, updated.Logo)
	assert.Equal(t, "image/png", updated.LogoMIME)
	assert.Equal(t, "内部", updated.Watermark)

	updated.Logo[0] = 'X'
	stored, err := service.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, logo, stored.Logo)

	_, err = service.Update(ctx, identity.Subject{}, Config{ProductName: "unapproved"})
	assert.True(t, errors.Is(err, ErrForbidden))
}

func TestBrandServiceNormalizesEmptyLogo(t *testing.T) {
	service := NewService(NewMemoryRepository())
	current, err := service.Get(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "AI 安全治理平台", current.ProductName)
	assert.NotNil(t, current.Logo)
	assert.Empty(t, current.Logo)
	assert.Empty(t, current.LogoMIME)

	updated, err := service.Update(context.Background(), identity.Subject{Role: identity.RoleAdmin}, Config{
		ProductName: "企业安全平台", PrimaryColor: "#1677FF", LogoMIME: "image/png",
	})
	require.NoError(t, err)
	assert.NotNil(t, updated.Logo)
	assert.Empty(t, updated.Logo)
	assert.Empty(t, updated.LogoMIME)
}

func TestServiceRejectsInvalidPrimaryColor(t *testing.T) {
	service := NewService(NewMemoryRepository())

	_, err := service.Update(context.Background(), identity.Subject{Role: identity.RoleAdmin}, Config{
		ProductName:  "企业安全平台",
		PrimaryColor: "1677FF",
	})
	assert.True(t, errors.Is(err, ErrInvalid))
}

func TestBrandServiceRejectsTextThatCannotFitImmutableReportLayouts(t *testing.T) {
	service := NewService(NewMemoryRepository())
	for _, config := range []Config{
		{ProductName: strings.Repeat("产", 129), PrimaryColor: "#1677FF"},
		{ProductName: "企业安全平台", PrimaryColor: "#1677FF", Watermark: strings.Repeat("水", 65)},
	} {
		_, err := service.Update(context.Background(), identity.Subject{Role: identity.RoleAdmin}, config)
		assert.ErrorIs(t, err, ErrInvalid)
	}
}

func TestBrandServiceValidatesLogoContentBeforeGovernanceWrite(t *testing.T) {
	for _, valid := range []struct {
		name, mime string
		logo       func(*testing.T) []byte
	}{
		{name: "png", mime: "image/png", logo: testPNG},
		{name: "jpeg", mime: "image/jpeg", logo: testJPEG},
	} {
		t.Run("accepts "+valid.name, func(t *testing.T) {
			service := NewService(NewMemoryRepository())
			updated, err := service.Update(context.Background(), identity.Subject{Role: identity.RoleAdmin}, Config{
				ProductName: valid.name, PrimaryColor: "#1677FF", Logo: valid.logo(t), LogoMIME: valid.mime,
			})
			require.NoError(t, err)
			assert.Equal(t, valid.mime, updated.LogoMIME)
		})
	}

	pngLogo := testPNG(t)
	jpegLogo := testJPEG(t)
	truncatedPNG := append([]byte(nil), pngLogo[:33]...)
	for _, invalid := range []struct {
		name, mime string
		logo       []byte
	}{
		{name: "forged bytes", mime: "image/png", logo: []byte("private-fake-logo")},
		{name: "png declared jpeg", mime: "image/jpeg", logo: pngLogo},
		{name: "jpeg declared png", mime: "image/png", logo: jpegLogo},
		{name: "svg", mime: "image/svg+xml", logo: []byte("<svg>private-logo</svg>")},
		{name: "oversized dimensions", mime: "image/png", logo: testPNGDimensions(t, 4097, 1)},
		{name: "truncated image", mime: "image/png", logo: truncatedPNG},
	} {
		t.Run("rejects "+invalid.name, func(t *testing.T) {
			ctx := context.Background()
			repository := NewMemoryRepository()
			audits := audit.NewMemoryRepository()
			service := NewGovernedService(repository, audit.NewService(audits))
			_, err := service.Update(ctx, identity.Subject{UserID: "admin", Role: identity.RoleAdmin}, Config{
				ProductName: invalid.name, PrimaryColor: "#1677FF", Logo: invalid.logo, LogoMIME: invalid.mime,
			})
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalid)
			assert.NotContains(t, err.Error(), string(invalid.logo))
			stored, getErr := repository.Get(ctx)
			require.NoError(t, getErr)
			assert.Equal(t, "AI 安全治理平台", stored.ProductName)
			events, listErr := audits.List(ctx, audit.Filter{Action: audit.ActionBrandUpdated})
			require.NoError(t, listErr)
			assert.Empty(t, events)
		})
	}
}
