package brand

import (
	"context"
	"errors"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGovernedServiceAuditsAdminUpdateAndRejectsFailedOrForbiddenWrites(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	service := NewGovernedService(repository, failingBrandRecorder{})
	admin := identity.Subject{UserID: "admin", Role: identity.RoleAdmin}
	logo := testPNG(t)
	_, err := service.Update(ctx, admin, Config{ProductName: "new", PrimaryColor: "#1677FF", Logo: logo, LogoMIME: "image/png"})
	require.Error(t, err)
	stored, err := repository.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, "企业安全平台", stored.ProductName)

	audits := audit.NewMemoryRepository()
	service = NewGovernedService(repository, audit.NewService(audits))
	updated, err := service.Update(ctx, admin, Config{ProductName: "new", PrimaryColor: "#1677FF", Logo: logo, LogoMIME: "image/png"})
	require.NoError(t, err)
	assert.Equal(t, "new", updated.ProductName)
	events, err := audits.List(ctx, audit.Filter{Action: audit.ActionBrandUpdated})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, audit.OutcomePending, events[0].Outcome)
	assert.Equal(t, audit.OutcomeSuccess, events[1].Outcome)
	for _, event := range events {
		assert.NotContains(t, string(event.Metadata), string(logo))
	}

	_, err = service.Update(ctx, identity.Subject{Role: identity.RoleUser}, Config{ProductName: "blocked", PrimaryColor: "#1677FF"})
	assert.ErrorIs(t, err, ErrForbidden)
	events, err = audits.List(ctx, audit.Filter{Action: audit.ActionBrandUpdated})
	require.NoError(t, err)
	assert.Len(t, events, 2)
}

type failingBrandRecorder struct{}

func (failingBrandRecorder) Record(context.Context, identity.Subject, audit.EventInput) error {
	return errors.New("audit unavailable")
}
