package brand

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/txcontext"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const currentConfigID = "current"

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) TransactionDB() *gorm.DB { return repository.db }

func (repository *GormRepository) Init() error {
	if repository == nil || repository.db == nil || !repository.db.Migrator().HasTable("report_brand_settings") {
		return fmt.Errorf("品牌数据库尚未迁移，请先运行 aig migrate：缺少表 report_brand_settings")
	}
	return nil
}

func (repository *GormRepository) Get(ctx context.Context) (Config, error) {
	if repository == nil || repository.db == nil {
		return Config{}, errors.New("品牌数据库未配置")
	}
	var record brandRecord
	err := txcontext.Gorm(ctx, repository.db).Where("id = ?", currentConfigID).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return defaultConfig(), nil
	}
	if err != nil {
		return Config{}, err
	}
	return record.config(), nil
}

func (repository *GormRepository) Put(ctx context.Context, config Config) error {
	if repository == nil || repository.db == nil {
		return errors.New("品牌数据库未配置")
	}
	record := brandRecord{ID: currentConfigID, ProductName: config.ProductName, PrimaryColor: config.PrimaryColor,
		Logo: append([]byte{}, config.Logo...), LogoMIME: config.LogoMIME, Watermark: config.Watermark,
		UpdatedBy: config.UpdatedBy, UpdatedAt: config.UpdatedAt}
	return txcontext.Gorm(ctx, repository.db).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"product_name", "primary_color", "logo", "logo_mime", "watermark", "updated_by", "updated_at"}),
	}).Create(&record).Error
}

type brandRecord struct {
	ID           string    `gorm:"primaryKey;column:id"`
	ProductName  string    `gorm:"column:product_name"`
	PrimaryColor string    `gorm:"column:primary_color"`
	Logo         []byte    `gorm:"column:logo"`
	LogoMIME     string    `gorm:"column:logo_mime"`
	Watermark    string    `gorm:"column:watermark"`
	UpdatedBy    string    `gorm:"column:updated_by"`
	UpdatedAt    time.Time `gorm:"column:updated_at"`
}

func (brandRecord) TableName() string { return "report_brand_settings" }

func (record brandRecord) config() Config {
	return Config{ProductName: record.ProductName, PrimaryColor: record.PrimaryColor, Logo: append([]byte{}, record.Logo...),
		LogoMIME: record.LogoMIME, Watermark: record.Watermark, UpdatedBy: record.UpdatedBy, UpdatedAt: record.UpdatedAt}
}
