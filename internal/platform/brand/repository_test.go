package brand

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestGormRepositoryPersistsGovernedBrandAndRequiresMigratedSchema(t *testing.T) {
	ctx := context.Background()
	db := openBrandPostgresDB(t)
	repository := NewGormRepository(db)
	err := repository.Init()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "aig migrate")
	require.NoError(t, database.Migrate(db))
	require.NoError(t, repository.Init())
	service := NewGovernedService(repository, audit.NewService(audit.NewGormRepository(db)))
	admin := identity.Subject{UserID: "admin", Role: identity.RoleAdmin}
	logo := testPNG(t)
	updated, err := service.Update(ctx, admin, Config{ProductName: "持久化品牌", PrimaryColor: "#1677FF", Logo: logo, LogoMIME: "image/png"})
	require.NoError(t, err)
	assert.Equal(t, logo, updated.Logo)

	restarted := NewService(NewGormRepository(db))
	stored, err := restarted.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, "持久化品牌", stored.ProductName)
	assert.Equal(t, logo, stored.Logo)
}

func TestGormRepositoryPersistsAndClearsEmptyLogoAsNotNull(t *testing.T) {
	ctx := context.Background()
	db := openBrandPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	service := NewService(NewGormRepository(db))
	admin := identity.Subject{UserID: "admin", Role: identity.RoleAdmin}

	first, err := service.Update(ctx, admin, Config{ProductName: "无标志", PrimaryColor: "#1677FF"})
	require.NoError(t, err)
	assert.NotNil(t, first.Logo)
	assert.Empty(t, first.LogoMIME)
	_, err = service.Update(ctx, admin, Config{ProductName: "有标志", PrimaryColor: "#1677FF", Logo: testPNG(t), LogoMIME: "image/png"})
	require.NoError(t, err)
	cleared, err := service.Update(ctx, admin, Config{ProductName: "再次无标志", PrimaryColor: "#1677FF", LogoMIME: "image/png"})
	require.NoError(t, err)
	assert.NotNil(t, cleared.Logo)
	assert.Empty(t, cleared.Logo)
	assert.Empty(t, cleared.LogoMIME)

	var logoIsNull bool
	require.NoError(t, db.Raw("SELECT logo IS NULL FROM report_brand_settings WHERE id = ?", currentConfigID).Scan(&logoIsNull).Error)
	assert.False(t, logoIsNull)
}

func TestGormGovernedBrandAuditFailureLeavesNoRow(t *testing.T) {
	db := openBrandPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	service := NewGovernedService(repository, failingBrandRecorder{})
	_, err := service.Update(context.Background(), identity.Subject{UserID: "admin", Role: identity.RoleAdmin}, Config{ProductName: "blocked", PrimaryColor: "#1677FF"})
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Table("report_brand_settings").Count(&count).Error)
	assert.Zero(t, count)
}

func openBrandPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn)
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	schema := "brand_repo_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, adminDB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)).Error)
	t.Cleanup(func() { require.NoError(t, adminDB.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)).Error) })
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	return db
}
