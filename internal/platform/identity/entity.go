package identity

import "time"

type Role string

const (
	RoleAdmin   Role = "admin"
	RoleUser    Role = "user"
	RoleAuditor Role = "auditor"
)

type User struct {
	ID                 string    `gorm:"primaryKey;column:id"`
	Username           string    `gorm:"uniqueIndex;not null"`
	PasswordHash       string    `gorm:"not null"`
	Role               Role      `gorm:"not null"`
	Active             bool      `gorm:"not null;default:true"`
	MustChangePassword bool      `gorm:"not null;default:true"`
	CreatedAt          time.Time `gorm:"not null"`
	UpdatedAt          time.Time `gorm:"not null"`
}

func (User) TableName() string { return "identity_users" }

type Session struct {
	ID        string     `gorm:"primaryKey;column:id"`
	UserID    string     `gorm:"index;not null"`
	TokenHash string     `gorm:"uniqueIndex;not null"`
	ExpiresAt time.Time  `gorm:"index;not null"`
	RevokedAt *time.Time `gorm:"index"`
	CreatedAt time.Time  `gorm:"not null"`
}

func (Session) TableName() string { return "identity_sessions" }

type PasswordReset struct {
	ID        string     `gorm:"primaryKey;column:id"`
	UserID    string     `gorm:"index;not null"`
	TokenHash string     `gorm:"uniqueIndex;not null"`
	ExpiresAt time.Time  `gorm:"index;not null"`
	UsedAt    *time.Time `gorm:"index"`
	CreatedAt time.Time  `gorm:"not null"`
}

func (PasswordReset) TableName() string { return "identity_password_resets" }

type Subject struct {
	UserID             string
	Username           string
	Role               Role
	MustChangePassword bool
}
