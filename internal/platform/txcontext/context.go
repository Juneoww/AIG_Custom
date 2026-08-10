package txcontext

import (
	"context"

	"gorm.io/gorm"
)

type gormKey struct{}

// WithGorm carries an already-open GORM transaction through application
// service calls. It never starts, commits, or rolls back a transaction.
func WithGorm(ctx context.Context, tx *gorm.DB) context.Context {
	if tx == nil {
		return ctx
	}
	return context.WithValue(ctx, gormKey{}, tx)
}

// Gorm returns the transaction already carried by ctx, or the repository's
// normal connection when the caller is outside a governance unit of work.
func Gorm(ctx context.Context, fallback *gorm.DB) *gorm.DB {
	if tx, ok := ctx.Value(gormKey{}).(*gorm.DB); ok && tx != nil {
		return tx.WithContext(ctx)
	}
	return fallback.WithContext(ctx)
}
