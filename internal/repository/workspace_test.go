package repository

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func TestPruneSettingsAuditLogs(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("Skipping integration test: DATABASE_URL not set")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	repo := NewWorkspaceRepository(db)

	// Set up test workspace if it doesn't exist
	wsID := "ws-test-retention"
	_, _ = db.ExecContext(ctx, "INSERT INTO workspaces (id, name, owner_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING", wsID, "Retention Test Workspace", "test-user")
	defer func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM workspace_settings_audit WHERE workspace_id = $1", wsID)
		_, _ = db.ExecContext(ctx, "DELETE FROM workspaces WHERE id = $1", wsID)
	}()

	now := time.Now().UTC()

	// Insert test audit entries with different changed_at values
	tests := []struct {
		desc        string
		changedAt   time.Time
		shouldPrune bool
	}{
		{"40 days ago (expired)", now.Add(-40 * 24 * time.Hour), true},
		{"35 days ago (expired)", now.Add(-35 * 24 * time.Hour), true},
		{"25 days ago (retained)", now.Add(-25 * 24 * time.Hour), false},
		{"10 days ago (retained)", now.Add(-10 * 24 * time.Hour), false},
		{"1 hour ago (retained)", now.Add(-1 * time.Hour), false},
	}

	// Clear existing test records first
	_, _ = db.ExecContext(ctx, "DELETE FROM workspace_settings_audit WHERE workspace_id = $1", wsID)

	// Insert them
	for _, tc := range tests {
		_, err := db.ExecContext(ctx, `
			INSERT INTO workspace_settings_audit (workspace_id, user_id, old_values, new_values, changed_at)
			VALUES ($1, $2, $3, $4, $5)
		`, wsID, "test-user", "{}", "{}", tc.changedAt)
		if err != nil {
			t.Fatalf("Failed to insert test audit entry (%s): %v", tc.desc, err)
		}
	}

	// Prune logs older than 30 days
	deleted, err := repo.PruneSettingsAuditLogs(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("PruneSettingsAuditLogs returned error: %v", err)
	}

	if deleted != 2 {
		t.Errorf("Expected exactly 2 deleted entries, got %d", deleted)
	}

	// Verify remaining entries in the database
	rows, err := db.QueryContext(ctx, "SELECT changed_at FROM workspace_settings_audit WHERE workspace_id = $1", wsID)
	if err != nil {
		t.Fatalf("Failed to query remaining audit entries: %v", err)
	}
	defer rows.Close()

	var remainingCount int
	for rows.Next() {
		remainingCount++
		var changedAt time.Time
		if err := rows.Scan(&changedAt); err != nil {
			t.Fatalf("Failed to scan changed_at: %v", err)
		}
		
		// Ensure none of the remaining ones are older than 30 days (give 5 min safety buffer for clock skew)
		age := now.Sub(changedAt)
		if age > 30*24*time.Hour + 5*time.Minute {
			t.Errorf("Found a remaining entry older than 30 days: age is %v", age)
		}
	}

	if remainingCount != 3 {
		t.Errorf("Expected exactly 3 remaining entries, got %d", remainingCount)
	}
}

func TestWorkspaceInvitations(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("Skipping integration test: DATABASE_URL not set")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	repo := NewWorkspaceRepository(db)

	wsID := "ws-test-invitations"
	_, _ = db.ExecContext(ctx, "INSERT INTO workspaces (id, name, owner_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING", wsID, "Invite Test Workspace", "test-user")
	defer func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM workspace_invitations WHERE workspace_id = $1", wsID)
		_, _ = db.ExecContext(ctx, "DELETE FROM workspace_members WHERE workspace_id = $1", wsID)
		_, _ = db.ExecContext(ctx, "DELETE FROM workspaces WHERE id = $1", wsID)
	}()

	invite := &InvitationRow{
		ID:          "inv-test-1",
		WorkspaceID: wsID,
		Email:       "new-member@company.com",
		Role:        "SRE",
		TokenHash:   "hash123",
		InvitedBy:   "admin-user",
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	}

	// 1. Create Invitation
	err = repo.CreateInvitation(ctx, invite)
	if err != nil {
		t.Fatalf("CreateInvitation failed: %v", err)
	}

	// 2. Lookup invitation
	res, err := repo.GetInvitationByTokenHash(ctx, "hash123")
	if err != nil {
		t.Fatalf("GetInvitationByTokenHash failed: %v", err)
	}
	if res.Email != invite.Email || res.Role != invite.Role {
		t.Errorf("Invitation mismatch. Expected %s/%s, got %s/%s", invite.Email, invite.Role, res.Email, res.Role)
	}

	// 3. List pending invitations
	invites, err := repo.ListInvitations(ctx, wsID)
	if err != nil {
		t.Fatalf("ListInvitations failed: %v", err)
	}
	if len(invites) != 1 {
		t.Errorf("Expected 1 pending invitation, got %d", len(invites))
	}

	// 4. Accept Invitation
	err = repo.AcceptInvitation(ctx, invite.ID, wsID, "user_invitee_999", invite.Role)
	if err != nil {
		t.Fatalf("AcceptInvitation failed: %v", err)
	}

	// Verify member exists in workspace_members
	var role string
	err = db.QueryRowContext(ctx, "SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2", wsID, "user_invitee_999").Scan(&role)
	if err != nil {
		t.Fatalf("Failed to query workspace member: %v", err)
	}
	if role != invite.Role {
		t.Errorf("Expected role %s, got %s", invite.Role, role)
	}

	// Verify list is now empty (status is no longer pending)
	invites, err = repo.ListInvitations(ctx, wsID)
	if err != nil {
		t.Fatalf("ListInvitations failed: %v", err)
	}
	if len(invites) != 0 {
		t.Errorf("Expected 0 pending invitations, got %d", len(invites))
	}
}

