package service

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/nacl-org/nacl-cloud-go/internal/repository"
	_ "github.com/lib/pq"
)

func TestWorkspaceServiceAll(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("Skipping service integration test: DATABASE_URL not set")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	repo := repository.NewWorkspaceRepository(db)
	svc := NewWorkspaceService(repo)

	wsID := "ws-test-all-svc"
	ownerID := "owner-all-svc"
	memberID := "member-all-svc"

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
	_, _ = db.ExecContext(ctx, "INSERT INTO developers (id, name, email) VALUES ($1, $2, $3)", ownerID, "Owner Name", "owner-svc@company.com")
	_, _ = db.ExecContext(ctx, "INSERT INTO developers (id, name, email) VALUES ($1, $2, $3)", memberID, "Member Name", "member-svc@company.com")

	// 1. Create Workspace
	err = svc.CreateWorkspace(ctx, wsID, "Test Service Workspace", ownerID)
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	// 2. Get Workspace Owner
	owner, err := svc.GetWorkspaceOwner(ctx, wsID)
	if err != nil {
		t.Fatalf("GetWorkspaceOwner failed: %v", err)
	}
	if owner != ownerID {
		t.Errorf("Expected owner %s, got %s", ownerID, owner)
	}

	// 3. Add Workspace Member
	err = svc.AddWorkspaceMember(ctx, wsID, memberID, "SRE")
	if err != nil {
		t.Fatalf("AddWorkspaceMember failed: %v", err)
	}

	// 4. List Workspace Members
	members, err := svc.ListWorkspaceMembers(ctx, wsID)
	if err != nil {
		t.Fatalf("ListWorkspaceMembers failed: %v", err)
	}
	if len(members) != 2 {
		t.Errorf("Expected 2 members, got %d", len(members))
	}

	// 5. List Workspaces
	workspaces, err := svc.ListWorkspaces(ctx, memberID)
	if err != nil {
		t.Fatalf("ListWorkspaces failed: %v", err)
	}
	if len(workspaces) != 1 || workspaces[0].ID != wsID {
		t.Errorf("Expected workspace %s, got %v", wsID, workspaces)
	}

	// 6. Get settings
	settings, err := svc.GetWorkspaceSettings(ctx, wsID)
	if err != nil {
		t.Fatalf("GetWorkspaceSettings failed: %v", err)
	}

	// 7. Update workspace settings
	settings.LockTimeoutMs = 1500
	err = svc.UpdateWorkspaceSettings(ctx, wsID, settings, ownerID)
	if err != nil {
		t.Fatalf("UpdateWorkspaceSettings failed: %v", err)
	}

	// 8. Get settings audit logs
	logs, err := svc.GetSettingsAuditLogs(ctx, wsID)
	if err != nil {
		t.Fatalf("GetSettingsAuditLogs failed: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("Expected 1 audit entry, got %d", len(logs))
	}

	// 9. Create, Get, Revoke, List invitations
	inv := &repository.InvitationRow{
		ID:          "inv-svc-temp",
		WorkspaceID: wsID,
		Email:       "temp-svc@company.com",
		Role:        "Developer",
		TokenHash:   "hash_svc",
		InvitedBy:   ownerID,
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	}
	err = svc.CreateInvitation(ctx, inv)
	if err != nil {
		t.Fatalf("CreateInvitation failed: %v", err)
	}

	res, err := svc.GetInvitationByTokenHash(ctx, "hash_svc")
	if err != nil {
		t.Fatalf("GetInvitationByTokenHash failed: %v", err)
	}
	if res.ID != inv.ID {
		t.Errorf("Expected invite %s, got %s", inv.ID, res.ID)
	}

	invs, err := svc.ListInvitations(ctx, wsID)
	if err != nil {
		t.Fatalf("ListInvitations failed: %v", err)
	}
	if len(invs) != 1 {
		t.Errorf("Expected 1 pending invitation, got %d", len(invs))
	}

	err = svc.AcceptInvitation(ctx, inv.ID, wsID, "user_invitee_svc", "Developer")
	if err != nil {
		t.Fatalf("AcceptInvitation failed: %v", err)
	}

	// 10. Prune audit logs
	deleted, err := svc.PruneSettingsAuditLogs(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("PruneSettingsAuditLogs failed: %v", err)
	}
	if deleted != 0 {
		t.Errorf("Expected 0 deleted logs, got %d", deleted)
	}

	// 11. Revoke invitation
	err = svc.RevokeInvitation(ctx, inv.ID)
	if err != nil {
		t.Fatalf("RevokeInvitation failed: %v", err)
	}

	// 12. Remove member
	err = svc.RemoveWorkspaceMember(ctx, wsID, memberID)
	if err != nil {
		t.Fatalf("RemoveWorkspaceMember failed: %v", err)
	}

	// 13. Delete workspace
	err = svc.DeleteWorkspace(ctx, wsID)
	if err != nil {
		t.Fatalf("DeleteWorkspace failed: %v", err)
	}
}
