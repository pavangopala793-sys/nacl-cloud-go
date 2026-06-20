package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type PerformancePayload struct {
	Run   string `json:"run"`
	Parse int64  `json:"parse"`
	Sort  int64  `json:"sort"`
	DDL   int64  `json:"ddl"`
}

type RiskPayload struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
	Fill  string `json:"fill"`
}

type VelocityPayload struct {
	Date       string `json:"date"`
	Autonomous int64  `json:"autonomous"`
	Manual     int64  `json:"manual"`
}

type ExecutionRow struct {
	ExecutionID                  string          `json:"execution_id"`
	Timestamp                    string          `json:"timestamp"`
	Environment                  string          `json:"environment"`
	RiskLevel                    string          `json:"risk_level"`
	RiskScore                    float64         `json:"risk_score"`
	MutatedTables                int32           `json:"mutated_tables"`
	DurationUs                   int64           `json:"duration_us"`
	HeavyRewritesDetected        bool            `json:"heavy_rewrites_detected"`
	InfrastructureExclusiveLocks json.RawMessage `json:"infrastructure_exclusive_locks"`
	CompiledDDL                  string          `json:"compiled_ddl"`
}

type RecommendationRow struct {
	RecommendationID  string          `json:"recommendation_id"`
	PrID              *string         `json:"pr_id"`
	ExecutionID       *string         `json:"execution_id"`
	TableName         string          `json:"table_name"`
	SlowQuery         string          `json:"slow_query"`
	ProposedDDL       string          `json:"proposed_ddl"`
	ReclaimedSsdBytes int64           `json:"reclaimed_ssd_bytes"`
	ConfidenceScore   float64         `json:"confidence_score"`
	Metadata          json.RawMessage `json:"metadata"`
	Status            string          `json:"status"`
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
}

type PrRow struct {
	PrID                 string  `json:"pr_id"`
	Title                string  `json:"title"`
	GithubURL            *string `json:"github_url"`
	LocalPatchPath       *string `json:"local_patch_path"`
	ProposedChangesCount int32   `json:"proposed_changes_count"`
	IsMassiveStorageWarn bool    `json:"is_massive_storage_warn"`
	Status               string  `json:"status"`
	CreatedAt            string  `json:"created_at"`
	MergedAt             *string `json:"merged_at"`
	ClosedAt             *string `json:"closed_at"`
}

type ChunkedMigrationRow struct {
	JobID              string  `json:"job_id"`
	TableName          string  `json:"table_name"`
	SetClause          string  `json:"set_clause"`
	LastID             int64   `json:"last_id"`
	ChunkSize          int32   `json:"chunk_size"`
	RowsUpdated        int64   `json:"rows_updated"`
	TotalRowsEstimated *int64  `json:"total_rows_estimated"`
	Status             string  `json:"status"`
	StartedAt          string  `json:"started_at"`
	CompletedAt        *string `json:"completed_at"`
	LastHeartbeatAt    string  `json:"last_heartbeat_at"`
}

type TelemetryRepository interface {
	GetPerformanceMetrics(ctx context.Context, tenantID string) ([]PerformancePayload, error)
	GetRiskDistribution(ctx context.Context, tenantID string) ([]RiskPayload, error)
	GetVelocityMetrics(ctx context.Context, tenantID string) ([]VelocityPayload, error)
	GetExecutions(ctx context.Context, tenantID string, limit, offset int64) ([]ExecutionRow, int64, error)
	GetRecommendations(ctx context.Context, tenantID string) ([]RecommendationRow, error)
	GetPRs(ctx context.Context, tenantID string) ([]PrRow, error)
	GetChunkedMigrations(ctx context.Context, tenantID string) ([]ChunkedMigrationRow, error)
	GetRecommendation(ctx context.Context, id string, tenantID string) (proposedDDL string, status string, err error)
	ApproveRecommendation(ctx context.Context, id string) error
	RejectRecommendation(ctx context.Context, id string, tenantID string) error
	LogManualDispatch(ctx context.Context, id, environment, tenantID, sql string) error
	GetFeatureFlags(ctx context.Context) (map[string]bool, error)
	GetLatestLineageHash(ctx context.Context, tenantID string) (string, error)
	LogExecution(ctx context.Context, row *ExecutionRow, tenantID string, engineVersion string, lineageHash string, parseUs, sortUs, ddlUs, totalUs int64) error
}

type PostgresTelemetryRepository struct {
	db *sql.DB
}

func NewTelemetryRepository(db *sql.DB) TelemetryRepository {
	return &PostgresTelemetryRepository{db: db}
}

