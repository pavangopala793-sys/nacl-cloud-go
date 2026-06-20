package config

import (
	"os"
	"testing"
)

func TestNewConfig(t *testing.T) {
	// Set test environment variables
	os.Setenv("PORT", "9090")
	os.Setenv("DATABASE_URL", "postgres://test:test@localhost:5432/test")
	defer func() {
		os.Unsetenv("PORT")
		os.Unsetenv("DATABASE_URL")
	}()

	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig returned error: %v", err)
	}

	if cfg.Port != "9090" {
		t.Errorf("Expected Port 9090, got %s", cfg.Port)
	}

	if cfg.DatabaseURL != "postgres://test:test@localhost:5432/test" {
		t.Errorf("Expected DatabaseURL postgres://test:test@localhost:5432/test, got %s", cfg.DatabaseURL)
	}
}
