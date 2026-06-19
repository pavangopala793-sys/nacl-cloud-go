package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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

	envs := []string{"production", "staging", "development"}
	lockChoices := [][]string{
		{"users"},
		{"orders", "payments"},
		{"products", "inventory"},
		{},
	}

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
			// 5. Generate mock telemetry data
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

			// Generate mock risk metrics
			riskScore := rng.Float64() * 10.0
			var riskLevel string
			if riskScore < 2.0 {
				riskLevel = "Trivial"
			} else if riskScore < 5.0 {
				riskLevel = "Moderate"
			} else if riskScore < 8.0 {
				riskLevel = "High"
			} else {
				riskLevel = "Critical"
			}

			mutatedTables := int32(rng.Intn(8) + 1)
			heavyRewrite := rng.Float64() > 0.8

			// Choose random locks
			locks := lockChoices[rng.Intn(len(lockChoices))]
			locksJSON, err := json.Marshal(locks)
			if err != nil {
				locksJSON = []byte("[]")
			}

			// 6. Insert into database
			writeCtx, writeCancel := context.WithTimeout(ctx, 5*time.Second)
			_, err = db.ExecContext(writeCtx, `
				INSERT INTO telemetry_logs (
					execution_id, timestamp_utc, nacl_engine_version, environment, lineage_epoch_hash,
					frontend_parse_us, topological_sort_us, ddl_generation_us, total_compilation_us,
					risk_level, risk_score, mutated_tables_count, heavy_rewrites_detected, infrastructure_exclusive_locks,
					tenant_id
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
			`,
				execID, now, version, environment, epochHash,
				parseUs, sortUs, ddlUs, totalUs,
				riskLevel, riskScore, mutatedTables, heavyRewrite, locksJSON,
				"admin",
			)
			writeCancel()

			if err != nil {
				log.Printf("Error: Failed to insert simulated telemetry: %v", err)
			} else {
				log.Printf("Simulated telemetry event inserted: %s (Risk: %s, Score: %.2f)", execID, riskLevel, riskScore)
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
