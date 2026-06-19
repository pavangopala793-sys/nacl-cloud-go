package handler

import (
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/nacl-org/nacl-cloud-go/internal/auth"
	"github.com/nacl-org/nacl-cloud-go/internal/config"
	"github.com/nacl-org/nacl-cloud-go/internal/repository"
	"github.com/nacl-org/nacl-cloud-go/internal/service"
	"github.com/redis/go-redis/v9"
)

type TelemetryHandler struct {
	svc *service.TelemetryService
	cfg *config.Config
}

func NewTelemetryHandler(svc *service.TelemetryService, cfg *config.Config) *TelemetryHandler {
	return &TelemetryHandler{svc: svc, cfg: cfg}
}

func getRedisClient() (*redis.Client, error) {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://127.0.0.1:6379/"
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	return redis.NewClient(opt), nil
}

func (h *TelemetryHandler) GetPerformance(c *fiber.Ctx) error {
	tenantID, _ := c.Locals("tenant_id").(string)
	if tenantID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: No tenant ID context"})
	}

	data, err := h.svc.GetPerformanceMetrics(c.UserContext(), tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if data == nil {
		data = []repository.PerformancePayload{}
	}

	return c.JSON(data)
}

func (h *TelemetryHandler) GetRisk(c *fiber.Ctx) error {
	tenantID, _ := c.Locals("tenant_id").(string)
	if tenantID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: No tenant ID context"})
	}

	data, err := h.svc.GetRiskDistribution(c.UserContext(), tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if data == nil {
		data = []repository.RiskPayload{}
	}

	return c.JSON(data)
}

func (h *TelemetryHandler) GetVelocity(c *fiber.Ctx) error {
	tenantID, _ := c.Locals("tenant_id").(string)
	if tenantID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: No tenant ID context"})
	}

	data, err := h.svc.GetVelocityMetrics(c.UserContext(), tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if data == nil {
		data = []repository.VelocityPayload{}
	}

	return c.JSON(data)
}

func (h *TelemetryHandler) GetExecutions(c *fiber.Ctx) error {
	tenantID, _ := c.Locals("tenant_id").(string)
	if tenantID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: No tenant ID context"})
	}

	pageStr := c.Query("page", "1")
	limitStr := c.Query("limit", "20")

	page, _ := strconv.ParseInt(pageStr, 10, 64)
	if page < 1 {
		page = 1
	}

	limit, _ := strconv.ParseInt(limitStr, 10, 64)
	if limit < 1 || limit > 100 {
		limit = 20
	}

	offset := (page - 1) * limit

	data, totalCount, err := h.svc.GetExecutions(c.UserContext(), tenantID, limit, offset)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	totalPages := (totalCount + limit - 1) / limit
	if totalCount == 0 {
		totalPages = 0
	}

	if data == nil {
		data = []repository.ExecutionRow{}
	}

	return c.JSON(fiber.Map{
		"data":        data,
		"total_count": totalCount,
		"page":        page,
		"total_pages": totalPages,
	})
}

func (h *TelemetryHandler) GetRecommendations(c *fiber.Ctx) error {
	tenantID, _ := c.Locals("tenant_id").(string)
	if tenantID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: No tenant ID context"})
	}

	data, err := h.svc.GetRecommendations(c.UserContext(), tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if data == nil {
		data = []repository.RecommendationRow{}
	}

	return c.JSON(data)
}

func (h *TelemetryHandler) ApproveRecommendation(c *fiber.Ctx) error {
	tenantID, _ := c.Locals("tenant_id").(string)
	role, _ := c.Locals("role").(string)
	if tenantID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: No tenant ID context"})
	}

	if !auth.IsAuthorized(auth.Role(role), auth.ActionApprovePlan) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: Only SREs or workspace Admins can approve recommendations"})
	}

	id := c.Params("id")
	err := h.svc.ApproveRecommendation(c.UserContext(), id, tenantID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(fiber.Map{
		"status":  "success",
		"message": fmt.Sprintf("Successfully approved and applied recommendation '%s'", id),
	})
}

