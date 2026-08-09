package admin

import (
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
)

type CreateUserRequest struct {
	Username string        `json:"username" binding:"required"`
	Password string        `json:"password" binding:"required"`
	Role     identity.Role `json:"role" binding:"required"`
}

type AssignRoleRequest struct {
	Role identity.Role `json:"role" binding:"required"`
}

type SetActiveRequest struct {
	Active *bool `json:"active" binding:"required"`
}

type UserResponse struct {
	ID                 string        `json:"id"`
	Username           string        `json:"username"`
	Role               identity.Role `json:"role"`
	Active             bool          `json:"active"`
	MustChangePassword bool          `json:"must_change_password"`
	CreatedAt          time.Time     `json:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at"`
}

func userResponse(user *identity.User) UserResponse {
	return UserResponse{
		ID:                 user.ID,
		Username:           user.Username,
		Role:               user.Role,
		Active:             user.Active,
		MustChangePassword: user.MustChangePassword,
		CreatedAt:          user.CreatedAt,
		UpdatedAt:          user.UpdatedAt,
	}
}
