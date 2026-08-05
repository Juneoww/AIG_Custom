package identity

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"strings"
)

func BootstrapAdmin(ctx context.Context, service *Service, username, password string) error {
	exists, err := service.repo.HasAdministrator(ctx)
	if err != nil {
		return err
	}
	if exists {
		return ErrAdminAlreadyBootstrapped
	}
	_, err = service.CreateUser(ctx, CreateUserInput{Username: username, Password: password, Role: RoleAdmin, MustChangePassword: true})
	return err
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