func (h *TelemetryHandler) RejectRecommendation(c *fiber.Ctx) error {
	tenantID, _ := c.Locals("tenant_id").(string)
	role, _ := c.Locals("role").(string)
	if tenantID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: No tenant ID context"})
	}

	if !auth.IsAuthorized(auth.Role(role), auth.ActionApprovePlan) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: Only SREs or workspace Admins can reject recommendations"})
	}

	id := c.Params("id")
	err := h.svc.RejectRecommendation(c.UserContext(), id, tenantID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(fiber.Map{
		"status":  "success",
		"message": fmt.Sprintf("Successfully rejected recommendation '%s'", id),
	})
}

func (h *TelemetryHandler) GetPRs(c *fiber.Ctx) error {
	tenantID, _ := c.Locals("tenant_id").(string)
	if tenantID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: No tenant ID context"})
	}

	data, err := h.svc.GetPRs(c.UserContext(), tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if data == nil {
		data = []repository.PrRow{}
	}

	return c.JSON(data)
}

func (h *TelemetryHandler) GetChunkedMigrations(c *fiber.Ctx) error {
	tenantID, _ := c.Locals("tenant_id").(string)
	if tenantID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: No tenant ID context"})
	}

	data, err := h.svc.GetChunkedMigrations(c.UserContext(), tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if data == nil {
		data = []repository.ChunkedMigrationRow{}
	}

	return c.JSON(data)
}

func (h *TelemetryHandler) GetSchedulerHoldQueue(c *fiber.Ctx) error {
	tenantID, _ := c.Locals("tenant_id").(string)
	if tenantID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: No tenant ID context"})
	}

	rdb, err := getRedisClient()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": fmt.Sprintf("Redis open failed: %v", err)})
	}
	defer rdb.Close()

	ctx := c.UserContext()
	rawPayloads, err := rdb.LRange(ctx, "nacl:hold_queue", 0, -1).Result()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Redis LRANGE failed: %v", err)})
	}

	parsedPayloads := []interface{}{}
	for _, raw := range rawPayloads {
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			itemTenant, _ := parsed["tenant_id"].(string)
			if itemTenant == "" {
				itemTenant = "admin"
			}
			if itemTenant == tenantID {
				parsedPayloads = append(parsedPayloads, parsed)
			}
		}
	}

	return c.JSON(parsedPayloads)
}

func (h *TelemetryHandler) DispatchHoldQueue(c *fiber.Ctx) error {
	tenantID, _ := c.Locals("tenant_id").(string)
	role, _ := c.Locals("role").(string)
	if tenantID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: No tenant ID context"})
	}

	if !auth.IsAuthorized(auth.Role(role), auth.ActionDispatchQueue) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: Only SREs or workspace Admins can dispatch executions"})
	}

	id := c.Params("id")

	rdb, err := getRedisClient()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": fmt.Sprintf("Redis open failed: %v", err)})
	}
	defer rdb.Close()

	ctx := c.UserContext()
	rawPayloads, err := rdb.LRange(ctx, "nacl:hold_queue", 0, -1).Result()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Redis LREM failed: %v", err)})
	}

	var foundPayload string
	var sqlToRun string
	var environment string

	for _, raw := range rawPayloads {
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			execID, _ := parsed["execution_id"].(string)
			if execID == id {
				itemTenant, _ := parsed["tenant_id"].(string)
				if itemTenant == "" {
					itemTenant = "admin"
				}
				if itemTenant != tenantID {
					return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Access forbidden for this execution"})
				}
				foundPayload = raw
				sqlToRun, _ = parsed["sql"].(string)
				environment, _ = parsed["environment"].(string)
				break
			}
		}
	}

	if foundPayload == "" {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": fmt.Sprintf("Execution ID '%s' not found in hold queue", id)})
	}

	if strings.TrimSpace(sqlToRun) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Payload has empty or missing SQL statement"})
	}

	// Remove from Redis (LREM)
	_, err = rdb.LRem(ctx, "nacl:hold_queue", 1, foundPayload).Result()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Redis LREM failed: %v", err)})
	}

	// Run DDL statement directly using telemetry service DB connection
	err = h.svc.ExecuteDDL(ctx, sqlToRun)
	if err != nil {
		// Re-push on failure
		_ = rdb.LPush(ctx, "nacl:hold_queue", foundPayload).Val()
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("DDL execution failed: %v", err)})
	}

	// Log manual dispatch
	err = h.svc.LogManualDispatch(ctx, id, environment, tenantID, sqlToRun)
	if err != nil {
		_ = rdb.LPush(ctx, "nacl:hold_queue", foundPayload).Val()
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to write telemetry log: %v", err)})
	}

	return c.JSON(fiber.Map{
		"status":       "success",
		"message":      fmt.Sprintf("Successfully dispatched execution '%s'", id),
		"execution_id": id,
	})
}

