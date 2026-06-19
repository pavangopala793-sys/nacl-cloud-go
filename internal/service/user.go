package service

import (
	"context"
	"github.com/nacl-org/nacl-cloud-go/internal/repository"
)

// UserService coordinates business processes. It is decoupled from HTTP routers.
type UserService struct {
	repo repository.UserRepository
}

// NewUserService is the factory constructor used by Google Wire.
func NewUserService(repo repository.UserRepository) *UserService {
	return &UserService{repo: repo}
}

func (s *UserService) CreateUser(ctx context.Context, user *repository.User) error {
	return s.repo.Create(ctx, user)
}

func (s *UserService) GetUserByID(ctx context.Context, id string) (*repository.User, error) {
	return s.repo.GetByID(ctx, id)
}
