package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/lib/pq"
)

func main() {
	log.Println("Booting nacl-traffic-simulator-go...")

	// 1. Resolve configuration
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://nacl_engine_user:nacl_password@127.0.0.1:5432/nacl_telemetry?sslmode=disable"
	}

	intervalSecsStr := os.Getenv("SIMULATOR_INTERVAL_SECS")
	if intervalSecsStr == "" {
		intervalSecsStr = "3"
	}
	intervalSecs, err := strconv.Atoi(intervalSecsStr)
	if err != nil || intervalSecs <= 0 {
		intervalSecs = 3
	}

	log.Printf("Connecting to Postgres: %s", dbURL)
	log.Printf("Simulation interval: %d seconds", intervalSecs)

	// 2. Open DB connection pool (capped at 2 max open connections)
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Fatal: Failed to connect to database: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(10 * time.Minute)

	// Validate DB connectivity
	if err := db.Ping(); err != nil {
		log.Fatalf("Fatal: Database ping failed: %v", err)
	}
	log.Println("Database connection verified.")

	// 3. Initialize local random source (safe from global mutex bottlenecks)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handling for graceful shutdown
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-shutdownChan
		log.Println("Teardown signal received. Shutting down traffic simulator...")
		cancel()
	}()

	log.Println("Traffic simulator loop started.")

	type mockMigration struct {
		ddl           string
		riskLevel     string
		riskScore     float64
		mutatedTables int32
		heavyRewrite  bool
		locks         string
	}

	mockMigrations := []mockMigration{
		{
			ddl: `-- migration: 001_create_user_profiles_table.sql
CREATE TABLE user_profiles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    clerk_user_id VARCHAR(255) UNIQUE,
    email VARCHAR(255) NOT NULL UNIQUE,
    display_name VARCHAR(100),
    avatar_url TEXT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);`,
			riskLevel:     "MODERATE",
			riskScore:     3.2,
			mutatedTables: 1,
			heavyRewrite:  false,
			locks:         `["user_profiles"]`,
		},
		{
			ddl: `-- migration: 002_add_supabase_auth_sync_columns.sql
ALTER TABLE user_profiles 
    ADD COLUMN supabase_user_id UUID UNIQUE,
    ADD COLUMN email_verified BOOLEAN DEFAULT FALSE,
    ADD COLUMN last_sign_in_at TIMESTAMPTZ;`,
			riskLevel:     "MODERATE",
			riskScore:     4.5,
			mutatedTables: 1,
			heavyRewrite:  false,
			locks:         `["user_profiles"]`,
		},
		{
			ddl: `-- migration: 003_create_user_identities_table.sql
CREATE TABLE user_identities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES user_profiles(id) ON DELETE CASCADE,
    provider_name VARCHAR(50) NOT NULL,
    provider_user_id VARCHAR(255) NOT NULL,
    access_token_encrypted TEXT,
    refresh_token_encrypted TEXT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (provider_name, provider_user_id)
);`,
			riskLevel:     "HIGH",
			riskScore:     6.8,
			mutatedTables: 2,
			heavyRewrite:  false,
			locks:         `["user_identities", "user_profiles"]`,
		},
		{
			ddl: `-- migration: 004_add_concurrent_index_on_email.sql
CREATE INDEX CONCURRENTLY idx_user_profiles_email_lower ON user_profiles (LOWER(email));`,
			riskLevel:     "TRIVIAL",
			riskScore:     1.5,
			mutatedTables: 1,
			heavyRewrite:  false,
			locks:         `["user_profiles"]`,
		},
		{
			ddl: `-- migration: 005_drop_legacy_clerk_columns.sql
ALTER TABLE user_profiles DROP COLUMN clerk_user_id;`,
			riskLevel:     "CRITICAL",
			riskScore:     8.9,
			mutatedTables: 1,
			heavyRewrite:  true,
			locks:         `["user_profiles"]`,
		},
		{
			ddl: `-- migration: 006_create_user_sessions_table.sql
CREATE TABLE user_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES user_profiles(id) ON DELETE CASCADE,
    session_token VARCHAR(255) NOT NULL UNIQUE,
    ip_address VARCHAR(45),
    user_agent TEXT,
    is_mfa_verified BOOLEAN DEFAULT FALSE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);`,
			riskLevel:     "HIGH",
			riskScore:     7.2,
			mutatedTables: 2,
			heavyRewrite:  false,
			locks:         `["user_sessions", "user_profiles"]`,
		},
		{
			ddl: `-- migration: 007_rename_display_name_to_full_name.sql
ALTER TABLE user_profiles RENAME COLUMN display_name TO full_name;`,
			riskLevel:     "CRITICAL",
			riskScore:     9.3,
			mutatedTables: 1,
			heavyRewrite:  true,
			locks:         `["user_profiles"]`,
		},
		{
			ddl: `-- migration: 008_add_mfa_factors_table.sql
CREATE TABLE user_mfa_factors (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES user_profiles(id) ON DELETE CASCADE,
    factor_type VARCHAR(50) NOT NULL,
    status VARCHAR(50) DEFAULT 'unverified',
    secret VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);`,
			riskLevel:     "HIGH",
			riskScore:     6.5,
			mutatedTables: 2,
			heavyRewrite:  false,
			locks:         `["user_mfa_factors", "user_profiles"]`,
		},
		{
			ddl: `-- migration: 009_add_constraint_on_sessions_ip.sql
ALTER TABLE user_sessions ADD CONSTRAINT check_ip_length CHECK (char_length(ip_address) <= 45);`,
			riskLevel:     "MODERATE",
			riskScore:     4.2,
			mutatedTables: 1,
			heavyRewrite:  false,
			locks:         `["user_sessions"]`,
		},
	}

	envs := []string{"production", "staging", "development"}

	for {
		select {
		case <-ctx.Done():
			log.Println("Traffic simulator stopped cleanly.")
			return
		default:
		}

		// 4. Query feature flag to see if simulation is enabled
		var enabled bool
		flagCtx, flagCancel := context.WithTimeout(ctx, 3*time.Second)
		err := db.QueryRowContext(flagCtx, "SELECT is_enabled FROM feature_flags WHERE flag_key = 'traffic_simulation_enabled'").Scan(&enabled)
		flagCancel()

		if err != nil {
			log.Printf("Warning: Failed to fetch feature flag: %v. Retrying in next loop.", err)
			enabled = false
		}

		if !enabled {
			log.Println("Traffic simulation is disabled via feature flag.")
		} else {
			// 5. Pick random mock migration & generate metrics
			mig := mockMigrations[rng.Intn(len(mockMigrations))]
			execID := fmt.Sprintf("exec_sim_%d", rng.Uint32())
			now := time.Now().UTC()
			version := "v0.1.0"
			environment := envs[rng.Intn(len(envs))]

			// Generate random 8-byte epoch hash
			epochBytes := make([]byte, 8)
			rng.Read(epochBytes)
			epochHash := hex.EncodeToString(epochBytes)

			// Generate mock compilation metrics
			parseUs := int64(rng.Intn(4500) + 500)
			sortUs := int64(rng.Intn(900) + 100)
			ddlUs := int64(rng.Intn(7000) + 1000)
			totalUs := parseUs + sortUs + ddlUs + int64(rng.Intn(200))

			locksJSON := []byte(mig.locks)

			// 6. Insert into database
			writeCtx, writeCancel := context.WithTimeout(ctx, 5*time.Second)
			_, err = db.ExecContext(writeCtx, `
				INSERT INTO telemetry_logs (
					execution_id, timestamp_utc, nacl_engine_version, environment, lineage_epoch_hash,
					frontend_parse_us, topological_sort_us, ddl_generation_us, total_compilation_us,
					risk_level, risk_score, mutated_tables_count, heavy_rewrites_detected, 
					infrastructure_exclusive_locks, tenant_id, compiled_ddl
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			`,
				execID, now, version, environment, epochHash,
				parseUs, sortUs, ddlUs, totalUs,
				mig.riskLevel, mig.riskScore, mig.mutatedTables, mig.heavyRewrite, locksJSON,
				"admin", mig.ddl,
			)
			writeCancel()

			if err != nil {
				log.Printf("Error: Failed to insert simulated telemetry: %v", err)
			} else {
				log.Printf("Simulated telemetry event inserted: %s (Risk: %s, Score: %.2f)", execID, mig.riskLevel, mig.riskScore)
			}
		}

		// 7. Resilient interval sleep using select to prevent shutdown hangs
		select {
		case <-ctx.Done():
			log.Println("Traffic simulator stopped cleanly.")
			return
		case <-time.After(time.Duration(intervalSecs) * time.Second):
		}
	}
}
