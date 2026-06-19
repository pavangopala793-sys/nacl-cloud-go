package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

// CompilerMetrics maps to the compiler performance times.
type CompilerMetrics struct {
	FrontendParseUs    int64 `json:"frontend_parse_us"`
	TopologicalSortUs  int64 `json:"topological_sort_us"`
	DdlGenerationUs    int64 `json:"ddl_generation_us"`
	TotalCompilationUs int64 `json:"total_compilation_us"`
}

// BlastRadius maps to the risk classification and locks.
type BlastRadius struct {
	RiskLevel                    string          `json:"risk_level"`
	RiskScore                    float64         `json:"risk_score"`
	MutatedTablesCount           int32           `json:"mutated_tables_count"`
	HeavyRewritesDetected        bool            `json:"heavy_rewrites_detected"`
	InfrastructureExclusiveLocks json.RawMessage `json:"infrastructure_exclusive_locks"`
}

// TelemetryEvent is the parsed representation of a telemetry payload.
type TelemetryEvent struct {
	ExecutionID       string           `json:"execution_id"`
	TimestampUTC      string           `json:"timestamp_utc"`
	NaclEngineVersion string           `json:"nacl_engine_version"`
	Environment       string           `json:"environment"`
	LineageEpochHash  string           `json:"lineage_epoch_hash"`
	TenantID          string           `json:"tenant_id"`
	CompilerMetrics   *CompilerMetrics `json:"compiler_metrics"`
	BlastRadius       *BlastRadius     `json:"blast_radius"`
}

// Reset clears the fields to allow safe reuse of the pooled struct.
func (e *TelemetryEvent) Reset() {
	e.ExecutionID = ""
	e.TimestampUTC = ""
	e.NaclEngineVersion = ""
	e.Environment = ""
	e.LineageEpochHash = ""
	e.TenantID = ""
	if e.CompilerMetrics != nil {
		e.CompilerMetrics.FrontendParseUs = 0
		e.CompilerMetrics.TopologicalSortUs = 0
		e.CompilerMetrics.DdlGenerationUs = 0
		e.CompilerMetrics.TotalCompilationUs = 0
	} else {
		e.CompilerMetrics = &CompilerMetrics{}
	}
	if e.BlastRadius != nil {
		e.BlastRadius.RiskLevel = ""
		e.BlastRadius.RiskScore = 0.0
		e.BlastRadius.MutatedTablesCount = 0
		e.BlastRadius.HeavyRewritesDetected = false
		e.BlastRadius.InfrastructureExclusiveLocks = nil
	} else {
		e.BlastRadius = &BlastRadius{}
	}
}

// Global struct pool for parsing optimization.
var eventPool = sync.Pool{
	New: func() interface{} {
		return &TelemetryEvent{
			CompilerMetrics: &CompilerMetrics{},
			BlastRadius:     &BlastRadius{},
		}
	},
}