func (h *TelemetryHandler) GetFeatureFlags(c *fiber.Ctx) error {
	flags, err := h.svc.GetFeatureFlags(c.UserContext())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(flags)
}

func (h *TelemetryHandler) GetConnectionPrerequisites(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"subnet":       h.cfg.InboundSubnet,
		"port":         h.cfg.InboundPort,
		"sql_template": h.cfg.SQLTemplate,
	})
}

func (h *TelemetryHandler) CreatePlan(c *fiber.Ctx) error {
	tenantID, _ := c.Locals("tenant_id").(string)
	if tenantID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: No tenant ID context"})
	}

	execID := c.Get("x-execution-id")
	environment := c.Get("x-environment", "remote")

	type PlanRequest struct {
		SchemaInfra string `json:"schema_infra"`
	}

	var req PlanRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid JSON body"})
	}

	// Compile schema via gRPC call to Rust engine
	compRes, err := h.svc.CompileSchema(c.UserContext(), req.SchemaInfra, environment, tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Compilation failure: %v", err)})
	}

	// Verify safety via gRPC
	safetyRes, err := h.svc.VerifySafety(c.UserContext(), compRes.CompiledDdl)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Safety verification failure: %v", err)})
	}

	// Fetch the latest lineage epoch hash
	lineageHash, err := h.svc.GetLatestLineageHash(c.UserContext(), tenantID)
	if err != nil {
		lineageHash = "genesis"
	}

	if execID == "" {
		execID = compRes.ExecutionId
	}

	// Build the response format expected by the CLI
	return c.JSON(fiber.Map{
		"lineage_epoch_hash": lineageHash,
		"blast_radius": fiber.Map{
			"risk_score":              safetyRes.RiskScore,
			"risk_level":              safetyRes.RiskLevel,
			"heavy_rewrites_detected": safetyRes.HeavyRewritesDetected,
			"exclusive_locks":        safetyRes.ExclusiveLocks,
		},
	})
}

