package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gofiber/fiber/v2"
	_ "github.com/lib/pq"
	"github.com/nacl-org/nacl-cloud-go/internal/repository"
	"github.com/nacl-org/nacl-cloud-go/internal/service"
	enginev1 "github.com/nacl-org/nacl-cloud-go/pkg/pb/engine/v1"
)

func TestWorkspaceHandlerAll(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("Skipping handler integration test: DATABASE_URL not set")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	repo := repository.NewWorkspaceRepository(db)
	svc := service.NewWorkspaceService(repo)
	h := NewWorkspaceHandler(svc)

	wsID := "ws-test-handler-all"
	ownerID := "owner-test-handler"
	memberID := "member-test-handler"

	// Cleanup first
	_, _ = db.ExecContext(ctx, "DELETE FROM workspace_settings_audit WHERE workspace_id = $1", wsID)
	_, _ = db.ExecContext(ctx, "DELETE FROM workspace_invitations WHERE workspace_id = $1", wsID)
	_, _ = db.ExecContext(ctx, "DELETE FROM workspace_members WHERE workspace_id = $1", wsID)
	_, _ = db.ExecContext(ctx, "DELETE FROM workspaces WHERE id = $1", wsID)
	_, _ = db.ExecContext(ctx, "DELETE FROM developers WHERE id = $1 OR id = $2", ownerID, memberID)

	defer func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM workspace_settings_audit WHERE workspace_id = $1", wsID)
		_, _ = db.ExecContext(ctx, "DELETE FROM workspace_invitations WHERE workspace_id = $1", wsID)
		_, _ = db.ExecContext(ctx, "DELETE FROM workspace_members WHERE workspace_id = $1", wsID)
		_, _ = db.ExecContext(ctx, "DELETE FROM workspaces WHERE id = $1", wsID)
		_, _ = db.ExecContext(ctx, "DELETE FROM developers WHERE id = $1 OR id = $2", ownerID, memberID)
	}()

	// Setup developers
	_, _ = db.ExecContext(ctx, "INSERT INTO developers (id, name, email) VALUES ($1, $2, $3)", ownerID, "Owner Name", "owner-h@company.com")
	_, _ = db.ExecContext(ctx, "INSERT INTO developers (id, name, email) VALUES ($1, $2, $3)", memberID, "Member Name", "member-h@company.com")

	// Setup Fiber App
	app := fiber.New()

	// Inject Locals middleware
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("clerk_user_id", ownerID)
		c.Locals("workspace_id", wsID)
		c.Locals("role", "Admin")
		return c.Next()
	})

	// Register Routes
	app.Get("/api/v1/workspaces", h.ListWorkspaces)
	app.Post("/api/v1/workspaces", h.CreateWorkspace)
	app.Get("/api/v1/workspaces/members", h.ListWorkspaceMembers)
	app.Post("/api/v1/workspaces/members", h.AddWorkspaceMember)
	app.Delete("/api/v1/workspaces/members", h.RemoveWorkspaceMember)
	app.Post("/api/v1/workspaces/invitations", h.CreateInvitation)
	app.Post("/api/v1/workspaces/invitations/accept", h.AcceptInvitation)
	app.Post("/api/v1/workspaces/invitations/:id/revoke", h.RevokeInvitation)
	app.Get("/api/v1/workspaces/invitations", h.ListInvitations)
	app.Delete("/api/v1/workspaces/:id", h.DeleteWorkspace)
	app.Get("/api/v1/workspaces/:id/settings", h.GetSettings)
	app.Patch("/api/v1/workspaces/:id/settings", h.UpdateSettings)
	app.Get("/api/v1/workspaces/:id/settings/audit", h.GetSettingsAuditLogs)

	// 1. Create Workspace
	reqBody, _ := json.Marshal(map[string]string{"name": "Handler Test Workspace"})
	req := httptest.NewRequest("POST", "/api/v1/workspaces", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("CreateWorkspace handler failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Read generated workspace ID from response
	respBody, _ := io.ReadAll(resp.Body)
	var createResp map[string]interface{}
	_ = json.Unmarshal(respBody, &createResp)
	createdWsID := createResp["id"].(string)

	// Update active workspace in a middleware replacement for subsequent routes or use the created ID
	// Let's create another fiber app or dynamic middleware to test specific workspace IDs
	appDynamic := fiber.New()
	appDynamic.Use(func(c *fiber.Ctx) error {
		c.Locals("clerk_user_id", ownerID)
		c.Locals("workspace_id", createdWsID)
		c.Locals("role", "Admin")
		return c.Next()
	})
	appDynamic.Get("/api/v1/workspaces", h.ListWorkspaces)
	appDynamic.Get("/api/v1/workspaces/members", h.ListWorkspaceMembers)
	appDynamic.Post("/api/v1/workspaces/members", h.AddWorkspaceMember)
	appDynamic.Delete("/api/v1/workspaces/members", h.RemoveWorkspaceMember)
	appDynamic.Post("/api/v1/workspaces/invitations", h.CreateInvitation)
	appDynamic.Post("/api/v1/workspaces/invitations/accept", h.AcceptInvitation)
	appDynamic.Post("/api/v1/workspaces/invitations/:id/revoke", h.RevokeInvitation)
	appDynamic.Get("/api/v1/workspaces/invitations", h.ListInvitations)
	appDynamic.Delete("/api/v1/workspaces/:id", h.DeleteWorkspace)
	appDynamic.Get("/api/v1/workspaces/:id/settings", h.GetSettings)
	appDynamic.Patch("/api/v1/workspaces/:id/settings", h.UpdateSettings)
	appDynamic.Get("/api/v1/workspaces/:id/settings/audit", h.GetSettingsAuditLogs)

	// 2. List Workspaces
	req = httptest.NewRequest("GET", "/api/v1/workspaces", nil)
	resp, _ = appDynamic.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("ListWorkspaces expected status 200, got %d", resp.StatusCode)
	}

	// 3. List Members
	req = httptest.NewRequest("GET", "/api/v1/workspaces/members", nil)
	resp, _ = appDynamic.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("ListWorkspaceMembers expected status 200, got %d", resp.StatusCode)
	}

	// 4. Add Member
	memberReq, _ := json.Marshal(map[string]string{"user_id": memberID, "role": "Developer"})
	req = httptest.NewRequest("POST", "/api/v1/workspaces/members", bytes.NewBuffer(memberReq))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = appDynamic.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("AddWorkspaceMember expected status 200, got %d", resp.StatusCode)
	}

	// 5. Create Invitation
	inviteReq, _ := json.Marshal(map[string]interface{}{
		"email":         "new-user@test.com",
		"role":          "Viewer",
		"workspace_ids": []string{createdWsID},
	})
	req = httptest.NewRequest("POST", "/api/v1/workspaces/invitations", bytes.NewBuffer(inviteReq))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = appDynamic.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("CreateInvitation expected status 200, got %d", resp.StatusCode)
	}

	// Read invitation ID
	respBody, _ = io.ReadAll(resp.Body)
	var inviteResp map[string]interface{}
	_ = json.Unmarshal(respBody, &inviteResp)
	createdInviteID := inviteResp["invite_id"].(string)

	// 6. List Invitations
	req = httptest.NewRequest("GET", "/api/v1/workspaces/invitations", nil)
	resp, _ = appDynamic.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("ListInvitations expected status 200, got %d", resp.StatusCode)
	}

	// 7. Revoke Invitation
	req = httptest.NewRequest("POST", "/api/v1/workspaces/invitations/"+createdInviteID+"/revoke", nil)
	resp, _ = appDynamic.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("RevokeInvitation expected status 200, got %d", resp.StatusCode)
	}

	// 8. Get Settings
	req = httptest.NewRequest("GET", "/api/v1/workspaces/"+createdWsID+"/settings", nil)
	resp, _ = appDynamic.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("GetSettings expected status 200, got %d", resp.StatusCode)
	}

	// 9. Update Settings
	newSettings := &enginev1.WorkspaceSettings{
		PaddingEnabled:      false,
		LockTimeoutMs:       5000,
		MaxRetries:          10,
		DefaultChunkSize:    5000,
		SreApprovalRequired: true,
	}
	settingsJSON, _ := json.Marshal(newSettings)
	req = httptest.NewRequest("PATCH", "/api/v1/workspaces/"+createdWsID+"/settings", bytes.NewBuffer(settingsJSON))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = appDynamic.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("UpdateSettings expected status 200, got %d", resp.StatusCode)
	}

	// 10. Get Settings Audit Logs
	req = httptest.NewRequest("GET", "/api/v1/workspaces/"+createdWsID+"/settings/audit", nil)
	resp, _ = appDynamic.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("GetSettingsAuditLogs expected status 200, got %d", resp.StatusCode)
	}

	// 11. Remove Member
	removeReq, _ := json.Marshal(map[string]string{"user_id": memberID})
	req = httptest.NewRequest("DELETE", "/api/v1/workspaces/members", bytes.NewBuffer(removeReq))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = appDynamic.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("RemoveWorkspaceMember expected status 200, got %d", resp.StatusCode)
	}

	// 12. Delete Workspace
	req = httptest.NewRequest("DELETE", "/api/v1/workspaces/"+createdWsID, nil)
	resp, _ = appDynamic.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("DeleteWorkspace expected status 200, got %d", resp.StatusCode)
	}

	// 13. Test bad request paths (400, 401, 403 errors to cover more branches in workspace handler)
	appError := fiber.New()
	appError.Post("/api/v1/workspaces", h.CreateWorkspace)

	// Post workspace without user in Locals (Unauthenticated)
	req = httptest.NewRequest("POST", "/api/v1/workspaces", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = appError.Test(req)
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", resp.StatusCode)
	}

	// Post workspace with empty name
	appErrorWithUser := fiber.New()
	appErrorWithUser.Use(func(c *fiber.Ctx) error {
		c.Locals("clerk_user_id", ownerID)
		return c.Next()
	})
	appErrorWithUser.Post("/api/v1/workspaces", h.CreateWorkspace)
	badBody, _ := json.Marshal(map[string]string{"name": "   "})
	req = httptest.NewRequest("POST", "/api/v1/workspaces", bytes.NewBuffer(badBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = appErrorWithUser.Test(req)
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Errorf("Expected status 400 for empty name, got %d", resp.StatusCode)
	}
}