func (r *PostgresTelemetryRepository) GetPerformanceMetrics(ctx context.Context, tenantID string) ([]PerformancePayload, error) {
	r.ensureSeeded(ctx, tenantID)
	rows, err := r.db.QueryContext(ctx,
		`SELECT run, parse, sort, ddl FROM (
			SELECT 
				execution_id as run,
				AVG(frontend_parse_us)::BIGINT as parse,
				AVG(topological_sort_us)::BIGINT as sort,
				AVG(ddl_generation_us)::BIGINT as ddl,
				MAX(timestamp_utc) as max_ts
			 FROM telemetry_logs
			 WHERE tenant_id = $1
			 GROUP BY execution_id
			 ORDER BY max_ts DESC
			 LIMIT 1000
		) sub
		ORDER BY max_ts ASC`,
		tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []PerformancePayload
	for rows.Next() {
		var p PerformancePayload
		if err := rows.Scan(&p.Run, &p.Parse, &p.Sort, &p.DDL); err == nil {
			result = append(result, p)
		}
	}
	return result, nil
}

func (r *PostgresTelemetryRepository) GetRiskDistribution(ctx context.Context, tenantID string) ([]RiskPayload, error) {
	r.ensureSeeded(ctx, tenantID)
	rows, err := r.db.QueryContext(ctx,
		`SELECT risk_level, COUNT(*)::BIGINT as count 
		 FROM telemetry_logs 
		 WHERE tenant_id = $1 
		 GROUP BY risk_level`,
		tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []RiskPayload
	for rows.Next() {
		var name string
		var count int64
		if err := rows.Scan(&name, &count); err == nil {
			fillColor := "#9ca3af"
			switch name {
			case "Trivial", "TRIVIAL", "trivial":
				fillColor = "#10b981"
			case "Moderate", "MODERATE", "moderate":
				fillColor = "#3b82f6"
			case "High", "HIGH", "high":
				fillColor = "#f59e0b"
			case "Critical", "CRITICAL", "critical":
				fillColor = "#ef4444"
			}
			result = append(result, RiskPayload{
				Name:  name,
				Value: count,
				Fill:  fillColor,
			})
		}
	}
	return result, nil
}

func (r *PostgresTelemetryRepository) GetVelocityMetrics(ctx context.Context, tenantID string) ([]VelocityPayload, error) {
	r.ensureSeeded(ctx, tenantID)
	rows, err := r.db.QueryContext(ctx,
		`SELECT 
			TO_CHAR(timestamp_utc, 'HH24:MI') as date,
			SUM(CASE WHEN risk_score <= 2.0 THEN 1 ELSE 0 END)::BIGINT as autonomous,
			SUM(CASE WHEN risk_score > 2.0 THEN 1 ELSE 0 END)::BIGINT as manual
		 FROM (
		 	SELECT timestamp_utc, risk_score
		 	FROM telemetry_logs
		 	WHERE tenant_id = $1
		 	ORDER BY timestamp_utc DESC
		 	LIMIT 300
		 ) sub
		 GROUP BY date, timestamp_utc
		 ORDER BY timestamp_utc DESC`,
		tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []VelocityPayload
	for rows.Next() {
		var v VelocityPayload
		if err := rows.Scan(&v.Date, &v.Autonomous, &v.Manual); err == nil {
			result = append(result, v)
		}
	}

	// Reverse to match chronological order
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return result, nil
}

func (r *PostgresTelemetryRepository) GetExecutions(ctx context.Context, tenantID string, limit, offset int64) ([]ExecutionRow, int64, error) {
	r.ensureSeeded(ctx, tenantID)
	var totalCount int64
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM telemetry_logs WHERE tenant_id = $1", tenantID).Scan(&totalCount)
	if err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT 
			t.execution_id,
			t.timestamp_utc::text as timestamp,
			t.environment,
			t.total_compilation_us,
			t.risk_level,
			t.risk_score,
			t.mutated_tables_count,
			t.heavy_rewrites_detected,
			t.infrastructure_exclusive_locks,
			COALESCE(t.compiled_ddl, '')
		 FROM telemetry_logs t
		 WHERE t.tenant_id = $1
		 ORDER BY t.timestamp_utc DESC
		 LIMIT $2 OFFSET $3`,
		tenantID, limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var data []ExecutionRow
	for rows.Next() {
		var ex ExecutionRow
		var locks []byte
		err := rows.Scan(
			&ex.ExecutionID, &ex.Timestamp, &ex.Environment, &ex.DurationUs,
			&ex.RiskLevel, &ex.RiskScore, &ex.MutatedTables, &ex.HeavyRewritesDetected,
			&locks, &ex.CompiledDDL,
		)
		if err == nil {
			ex.InfrastructureExclusiveLocks = json.RawMessage(locks)
			data = append(data, ex)
		}
	}

	return data, totalCount, nil
}

func (r *PostgresTelemetryRepository) GetRecommendations(ctx context.Context, tenantID string) ([]RecommendationRow, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT 
			ir.recommendation_id::text as recommendation_id,
			apr.pr_id as pr_id,
			ir.execution_id,
			ir.table_name,
			ir.slow_query,
			ir.proposed_ddl,
			ir.reclaimed_ssd_bytes,
			ir.confidence_score::FLOAT as confidence_score,
			ir.metadata,
			ir.status,
			ir.created_at::text as created_at,
			ir.updated_at::text as updated_at
		 FROM index_recommendations ir
		 LEFT JOIN autonomous_pr_recommendations apr ON ir.recommendation_id = apr.recommendation_id
		 WHERE ir.tenant_id = $1
		 ORDER BY ir.created_at DESC`,
		tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []RecommendationRow
	for rows.Next() {
		var rec RecommendationRow
		var meta []byte
		err := rows.Scan(
			&rec.RecommendationID, &rec.PrID, &rec.ExecutionID, &rec.TableName,
			&rec.SlowQuery, &rec.ProposedDDL, &rec.ReclaimedSsdBytes, &rec.ConfidenceScore,
			&meta, &rec.Status, &rec.CreatedAt, &rec.UpdatedAt,
		)
		if err == nil {
			rec.Metadata = json.RawMessage(meta)
			result = append(result, rec)
		}
	}
	return result, nil
}

func (r *PostgresTelemetryRepository) GetPRs(ctx context.Context, tenantID string) ([]PrRow, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT 
			pr_id,
			title,
			github_url,
			local_patch_path,
			proposed_changes_count,
			is_massive_storage_warn,
			status,
			created_at::text as created_at,
			merged_at::text as merged_at,
			closed_at::text as closed_at
		 FROM autonomous_prs
		 WHERE tenant_id = $1
		 ORDER BY created_at DESC`,
		tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []PrRow
	for rows.Next() {
		var p PrRow
		err := rows.Scan(
			&p.PrID, &p.Title, &p.GithubURL, &p.LocalPatchPath,
			&p.ProposedChangesCount, &p.IsMassiveStorageWarn, &p.Status,
			&p.CreatedAt, &p.MergedAt, &p.ClosedAt,
		)
		if err == nil {
			result = append(result, p)
		}
	}
	return result, nil
}

func (r *PostgresTelemetryRepository) GetChunkedMigrations(ctx context.Context, tenantID string) ([]ChunkedMigrationRow, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT 
			job_id,
			table_name,
			set_clause,
			last_id,
			chunk_size,
			rows_updated,
			total_rows_estimated,
			status,
			started_at::text as started_at,
			completed_at::text as completed_at,
			last_heartbeat_at::text as last_heartbeat_at
		 FROM chunked_migrations_status
		 WHERE tenant_id = $1
		 ORDER BY started_at DESC`,
		tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ChunkedMigrationRow
	for rows.Next() {
		var c ChunkedMigrationRow
		err := rows.Scan(
			&c.JobID, &c.TableName, &c.SetClause, &c.LastID,
			&c.ChunkSize, &c.RowsUpdated, &c.TotalRowsEstimated, &c.Status,
			&c.StartedAt, &c.CompletedAt, &c.LastHeartbeatAt,
		)
		if err == nil {
			result = append(result, c)
		}
	}
	return result, nil
}

func (r *PostgresTelemetryRepository) GetRecommendation(ctx context.Context, id string, tenantID string) (string, string, error) {
	var proposedDDL string
	var status string
	err := r.db.QueryRowContext(ctx,
		"SELECT proposed_ddl, status FROM index_recommendations WHERE recommendation_id = $1 AND tenant_id = $2",
		id, tenantID,
	).Scan(&proposedDDL, &status)
	return proposedDDL, status, err
}

func (r *PostgresTelemetryRepository) ApproveRecommendation(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		"UPDATE index_recommendations SET status = 'applied', updated_at = NOW() WHERE recommendation_id = $1",
		id,
	)
	return err
}

func (r *PostgresTelemetryRepository) RejectRecommendation(ctx context.Context, id string, tenantID string) error {
	_, err := r.db.ExecContext(ctx,
		"UPDATE index_recommendations SET status = 'rejected', updated_at = NOW() WHERE recommendation_id = $1 AND tenant_id = $2 AND status = 'pending_approval'",
		id, tenantID,
	)
	return err
}

func (r *PostgresTelemetryRepository) LogManualDispatch(ctx context.Context, id, environment, tenantID, sqlStr string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO telemetry_logs (
			execution_id, timestamp_utc, nacl_engine_version, environment,
			lineage_epoch_hash, frontend_parse_us, topological_sort_us,
			ddl_generation_us, total_compilation_us, risk_level, risk_score,
			mutated_tables_count, heavy_rewrites_detected, infrastructure_exclusive_locks,
			tenant_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (execution_id) DO NOTHING`,
		id, time.Now().UTC(), "nacl-api-manual-dispatch", environment,
		"", 0, 0, 0, 0, "manual_dispatched", 0.0, 0, false, json.RawMessage("[]"), tenantID,
	)
	return err
}

func (r *PostgresTelemetryRepository) GetFeatureFlags(ctx context.Context) (map[string]bool, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT flag_key, is_enabled FROM feature_flags")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	flags := make(map[string]bool)
	for rows.Next() {
		var key string
		var enabled bool
		if err := rows.Scan(&key, &enabled); err == nil {
			flags[key] = enabled
		}
	}
	return flags, nil
}

func (r *PostgresTelemetryRepository) GetLatestLineageHash(ctx context.Context, tenantID string) (string, error) {
	var hash string
	err := r.db.QueryRowContext(ctx,
		"SELECT lineage_epoch_hash FROM telemetry_logs WHERE tenant_id = $1 AND lineage_epoch_hash <> '' ORDER BY timestamp_utc DESC LIMIT 1",
		tenantID,
	).Scan(&hash)
	if err == sql.ErrNoRows {
		return "genesis", nil
	}
	return hash, err
}

func (r *PostgresTelemetryRepository) LogExecution(
	ctx context.Context,
	row *ExecutionRow,
	tenantID string,
	engineVersion string,
	lineageHash string,
	parseUs, sortUs, ddlUs, totalUs int64,
) error {
	locksJSON := row.InfrastructureExclusiveLocks
	if len(locksJSON) == 0 {
		locksJSON = json.RawMessage("[]")
	}

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO telemetry_logs (
			execution_id, timestamp_utc, nacl_engine_version, environment,
			lineage_epoch_hash, frontend_parse_us, topological_sort_us,
			ddl_generation_us, total_compilation_us, risk_level, risk_score,
			mutated_tables_count, heavy_rewrites_detected, infrastructure_exclusive_locks,
			tenant_id, compiled_ddl
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (execution_id) DO NOTHING`,
		row.ExecutionID, time.Now().UTC(), engineVersion, row.Environment,
		lineageHash, parseUs, sortUs, ddlUs, totalUs, row.RiskLevel, row.RiskScore,
		row.MutatedTables, row.HeavyRewritesDetected, locksJSON, tenantID, row.CompiledDDL,
	)
	return err
}

func (r *PostgresTelemetryRepository) ensureSeeded(ctx context.Context, tenantID string) {
	var count int64
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM telemetry_logs WHERE tenant_id = $1", tenantID).Scan(&count)
	if err == nil && count < 1000 {
		_, _ = r.db.ExecContext(ctx, "DELETE FROM telemetry_logs WHERE tenant_id = $1", tenantID)
		_ = r.seedTenant(ctx, tenantID)
	}
}

func (r *PostgresTelemetryRepository) seedTenant(ctx context.Context, tenantID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	type mockMigration struct {
		ddl           string
		riskLevel     string
		riskScore     float64
		mutatedTables int
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

	now := time.Now().UTC()
	for i := 1; i <= 1000; i++ {
		execID := fmt.Sprintf("exec_sim_%s_%d", tenantID, i)
		ts := now.Add(time.Duration(-i) * time.Minute)
		
		parseUs := int64(1200 + (i%7)*150 + (i%5)*80)
		sortUs := int64(300 + (i%9)*40 + (i%3)*20)
		ddlUs := int64(1800 + (i%11)*200 + (i%4)*100)
		totalUs := parseUs + sortUs + ddlUs

		mig := mockMigrations[i%len(mockMigrations)]
		env := envs[i%len(envs)]

		_, err = tx.ExecContext(ctx, `
			INSERT INTO telemetry_logs (
				execution_id, timestamp_utc, nacl_engine_version, environment, lineage_epoch_hash,
				frontend_parse_us, topological_sort_us, ddl_generation_us, total_compilation_us,
				risk_level, risk_score, mutated_tables_count, heavy_rewrites_detected, 
				infrastructure_exclusive_locks, tenant_id, compiled_ddl
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			ON CONFLICT (execution_id) DO NOTHING;
		`, execID, ts, "1.0.0", env, "genesis",
			parseUs, sortUs, ddlUs, totalUs,
			mig.riskLevel, mig.riskScore, mig.mutatedTables, mig.heavyRewrite,
			[]byte(mig.locks), tenantID, mig.ddl,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