func (h *TelemetryHandler) ApplyPlan(c *fiber.Ctx) error {
	tenantID, _ := c.Locals("tenant_id").(string)
	dbURL, _ := c.Locals("db_url").(string)

	if tenantID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: No tenant ID context"})
	}

	execID := c.Get("x-execution-id")
	environment := c.Get("x-environment", "remote")
	expiresAtStr := c.Get("x-expires-at")

	type ApplyRequest struct {
		SchemaInfra string     `json:"schema_infra"`
		Signatures  [][]string `json:"signatures"`
	}

	var req ApplyRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid JSON body"})
	}

	// Fetch the latest lineage epoch hash
	lineageHash, err := h.svc.GetLatestLineageHash(c.UserContext(), tenantID)
	if err != nil {
		lineageHash = "genesis"
	}

	// Fetch workspace settings from DB to check if SRE/Admin approval is required
	settings, err := h.svc.GetWorkspaceSettings(c.UserContext(), tenantID)
	sreApprovalRequired := true
	if err == nil && settings != nil {
		sreApprovalRequired = settings.SreApprovalRequired
	}

	if sreApprovalRequired {
		// Verify cryptographic signature (Ed25519 signature verification)
		if len(req.Signatures) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "No signatures provided for validation"})
		}

		for _, sigPair := range req.Signatures {
			if len(sigPair) != 2 {
				continue
			}
			pubKeyHex := sigPair[0]
			sigHex := sigPair[1]

			pubKeyBytes, err := hex.DecodeString(pubKeyHex)
			if err != nil || len(pubKeyBytes) != 32 {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid public key hex length"})
			}

			sigBytes, err := hex.DecodeString(sigHex)
			if err != nil || len(sigBytes) != 64 {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid signature hex length"})
			}

			// Calculate SHA-256 of the schema_infra content
			hashInstance := sha256.New()
			hashInstance.Write([]byte(req.SchemaInfra))
			schemaHash := hex.EncodeToString(hashInstance.Sum(nil))

			// Canonical message: schema_hash|environment|exec_id|lineage_epoch_hash|expires_at
			canonicalPayload := fmt.Sprintf("%s|%s|%s|%s|%s", schemaHash, environment, execID, lineageHash, expiresAtStr)

			if !ed25519.Verify(pubKeyBytes, []byte(canonicalPayload), sigBytes) {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Cryptographic signature verification failed"})
			}
		}
	}

	// Compile schema to get the final DDL
	compRes, err := h.svc.CompileSchema(c.UserContext(), req.SchemaInfra, environment, tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Compilation failure: %v", err)})
	}

	// Verify safety before executing
	safetyRes, err := h.svc.VerifySafety(c.UserContext(), compRes.CompiledDdl)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Safety verification failure: %v", err)})
	}

	if safetyRes.RiskLevel == "CRITICAL" {
		fmt.Printf("WARNING: CRITICAL RISK DETECTED (Risk Score: %.2f) but executing since plan is signed by trusted SRE identity.\n", safetyRes.RiskScore)
	}

	// Execute migrations against target customer database URL context
	if dbURL == "" {
		// Fallback to active sandbox DB if empty (for local CLI push target cloud in localhost dev)
		dbURL = os.Getenv("DATABASE_URL")
		if dbURL == "" {
			dbURL = "postgres://postgres:postgres@localhost:5432/postgres"
		}
	}

	if !strings.Contains(dbURL, "sslmode=") {
		if strings.Contains(dbURL, "?") {
			dbURL = dbURL + "&sslmode=disable"
		} else {
			dbURL = dbURL + "?sslmode=disable"
		}
	}

	targetDB, err := sql.Open("postgres", dbURL)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("Failed to open connection to target database: %v", err)})
	}
	defer targetDB.Close()

	if err := targetDB.PingContext(c.UserContext()); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("Target database is unreachable: %v", err)})
	}

	// Apply the compiled DDL statements on target database
	if _, err := targetDB.ExecContext(c.UserContext(), compRes.CompiledDdl); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("Failed to execute migrations: %v", err)})
	}

	// Log execution to database
	locksJSON, _ := json.Marshal(safetyRes.ExclusiveLocks)
	if len(locksJSON) == 0 {
		locksJSON = []byte("[]")
	}

	execRow := &repository.ExecutionRow{
		ExecutionID:                  execID,
		Environment:                  environment,
		RiskLevel:                    safetyRes.RiskLevel,
		RiskScore:                    safetyRes.RiskScore,
		MutatedTables:                int32(len(safetyRes.ExclusiveLocks)),
		DurationUs:                   int64(compRes.Spans.DdlGenerationUs),
		HeavyRewritesDetected:        safetyRes.HeavyRewritesDetected,
		InfrastructureExclusiveLocks: json.RawMessage(locksJSON),
		CompiledDDL:                  compRes.CompiledDdl,
	}

	err = h.svc.LogExecution(
		c.UserContext(),
		execRow,
		tenantID,
		"nacl-cli-remote-push",
		lineageHash,
		int64(compRes.Spans.FrontendParseUs),
		int64(compRes.Spans.TopologicalSortUs),
		int64(compRes.Spans.DdlGenerationUs),
		int64(compRes.Spans.TotalCompilationUs),
	)
	if err != nil {
		// Just log the telemetry write error, do not fail execution since DDL was applied
		fmt.Printf("Telemetry LogExecution error: %v\n", err)
	}

	return c.JSON(fiber.Map{
		"status":  "success",
		"message": "Migrations executed successfully",
	})
}

