package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/nacl-org/nacl-cloud-go/internal/repository"
	pb "github.com/nacl-org/nacl-cloud-go/pkg/pb/engine/v1"
	"google.golang.org/grpc"
)

type TelemetryService struct {
	repo       repository.TelemetryRepository
	grpcConn   *grpc.ClientConn
	grpcClient pb.EngineServiceClient
	db         *sql.DB
}

func NewTelemetryService(repo repository.TelemetryRepository, conn *grpc.ClientConn, db *sql.DB) *TelemetryService {
	return &TelemetryService{
		repo:       repo,
		grpcConn:   conn,
		grpcClient: pb.NewEngineServiceClient(conn),
		db:         db,
	}
}

func (s *TelemetryService) GetPerformanceMetrics(ctx context.Context, tenantID string) ([]repository.PerformancePayload, error) {
	return s.repo.GetPerformanceMetrics(ctx, tenantID)
}

func (s *TelemetryService) GetRiskDistribution(ctx context.Context, tenantID string) ([]repository.RiskPayload, error) {
	return s.repo.GetRiskDistribution(ctx, tenantID)
}

func (s *TelemetryService) GetVelocityMetrics(ctx context.Context, tenantID string) ([]repository.VelocityPayload, error) {
	return s.repo.GetVelocityMetrics(ctx, tenantID)
}

func (s *TelemetryService) GetExecutions(ctx context.Context, tenantID string, limit, offset int64) ([]repository.ExecutionRow, int64, error) {
	return s.repo.GetExecutions(ctx, tenantID, limit, offset)
}

func (s *TelemetryService) GetRecommendations(ctx context.Context, tenantID string) ([]repository.RecommendationRow, error) {
	return s.repo.GetRecommendations(ctx, tenantID)
}

func (s *TelemetryService) GetPRs(ctx context.Context, tenantID string) ([]repository.PrRow, error) {
	return s.repo.GetPRs(ctx, tenantID)
}

func (s *TelemetryService) GetChunkedMigrations(ctx context.Context, tenantID string) ([]repository.ChunkedMigrationRow, error) {
	return s.repo.GetChunkedMigrations(ctx, tenantID)
}

func (s *TelemetryService) ApproveRecommendation(ctx context.Context, id string, tenantID string) error {
	proposedDDL, status, err := s.repo.GetRecommendation(ctx, id, tenantID)
	if err != nil {
		return err
	}

	if status != "pending_approval" {
		return fmt.Errorf("cannot approve recommendation in status '%s'", status)
	}

	// Make the gRPC safety check call before applying the DDL!
	if proposedDDL != "" {
		// VerifySafety gRPC check
		safetyRes, err := s.grpcClient.VerifySafety(ctx, &pb.VerifySafetyRequest{
			RawDdl: proposedDDL,
		})
		if err == nil {
			if safetyRes.RiskLevel == "CRITICAL" {
				return fmt.Errorf("refusing to apply recommendation: critical risk detected (%s, score %.2f)", safetyRes.RiskLevel, safetyRes.RiskScore)
			}
		}

		// Execute the DDL statement on PostgreSQL
		if _, err := s.db.ExecContext(ctx, proposedDDL); err != nil {
			errStr := err.Error()
			isUndefinedTable := strings.Contains(errStr, "42P01") || strings.Contains(errStr, "does not exist")
			if !isUndefinedTable {
				return fmt.Errorf("failed to execute index DDL: %w", err)
			}
		}
	}

	return s.repo.ApproveRecommendation(ctx, id)
}

func (s *TelemetryService) RejectRecommendation(ctx context.Context, id string, tenantID string) error {
	return s.repo.RejectRecommendation(ctx, id, tenantID)
}

func (s *TelemetryService) GetRecommendation(ctx context.Context, id string, tenantID string) (string, string, error) {
	return s.repo.GetRecommendation(ctx, id, tenantID)
}

func (s *TelemetryService) LogManualDispatch(ctx context.Context, id, environment, tenantID, sqlStr string) error {
	return s.repo.LogManualDispatch(ctx, id, environment, tenantID, sqlStr)
}

// CompileSchema calls gRPC CompileSchema endpoint on the Rust compiler engine
func (s *TelemetryService) CompileSchema(ctx context.Context, schemaContent, env, tenantID string) (*pb.CompileSchemaResponse, error) {
	return s.grpcClient.CompileSchema(ctx, &pb.CompileSchemaRequest{
		SchemaContent: schemaContent,
		Environment:   env,
		TenantId:      tenantID,
	})
}

// LintSchema calls gRPC LintSchema endpoint
func (s *TelemetryService) LintSchema(ctx context.Context, schemaContent string) (*pb.LintSchemaResponse, error) {
	return s.grpcClient.LintSchema(ctx, &pb.LintSchemaRequest{
		SchemaContent: schemaContent,
	})
}

// VerifySafety calls gRPC VerifySafety endpoint
func (s *TelemetryService) VerifySafety(ctx context.Context, rawDDL string) (*pb.VerifySafetyResponse, error) {
	return s.grpcClient.VerifySafety(ctx, &pb.VerifySafetyRequest{
		RawDdl: rawDDL,
	})
}

func (s *TelemetryService) ExecuteDDL(ctx context.Context, sqlStr string) error {
	_, err := s.db.ExecContext(ctx, sqlStr)
	return err
}

// Helper to get string representation of DB errors
type dbErrString interface {
	Error() string
}

func (s *TelemetryService) checkError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Helper extension method equivalent for err.Error()
func (s *TelemetryService) toString(err error) string {
	return s.checkError(err)
}

// Helper for error string fallback
func to_string_fallback(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Extension type for error to get string
type ExtendedError struct {
	error
}

func (e ExtendedError) to_string_fallback() string {
	return e.Error()
}

func (s *TelemetryService) GetFeatureFlags(ctx context.Context) (map[string]bool, error) {
	return s.repo.GetFeatureFlags(ctx)
}

func (s *TelemetryService) GetLatestLineageHash(ctx context.Context, tenantID string) (string, error) {
	return s.repo.GetLatestLineageHash(ctx, tenantID)
}

func (s *TelemetryService) LogExecution(
	ctx context.Context,
	row *repository.ExecutionRow,
	tenantID string,
	engineVersion string,
	lineageHash string,
	parseUs, sortUs, ddlUs, totalUs int64,
) error {
	return s.repo.LogExecution(ctx, row, tenantID, engineVersion, lineageHash, parseUs, sortUs, ddlUs, totalUs)
}
