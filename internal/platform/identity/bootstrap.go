package identity

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
)

func BootstrapAdmin(ctx context.Context, service *Service, username, password string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return ErrInvalidCredentials
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	now := service.now()
	created, err := service.repo.CreateInitialAdministrator(ctx, &User{
		ID:                 uuid.NewString(),
		Username:           username,
		PasswordHash:       hash,
		Role:               RoleAdmin,
		Active:             true,
		MustChangePassword: true,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
	if err != nil {
		return err
	}
	if !created {
		return ErrAdminAlreadyBootstrapped
	}
	return nil
}
func ReadBootstrapPassword(stdin io.Reader) (string, error) {
	if value, ok := os.LookupEnv("AIG_BOOTSTRAP_ADMIN_PASSWORD"); ok {
		if strings.TrimSpace(value) == "" {
			return "", ErrInvalidPassword
		}
		return value, nil
	}
	value, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ErrInvalidPassword
	}
	return value, nil
}