func main() {
	log.Println("Booting nacl-telemetry-worker-go...")

	// 1. Connect to PostgreSQL
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://nacl_engine_user:nacl_password@127.0.0.1:5432/nacl_telemetry?sslmode=disable"
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Fatal: Failed to open PostgreSQL connection: %v", err)
	}
	defer db.Close()

	// Configure pool parameters matching systems-level architecture
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Database Ping with retry loop to survive service boot sequence
	dbPingRetry(db)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 2. Run Database Migrations
	migrationsPath := findMigrationsPath(os.Getenv("MIGRATIONS_PATH"))
	migrationCtx, migrationCancel := context.WithTimeout(ctx, 30*time.Second)
	if err := runMigrations(migrationCtx, db, migrationsPath); err != nil {
		migrationCancel()
		log.Fatalf("Fatal: Database migrations failed: %v", err)
	}
	migrationCancel()

	// 3. Connect to Redis
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://127.0.0.1:6379/"
	}

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Fatal: Invalid Redis URL: %v", err)
	}

	rdb := redis.NewClient(opt)
	defer rdb.Close()

	// Concurrency Limiter Semaphore (Max 50 workers)
	sem := make(chan struct{}, 50)
	var wg sync.WaitGroup

	// Subscription and Reconnect loop
	go func() {
		backoff := 1 * time.Second
		maxBackoff := 60 * time.Second

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			log.Println("Subscribing to channel 'nacl_telemetry_events'...")
			pubsub := rdb.Subscribe(ctx, "nacl_telemetry_events")

			// Verify subscription
			_, err := pubsub.Receive(ctx)
			if err != nil {
				log.Printf("Subscription failed: %v. Retrying in %v...", err, backoff)
				pubsub.Close()
				time.Sleep(backoff)
				backoff = min(backoff*2, maxBackoff)
				continue
			}

			log.Println("Subscribed successfully to nacl_telemetry_events. Listening...")
			backoff = 1 * time.Second // Reset backoff on success

			ch := pubsub.Channel()

		readLoop:
			for {
				select {
				case <-ctx.Done():
					pubsub.Close()
					return
				case msg, ok := <-ch:
					if !ok {
						log.Println("PubSub channel closed. Redis connection dropped.")
						break readLoop
					}

					// Rate limit: acquire worker slot or log drop if channel saturates
					select {
					case sem <- struct{}{}:
						wg.Add(1)
						go func(payload string) {
							defer func() {
								<-sem
								wg.Done()
							}()
							processMessage(ctx, db, payload)
						}(msg.Payload)
					default:
						// Non-blocking drop log to protect the connection buffer from backpressure
						log.Printf("Warning: Telemetry worker pool saturated (50/50). Dropping telemetry event to avoid network buffer exhaustion.")
					}
				}
			}

			pubsub.Close()
			log.Printf("Attempting reconnection in %v...", backoff)
			time.Sleep(backoff)
			backoff = min(backoff*2, maxBackoff)
		}
	}()

	// Signal handling for graceful shutdown
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	<-shutdownChan
	log.Println("Shutting down telemetry worker. Waiting for running workers to finish...")
	cancel()
	wg.Wait()
	log.Println("Telemetry worker shutdown complete.")
}

func dbPingRetry(db *sql.DB) {
	backoff := 1 * time.Second
	for i := 0; i < 10; i++ {
		err := db.Ping()
		if err == nil {
			log.Println("Connected to PostgreSQL successfully.")
			return
		}
		log.Printf("Warning: PostgreSQL ping failed: %v. Retrying in %v...", err, backoff)
		time.Sleep(backoff)
		backoff *= 2
	}
	log.Fatalf("Fatal: Could not establish connection to PostgreSQL.")
}

func findMigrationsPath(cfgPath string) string {
	if cfgPath != "" {
		return cfgPath
	}
	candidates := []string{
		"../gravitan/migrations",
		"../../gravitan/migrations",
		"../../../gravitan/migrations",
		"./gravitan/migrations",
		"/app/migrations",
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			log.Printf("Selected migrations directory: %s", c)
			return c
		}
	}
	return "../../../gravitan/migrations" // fallback default
}

func runMigrations(ctx context.Context, db *sql.DB, migrationsPath string) error {
	log.Printf("Starting database migrations using path: %s", migrationsPath)

	// Open transaction to acquire advisory lock and run all migrations
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to start migration transaction: %w", err)
	}
	defer tx.Rollback()

	log.Println("Acquiring database advisory lock for migrations...")
	// Unique 64-bit advisory lock key derived from hashing the lock name namespace
	_, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(5942385732947209)")
	if err != nil {
		return fmt.Errorf("failed to acquire advisory lock: %w", err)
	}
	log.Println("Database advisory lock acquired.")

	// Create schema_migrations table if not exists
	_, err = tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version BIGINT PRIMARY KEY,
			dirty BOOLEAN NOT NULL DEFAULT FALSE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	// Read migration directory
	files, err := os.ReadDir(migrationsPath)
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	type migration struct {
		version  int64
		filename string
		path     string
	}

	var migrations []migration
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}
		parts := strings.SplitN(file.Name(), "_", 2)
		if len(parts) < 2 {
			continue
		}
		version, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}
		migrations = append(migrations, migration{
			version:  version,
			filename: file.Name(),
			path:     filepath.Join(migrationsPath, file.Name()),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	for _, m := range migrations {
		var exists bool
		err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)", m.version).Scan(&exists)
		if err != nil {
			return fmt.Errorf("failed to check migration status for version %d: %w", m.version, err)
		}

		if exists {
			continue
		}

		log.Printf("Executing migration: %s (version: %d)", m.filename, m.version)
		content, err := os.ReadFile(m.path)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", m.filename, err)
		}

		// Execute migration SQL inside the same transaction
		_, err = tx.ExecContext(ctx, string(content))
		if err != nil {
			return fmt.Errorf("migration %s failed: %w", m.filename, err)
		}

		// Register migration inside the same transaction
		_, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, dirty) VALUES ($1, FALSE)", m.version)
		if err != nil {
			return fmt.Errorf("failed to register migration version %d: %w", m.version, err)
		}
	}

	// Commit transaction releases the advisory lock
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit migration transaction: %w", err)
	}

	log.Println("Database migrations check complete.")
	return nil
}

