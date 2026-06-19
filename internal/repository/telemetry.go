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
		`SELECT 
			execution_id as run,
			AVG(frontend_parse_us)::BIGINT as parse,
			AVG(topological_sort_us)::BIGINT as sort,
			AVG(ddl_generation_us)::BIGINT as ddl
		 FROM telemetry_logs
		 WHERE tenant_id = $1
		 GROUP BY execution_id
		 ORDER BY MAX(timestamp_utc) DESC
		 LIMIT 7`,
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
			TO_CHAR(timestamp_utc, 'Dy') as day,
			SUM(CASE WHEN risk_score <= 2.0 THEN 1 ELSE 0 END)::BIGINT as autonomous,
			SUM(CASE WHEN risk_score > 2.0 THEN 1 ELSE 0 END)::BIGINT as manual
		 FROM telemetry_logs
		 WHERE tenant_id = $1
		 GROUP BY day
		 LIMIT 7`,
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
	if err == nil && count == 0 {
		_ = r.seedTenant(ctx, tenantID)
	}
}

func (r *PostgresTelemetryRepository) seedTenant(ctx context.Context, tenantID string) error {
	// Trivial
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO telemetry_logs (
			execution_id, timestamp_utc, nacl_engine_version, environment, lineage_epoch_hash,
			frontend_parse_us, topological_sort_us, ddl_generation_us, total_compilation_us,
			risk_level, risk_score, mutated_tables_count, heavy_rewrites_detected, 
			infrastructure_exclusive_locks, tenant_id, compiled_ddl
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (execution_id) DO NOTHING;
	`, fmt.Sprintf("exec-%s-103", tenantID), time.Now().UTC(), "1.0.0", "staging", "genesis",
		110, 30, 40, 180,
		"TRIVIAL", 1.2, 1, false,
		[]byte(`["logs_archive"]`), tenantID,
		"CREATE TABLE \"logs_archive\" (\n    \"id\" SERIAL PRIMARY KEY,\n    \"log_message\" TEXT NOT NULL,\n    \"created_at\" TIMESTAMP DEFAULT NOW()\n);",
	)
	if err != nil {
		return err
	}

	// Moderate
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO telemetry_logs (
			execution_id, timestamp_utc, nacl_engine_version, environment, lineage_epoch_hash,
			frontend_parse_us, topological_sort_us, ddl_generation_us, total_compilation_us,
			risk_level, risk_score, mutated_tables_count, heavy_rewrites_detected, 
			infrastructure_exclusive_locks, tenant_id, compiled_ddl
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (execution_id) DO NOTHING;
	`, fmt.Sprintf("exec-%s-102", tenantID), time.Now().UTC(), "1.0.0", "production", "genesis",
		95, 20, 35, 150,
		"MODERATE", 3.8, 1, false,
		[]byte(`["orders"]`), tenantID,
		"CREATE INDEX CONCURRENTLY \"idx_orders_customer_id\" ON \"orders\" (\"customer_id\");",
	)
	if err != nil {
		return err
	}

	// Critical
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO telemetry_logs (
			execution_id, timestamp_utc, nacl_engine_version, environment, lineage_epoch_hash,
			frontend_parse_us, topological_sort_us, ddl_generation_us, total_compilation_us,
			risk_level, risk_score, mutated_tables_count, heavy_rewrites_detected, 
			infrastructure_exclusive_locks, tenant_id, compiled_ddl
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (execution_id) DO NOTHING;
	`, fmt.Sprintf("exec-%s-101", tenantID), time.Now().UTC(), "1.0.0", "production", "genesis",
		150, 42, 60, 252,
		"CRITICAL", 8.5, 2, true,
		[]byte(`["users", "user_sessions"]`), tenantID,
		"ALTER TABLE \"users\" DROP COLUMN \"deprecated_age_field\";\nDROP TABLE \"user_sessions\";",
	)
	return err
}

