package repository

import (
	"database/sql"
	"log"
	"time"

	"github.com/nacl-org/nacl-cloud-go/internal/config"
	// Import standard Postgres driver (will be fetched when go mod tidy runs)
	_ "github.com/lib/pq" 
)

// NewPostgresDatabase initializes a PostgreSQL connection pool and registers a cleanup closer.
func NewPostgresDatabase(cfg *config.Config) (*sql.DB, func(), error) {
	log.Println("Initializing PostgreSQL connection pool...")
	
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}

	// Setup pool parameters (Google Production Best Practices)
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Verify connection is alive
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, nil, err
	}

	cleanup := func() {
		log.Println("Closing PostgreSQL connection pool...")
		if err := db.Close(); err != nil {
			log.Printf("Error closing database: %v", err)
		}
	}

	log.Println("PostgreSQL connection verified.")
	return db, cleanup, nil
}