func processMessage(ctx context.Context, db *sql.DB, payload string) {
	event := eventPool.Get().(*TelemetryEvent)
	event.Reset()
	defer eventPool.Put(event)

	if err := json.Unmarshal([]byte(payload), event); err != nil {
		log.Printf("Error: Failed to decode telemetry JSON: %v. Raw payload: %s", err, payload)
		return
	}

	// Default values
	if event.ExecutionID == "" {
		event.ExecutionID = "unknown"
	}
	if event.TenantID == "" {
		event.TenantID = "admin"
	}

	// Parse timestamp or fallback to UTC Now
	parsedTime := time.Now().UTC()
	if event.TimestampUTC != "" {
		if t, err := time.Parse(time.RFC3339, event.TimestampUTC); err == nil {
			parsedTime = t
		} else {
			log.Printf("Warning: Failed to parse timestamp_utc '%s' using RFC3339. Defaulting to time.Now().", event.TimestampUTC)
		}
	}

	// Nested structure fields mapping
	var parseUs, sortUs, ddlUs, totalUs int64
	if event.CompilerMetrics != nil {
		parseUs = event.CompilerMetrics.FrontendParseUs
		sortUs = event.CompilerMetrics.TopologicalSortUs
		ddlUs = event.CompilerMetrics.DdlGenerationUs
		totalUs = event.CompilerMetrics.TotalCompilationUs
	}

	var riskLevel string
	var riskScore float64
	var mutatedTables int32
	var heavyRewrites bool
	var locksJSON []byte

	if event.BlastRadius != nil {
		riskLevel = event.BlastRadius.RiskLevel
		riskScore = event.BlastRadius.RiskScore
		mutatedTables = event.BlastRadius.MutatedTablesCount
		heavyRewrites = event.BlastRadius.HeavyRewritesDetected
		if event.BlastRadius.InfrastructureExclusiveLocks != nil {
			locksJSON = event.BlastRadius.InfrastructureExclusiveLocks
		}
	}

	if locksJSON == nil {
		locksJSON = []byte("[]")
	}

	// Write to database with 5s timeout context
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := db.ExecContext(writeCtx, `
		INSERT INTO telemetry_logs (
			execution_id, timestamp_utc, nacl_engine_version, environment, lineage_epoch_hash,
			frontend_parse_us, topological_sort_us, ddl_generation_us, total_compilation_us,
			risk_level, risk_score, mutated_tables_count, heavy_rewrites_detected, infrastructure_exclusive_locks,
			tenant_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (execution_id) DO NOTHING
	`,
		event.ExecutionID,
		parsedTime,
		event.NaclEngineVersion,
		event.Environment,
		event.LineageEpochHash,
		parseUs,
		sortUs,
		ddlUs,
		totalUs,
		riskLevel,
		riskScore,
		mutatedTables,
		heavyRewrites,
		locksJSON,
		event.TenantID,
	)

	if err != nil {
		log.Printf("Error: Failed to write telemetry to PostgreSQL: %v", err)
	} else {
		log.Printf("Telemetry log saved. ExecID: %s, Tenant: %s, Environment: %s, Risk: %s (Score: %.2f)",
			event.ExecutionID, event.TenantID, event.Environment, riskLevel, riskScore)
	}
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
