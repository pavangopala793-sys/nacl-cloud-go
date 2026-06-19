//go:generate bash ../../../nacl-proto-schema/generate.sh

package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/nacl-org/nacl-cloud-go/internal/config"
	"github.com/nacl-org/nacl-cloud-go/internal/handler"
	"github.com/nacl-org/nacl-cloud-go/internal/middleware"
	"google.golang.org/grpc"
)

// App aggregates all initialized modules for runtime management.
type App struct {
	WebEngine        *fiber.App
	DB               *sql.DB
	GrpcConn         *grpc.ClientConn
	Config           *config.Config
	UserHandler      *handler.UserHandler
	WorkspaceHandler *handler.WorkspaceHandler
	TelemetryHandler *handler.TelemetryHandler
	AuthMiddleware   *middleware.ClerkAuthMiddleware
}

// NewApp is the factory constructor used by Google Wire.
func NewApp(
	web *fiber.App,
	db *sql.DB,
	conn *grpc.ClientConn,
	cfg *config.Config,
	userHandler *handler.UserHandler,
	workspaceHandler *handler.WorkspaceHandler,
	telemetryHandler *handler.TelemetryHandler,
	authMiddleware *middleware.ClerkAuthMiddleware,
) *App {
	return &App{
		WebEngine:        web,
		DB:               db,
		GrpcConn:         conn,
		Config:           cfg,
		UserHandler:      userHandler,
		WorkspaceHandler: workspaceHandler,
		TelemetryHandler: telemetryHandler,
		AuthMiddleware:   authMiddleware,
	}
}

// NewFiberApp initializes and configures a new Fiber web engine.
func NewFiberApp() *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:        "NACL Cloud API (Go)",
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		ReadBufferSize: 80000,
	})
	app.Use(logger.New(logger.Config{
		Format: "[${time}] ${status} - ${latency} ${method} ${path}\n",
	}))
	return app
}

func main() {
	log.Println("Bootstrapping nacl-cloud-go (Google Architecture)...")

	// 1. Resolve dependency graph using generated Wire constructor
	app, cleanup, err := InitializeApp()
	if err != nil {
		log.Fatalf("Fatal: Failed to resolve dependencies: %v", err)
	}
	defer cleanup()

	// 2. Register REST routes
	app.registerRoutes()

	// 3. Graceful Shutdown listener
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	// Start settings audit log pruner background worker
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()

	go func() {
		log.Println("Starting settings audit log pruner background worker (30 days retention policy)...")
		
		// Run a pruning operation shortly after startup (e.g., 5 seconds) to handle initial backlog
		select {
		case <-time.After(5 * time.Second):
			runPruning(workerCtx, app)
		case <-workerCtx.Done():
			return
		}

		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				runPruning(workerCtx, app)
			case <-workerCtx.Done():
				log.Println("Settings audit log pruner background worker stopped.")
				return
			}
		}
	}()

	go func() {
		log.Printf("Starting HTTP Web Server on port %s...", app.Config.Port)
		if err := app.WebEngine.Listen(":" + app.Config.Port); err != nil {
			log.Printf("Web server stopped: %v", err)
		}
	}()

	<-shutdownChan
	log.Println("Shutdown signal received. Starting graceful shutdown...")
	cancelWorker() // Cancel background worker immediately to stop active loops

	// Close web server with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := app.WebEngine.ShutdownWithContext(ctx); err != nil {
		log.Printf("Error during web engine shutdown: %v", err)
	}

	log.Println("Clean shutdown complete. Exiting.")
}

func runPruning(ctx context.Context, app *App) {
	log.Println("Background worker: Starting settings audit log pruning...")
	
	// Define retention period: 30 days
	retentionPeriod := 30 * 24 * time.Hour
	
	// Perform pruning
	deletedCount, err := app.WorkspaceHandler.GetService().PruneSettingsAuditLogs(ctx, retentionPeriod)
	if err != nil {
		log.Printf("Background worker error: Failed to prune settings audit logs: %v", err)
		return
	}
	
	if deletedCount > 0 {
		log.Printf("Background worker: Successfully pruned %d expired settings audit logs (older than 30 days)", deletedCount)
	} else {
		log.Println("Background worker: No expired settings audit logs found to prune.")
	}
}


