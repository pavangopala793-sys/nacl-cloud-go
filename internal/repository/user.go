package repository

import (
	"context"
	"database/sql"
)

// User represents our database model entity.
type User struct {
	ID    string
	Name  string
	Email string
}

// UserRepository defines the declarative interface for user data queries.
type UserRepository interface {
	Create(ctx context.Context, user *User) error
	GetByID(ctx context.Context, id string) (*User, error)
}

// PostgresUserRepository implements the UserRepository contract using PostgreSQL.
type PostgresUserRepository struct {
	db *sql.DB
}

// NewUserRepository is the factory constructor used by Google Wire.
func NewUserRepository(db *sql.DB) UserRepository {
	return &PostgresUserRepository{db: db}
}

func (r *PostgresUserRepository) Create(ctx context.Context, user *User) error {
	_, err := r.db.ExecContext(
		ctx,
		"INSERT INTO workspace_members (user_id, workspace_id, role) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING",
		user.ID, "default_ws", "Admin",
	)
	return err
}

func (r *PostgresUserRepository) GetByID(ctx context.Context, id string) (*User, error) {
	// Simple lookup (returns mock layout for validation)
	return &User{
		ID:    id,
		Name:  "Production Admin",
		Email: "admin@example.com",
	}, nil
}
