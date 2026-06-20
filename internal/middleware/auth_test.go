package middleware

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gofiber/fiber/v2"
	_ "github.com/lib/pq"
)

func TestClerkAuthMiddlewareAll(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("Skipping middleware test: DATABASE_URL not set")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	wsID := "ws-test-middleware"
	userID := "user_test_mw"
	email := "mw-test@company.com"

	// Cleanup first
	_, _ = db.ExecContext(ctx, "DELETE FROM workspace_invitations WHERE workspace_id = $1", wsID)
	_, _ = db.ExecContext(ctx, "DELETE FROM workspace_members WHERE workspace_id = $1", wsID)
	_, _ = db.ExecContext(ctx, "DELETE FROM workspaces WHERE id = $1", wsID)
	_, _ = db.ExecContext(ctx, "DELETE FROM developers WHERE id = $1", userID)

	defer func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM workspace_invitations WHERE workspace_id = $1", wsID)
		_, _ = db.ExecContext(ctx, "DELETE FROM workspace_members WHERE workspace_id = $1", wsID)
		_, _ = db.ExecContext(ctx, "DELETE FROM workspaces WHERE id = $1", wsID)
		_, _ = db.ExecContext(ctx, "DELETE FROM developers WHERE id = $1", userID)
	}()

	// Setup developer and workspace
	_, err = db.ExecContext(ctx, "INSERT INTO developers (id, name, email) VALUES ($1, $2, $3)", userID, "MW User", email)
	if err != nil {
		t.Fatalf("Setup developer failed: %v", err)
	}
	_, err = db.ExecContext(ctx, "INSERT INTO workspaces (id, name, owner_id) VALUES ($1, $2, $3)", wsID, "MW Test Workspace", userID)
	if err != nil {
		t.Fatalf("Setup workspace failed: %v", err)
	}
	_, err = db.ExecContext(ctx, "INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, $3)", wsID, userID, "Admin")
	if err != nil {
		t.Fatalf("Setup member failed: %v", err)
	}

	// Create middleware instance
	mw := NewClerkAuthMiddleware(db)

	app := fiber.New()
	app.Use(mw.Authenticate())
	app.Get("/test", func(c *fiber.Ctx) error {
		clerkID := c.Locals("clerk_user_id").(string)
		activeWS := c.Locals("workspace_id").(string)
		role := c.Locals("role").(string)
		return c.JSON(fiber.Map{
			"clerk_user_id": clerkID,
			"workspace_id":  activeWS,
			"role":          role,
		})
	})

	// Test case: Valid headers
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("x-clerk-user-id", userID)
	req.Header.Set("x-clerk-user-email", email)
	req.Header.Set("x-active-workspace-id", wsID)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Middleware request failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}