// registerRoutes wires up handlers to HTTP endpoints.
func (a *App) registerRoutes() {
	// Root status checks
	a.WebEngine.Get("/ready", func(c *fiber.Ctx) error {
		if err := a.DB.Ping(); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).SendString("DB UNREACHABLE")
		}
		return c.SendString("READY")
	})

	api := a.WebEngine.Group("/api")
	v1 := api.Group("/v1")

	// Protected routes under ClerkAuthMiddleware
	protected := v1.Use(a.AuthMiddleware.Authenticate())

	protected.Get("/status", func(c *fiber.Ctx) error {
		return c.SendString("API is protected")
	})

	// Workspaces endpoints
	protected.Get("/workspaces", a.WorkspaceHandler.ListWorkspaces)
	protected.Post("/workspaces", a.WorkspaceHandler.CreateWorkspace)
	protected.Get("/workspaces/members", a.WorkspaceHandler.ListWorkspaceMembers)
	protected.Post("/workspaces/members", a.WorkspaceHandler.AddWorkspaceMember)
	protected.Delete("/workspaces/members", a.WorkspaceHandler.RemoveWorkspaceMember)
	protected.Post("/workspaces/invitations", a.WorkspaceHandler.CreateInvitation)
	protected.Post("/workspaces/invitations/accept", a.WorkspaceHandler.AcceptInvitation)
	protected.Post("/workspaces/invitations/:id/revoke", a.WorkspaceHandler.RevokeInvitation)
	protected.Get("/workspaces/invitations", a.WorkspaceHandler.ListInvitations)
	protected.Delete("/workspaces/:id", a.WorkspaceHandler.DeleteWorkspace)
	protected.Get("/workspaces/:id/settings", a.WorkspaceHandler.GetSettings)
	protected.Patch("/workspaces/:id/settings", a.WorkspaceHandler.UpdateSettings)
	protected.Get("/workspaces/:id/settings/audit", a.WorkspaceHandler.GetSettingsAuditLogs)

	// Telemetry & Recommendations
	protected.Post("/plan", a.TelemetryHandler.CreatePlan)
	protected.Post("/apply", a.TelemetryHandler.ApplyPlan)
	protected.Get("/config/connection", a.TelemetryHandler.GetConnectionPrerequisites)
	protected.Get("/feature-flags", a.TelemetryHandler.GetFeatureFlags)
	protected.Get("/telemetry/performance", a.TelemetryHandler.GetPerformance)
	protected.Get("/telemetry/risk", a.TelemetryHandler.GetRisk)
	protected.Get("/telemetry/velocity", a.TelemetryHandler.GetVelocity)
	protected.Get("/telemetry/executions", a.TelemetryHandler.GetExecutions)
	protected.Get("/telemetry/recommendations", a.TelemetryHandler.GetRecommendations)
	protected.Post("/telemetry/recommendations/:id/approve", a.TelemetryHandler.ApproveRecommendation)
	protected.Post("/telemetry/recommendations/:id/reject", a.TelemetryHandler.RejectRecommendation)
	protected.Get("/telemetry/prs", a.TelemetryHandler.GetPRs)
	// protected.Get("/migrations/chunked", a.TelemetryHandler.GetChunkedMigrations)

	// Scheduler Hold Queue
	// protected.Get("/scheduler/hold-queue", a.TelemetryHandler.GetSchedulerHoldQueue)
	// protected.Post("/scheduler/hold-queue/:id/dispatch", a.TelemetryHandler.DispatchHoldQueue)

	// Legacy User endpoints (optional / debug)
	users := v1.Group("/users")
	users.Post("/", a.UserHandler.CreateUser)
	users.Get("/:id", a.UserHandler.GetUserByID)
}
