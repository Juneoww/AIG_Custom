package brand

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
)

const (
	maxLogoSize         = 1 << 20
	maxLogoDimension    = 4096
	maxLogoPixels       = 16 << 20
	maxProductNameRunes = 128
	maxWatermarkRunes   = 64
)

var (
	ErrForbidden = errors.New("无权更新品牌配置")
	ErrInvalid   = errors.New("品牌配置无效")
	colorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

type Repository interface {
	Get(context.Context) (Config, error)
	Put(context.Context, Config) error
}

type MemoryRepository struct {
	mu     sync.Mutex
	config *Config
}

func NewMemoryRepository() *MemoryRepository { return &MemoryRepository{} }

func (repository *MemoryRepository) Get(_ context.Context) (Config, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.config == nil {
		return defaultConfig(), nil
	}
	return cloneConfig(*repository.config), nil
}

func (repository *MemoryRepository) Put(_ context.Context, config Config) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	copy := cloneConfig(config)
	repository.config = &copy
	return nil
}

type Service struct {
	repository Repository
	audits     audit.Recorder
	now        func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, now: func() time.Time { return time.Now().UTC() }}
}

func NewGovernedService(repository Repository, audits audit.Recorder) *Service {
	service := NewService(repository)
	service.audits = audits
	return service
}

func (service *Service) Get(ctx context.Context) (Config, error) {
	config, err := service.repository.Get(ctx)
	if err != nil {
		return Config{}, err
	}
	return cloneConfig(config), nil
}

func (service *Service) Update(ctx context.Context, subject identity.Subject, config Config) (Config, error) {
	if subject.Role != identity.RoleAdmin {
		return Config{}, ErrForbidden
	}
	config = normalizedConfig(config)
	if err := validateConfig(config); err != nil {
		return Config{}, err
	}
	config.UpdatedBy = subject.UserID
	config.UpdatedAt = service.now()
	if service.audits != nil {
		mutation, err := audit.BeginMutation(ctx, service.audits, subject, audit.EventInput{
			Action: audit.ActionBrandUpdated, ResourceType: "brand", ResourceID: "current",
			Metadata: map[string]any{"product_name": config.ProductName, "primary_color": config.PrimaryColor, "logo_mime": config.LogoMIME},
		})
		if err != nil {
			return Config{}, err
		}
		err = mutation.Run(ctx, "current", map[string]any{"product_name": config.ProductName, "primary_color": config.PrimaryColor, "logo_mime": config.LogoMIME}, func(transactionContext context.Context) error {
			return service.repository.Put(transactionContext, config)
		})
		if err != nil {
			return Config{}, err
		}
		return cloneConfig(config), nil
	}
	if err := service.repository.Put(ctx, config); err != nil {
		return Config{}, err
	}
	return cloneConfig(config), nil
}

func defaultConfig() Config {
	return Config{ProductName: "企业安全平台", PrimaryColor: "#1677FF", Logo: []byte{}}
}

func cloneConfig(config Config) Config {
	config.Logo = append([]byte{}, config.Logo...)
	return config
}

func normalizedConfig(config Config) Config {
	config.ProductName = strings.TrimSpace(config.ProductName)
	config.PrimaryColor = strings.TrimSpace(config.PrimaryColor)
	config.LogoMIME = strings.TrimSpace(config.LogoMIME)
	config.Watermark = strings.TrimSpace(config.Watermark)
	if len(config.Logo) == 0 {
		config.Logo = []byte{}
		config.LogoMIME = ""
	}
	return config
}

func validateConfig(config Config) error {
	if config.ProductName == "" || len([]rune(config.ProductName)) > maxProductNameRunes || len([]rune(config.Watermark)) > maxWatermarkRunes ||
		!colorPattern.MatchString(config.PrimaryColor) || len(config.Logo) > maxLogoSize {
		return ErrInvalid
	}
	if len(config.Logo) == 0 {
		return nil
	}
	if config.LogoMIME != "image/png" && config.LogoMIME != "image/jpeg" {
		return ErrInvalid
	}
	decoded, format, err := image.DecodeConfig(bytes.NewReader(config.Logo))
	if err != nil || formatMIME(format) != config.LogoMIME || decoded.Width <= 0 || decoded.Height <= 0 || decoded.Width > maxLogoDimension || decoded.Height > maxLogoDimension || int64(decoded.Width)*int64(decoded.Height) > maxLogoPixels {
		return ErrInvalid
	}
	if _, decodedFormat, err := image.Decode(bytes.NewReader(config.Logo)); err != nil || decodedFormat != format {
		return ErrInvalid
	}
	return nil
}

func formatMIME(format string) string {
	switch format {
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	default:
		return ""
	}
}
