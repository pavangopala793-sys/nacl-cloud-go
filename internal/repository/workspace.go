package repository

import (
	"context"
	"database/sql"
	"time"

	enginev1 "github.com/nacl-org/nacl-cloud-go/pkg/pb/engine/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

type WorkspaceRow struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	OwnerID string `json:"owner_id"`
}

type WorkspaceMemberRow struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	Role        string `json:"role"`
	JoinedAt    string `json:"joined_at"`
	Email       string `json:"email"`
}

type WorkspaceRepository interface {
	ListWorkspaces(ctx context.Context, userID string) ([]WorkspaceRow, error)
	CreateWorkspace(ctx context.Context, id, name, ownerID string) error
	AddWorkspaceMember(ctx context.Context, wsID, userID, role string) error
	RemoveWorkspaceMember(ctx context.Context, wsID, userID string) error
	GetWorkspaceOwner(ctx context.Context, wsID string) (string, error)
	ListWorkspaceMembers(ctx context.Context, wsID string) ([]WorkspaceMemberRow, error)
	DeleteWorkspace(ctx context.Context, id string) error
	GetWorkspaceSettings(ctx context.Context, id string) (*enginev1.WorkspaceSettings, error)
	UpdateWorkspaceSettings(ctx context.Context, id string, settings *enginev1.WorkspaceSettings, userID string) error
	GetSettingsAuditLogs(ctx context.Context, id string) ([]*SettingsAuditEntry, error)
	PruneSettingsAuditLogs(ctx context.Context, olderThan time.Duration) (int64, error)
	
	// Enterprise Onboarding & Invitations
	CreateInvitation(ctx context.Context, invite *InvitationRow) error
	GetInvitationByTokenHash(ctx context.Context, tokenHash string) (*InvitationRow, error)
	AcceptInvitation(ctx context.Context, inviteID, wsID, userID, role string) error
	RevokeInvitation(ctx context.Context, inviteID string) error
	ListInvitations(ctx context.Context, wsID string) ([]InvitationRow, error)
}

type InvitationRow struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Email       string    `json:"email"`
	Role        string    `json:"role"`
	TokenHash   string    `json:"token_hash"`
	InvitedBy   string    `json:"invited_by"`
	Status      string    `json:"status"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
}

type SettingsAuditEntry struct {
	ID          int       `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	UserID      string    `json:"user_id"`
	OldValues   string    `json:"old_values"`
	NewValues   string    `json:"new_values"`
	ChangedAt   time.Time `json:"changed_at"`
}

type PostgresWorkspaceRepository struct {
	db *sql.DB
}

func NewWorkspaceRepository(db *sql.DB) WorkspaceRepository {
	return &PostgresWorkspaceRepository{db: db}
}

func (r *PostgresWorkspaceRepository) ListWorkspaces(ctx context.Context, userID string) ([]WorkspaceRow, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT w.id, w.name, m.role, w.owner_id 
		 FROM workspaces w
		 JOIN workspace_members m ON w.id = m.workspace_id
		 WHERE m.user_id = $1`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []WorkspaceRow
	for rows.Next() {
		var w WorkspaceRow
		if err := rows.Scan(&w.ID, &w.Name, &w.Role, &w.OwnerID); err == nil {
			result = append(result, w)
		}
	}
	return result, nil
}

func (r *PostgresWorkspaceRepository) CreateWorkspace(ctx context.Context, id, name, ownerID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		"INSERT INTO workspaces (id, name, owner_id) VALUES ($1, $2, $3)",
		id, name, ownerID,
	)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx,
		"INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, $3)",
		id, ownerID, "Admin",
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (r *PostgresWorkspaceRepository) AddWorkspaceMember(ctx context.Context, wsID, userID, role string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) 
		 VALUES ($1, $2, $3) 
		 ON CONFLICT (workspace_id, user_id) 
		 DO UPDATE SET role = EXCLUDED.role`,
		wsID, userID, role,
	)
	return err
}

func (r *PostgresWorkspaceRepository) RemoveWorkspaceMember(ctx context.Context, wsID, userID string) error {
	_, err := r.db.ExecContext(ctx,
		"DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2",
		wsID, userID,
	)
	return err
}

func (r *PostgresWorkspaceRepository) GetWorkspaceOwner(ctx context.Context, wsID string) (string, error) {
	var ownerID string
	err := r.db.QueryRowContext(ctx, "SELECT owner_id FROM workspaces WHERE id = $1", wsID).Scan(&ownerID)
	return ownerID, err
}

func (r *PostgresWorkspaceRepository) ListWorkspaceMembers(ctx context.Context, wsID string) ([]WorkspaceMemberRow, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT wm.workspace_id, wm.user_id, wm.role, wm.joined_at::text as joined_at, COALESCE(d.email, wm.user_id) as email
		 FROM workspace_members wm
		 LEFT JOIN developers d ON wm.user_id = d.id
		 WHERE wm.workspace_id = $1`,
		wsID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []WorkspaceMemberRow{}
	for rows.Next() {
		var m WorkspaceMemberRow
		if err := rows.Scan(&m.WorkspaceID, &m.UserID, &m.Role, &m.JoinedAt, &m.Email); err == nil {
			result = append(result, m)
		}
	}
	return result, nil
}

func (r *PostgresWorkspaceRepository) DeleteWorkspace(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, "DELETE FROM workspace_members WHERE workspace_id = $1", id)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, "DELETE FROM workspaces WHERE id = $1", id)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (r *PostgresWorkspaceRepository) GetWorkspaceSettings(ctx context.Context, id string) (*enginev1.WorkspaceSettings, error) {
	var settingsJSON []byte
	err := r.db.QueryRowContext(ctx, "SELECT settings FROM workspaces WHERE id = $1", id).Scan(&settingsJSON)
	if err != nil {
		return nil, err
	}

	settings := &enginev1.WorkspaceSettings{
		PaddingEnabled:      true,
		ChunkingEnabled:     true,
		DefaultChunkSize:    5000,
		LockTimeoutMs:       2000,
		MaxRetries:          5,
		SreApprovalRequired: true,
		SandboxDryRun:       false,
	}

	if len(settingsJSON) > 0 && string(settingsJSON) != "{}" {
		err = protojson.Unmarshal(settingsJSON, settings)
		if err != nil {
			return nil, err
		}
	}

	return settings, nil
}

func (r *PostgresWorkspaceRepository) UpdateWorkspaceSettings(ctx context.Context, id string, settings *enginev1.WorkspaceSettings, userID string) error {
	// 1. Fetch old settings first for auditing
	oldSettings, err := r.GetWorkspaceSettings(ctx, id)
	if err != nil {
		return err
	}

	// Marshal new settings using protojson
	opts := protojson.MarshalOptions{
		EmitUnpopulated: true,
		UseProtoNames:   true,
	}
	newJSON, err := opts.Marshal(settings)
	if err != nil {
		return err
	}

	oldJSON, err := opts.Marshal(oldSettings)
	if err != nil {
		return err
	}

	// 2. Perform updates in an atomic transaction
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Update settings column in workspaces
	_, err = tx.ExecContext(ctx, "UPDATE workspaces SET settings = $1 WHERE id = $2", newJSON, id)
	if err != nil {
		return err
	}

	// Insert settings audit log record
	_, err = tx.ExecContext(ctx, 
		`INSERT INTO workspace_settings_audit (workspace_id, user_id, old_values, new_values) 
		 VALUES ($1, $2, $3, $4)`,
		id, userID, oldJSON, newJSON,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (r *PostgresWorkspaceRepository) GetSettingsAuditLogs(ctx context.Context, id string) ([]*SettingsAuditEntry, error) {
	rows, err := r.db.QueryContext(ctx, 
		`SELECT id, workspace_id, user_id, old_values, new_values, changed_at 
		 FROM workspace_settings_audit 
		 WHERE workspace_id = $1 
		 ORDER BY changed_at DESC LIMIT 50`, 
		id,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*SettingsAuditEntry
	for rows.Next() {
		var entry SettingsAuditEntry
		var oldVal, newVal []byte
		err := rows.Scan(&entry.ID, &entry.WorkspaceID, &entry.UserID, &oldVal, &newVal, &entry.ChangedAt)
		if err != nil {
			return nil, err
		}
		entry.OldValues = string(oldVal)
		entry.NewValues = string(newVal)
		entries = append(entries, &entry)
	}

	return entries, nil
}

func (r *PostgresWorkspaceRepository) PruneSettingsAuditLogs(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	var totalDeleted int64
	for {
		if err := ctx.Err(); err != nil {
			return totalDeleted, err
		}

		res, err := r.db.ExecContext(ctx, `
			DELETE FROM workspace_settings_audit 
			WHERE id IN (
				SELECT id 
				FROM workspace_settings_audit 
				WHERE changed_at < $1 
				LIMIT 500
			)
		`, cutoff)
		if err != nil {
			return totalDeleted, err
		}

		rowsAffected, err := res.RowsAffected()
		if err != nil {
			return totalDeleted, err
		}

		totalDeleted += rowsAffected
		if rowsAffected == 0 {
			break
		}

		select {
		case <-ctx.Done():
			return totalDeleted, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return totalDeleted, nil
}

func (r *PostgresWorkspaceRepository) CreateInvitation(ctx context.Context, invite *InvitationRow) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO workspace_invitations (id, workspace_id, email, role, token_hash, invited_by, expires_at, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending')
		ON CONFLICT (workspace_id, email) WHERE status = 'pending'
		DO UPDATE SET 
			role = EXCLUDED.role, 
			token_hash = EXCLUDED.token_hash, 
			invited_by = EXCLUDED.invited_by, 
			expires_at = EXCLUDED.expires_at,
			created_at = CURRENT_TIMESTAMP
	`, invite.ID, invite.WorkspaceID, invite.Email, invite.Role, invite.TokenHash, invite.InvitedBy, invite.ExpiresAt)
	return err
}

func (r *PostgresWorkspaceRepository) GetInvitationByTokenHash(ctx context.Context, tokenHash string) (*InvitationRow, error) {
	var invite InvitationRow
	err := r.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, email, role, token_hash, invited_by, status, expires_at, created_at
		FROM workspace_invitations
		WHERE token_hash = $1
	`, tokenHash).Scan(&invite.ID, &invite.WorkspaceID, &invite.Email, &invite.Role, &invite.TokenHash, &invite.InvitedBy, &invite.Status, &invite.ExpiresAt, &invite.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &invite, nil
}

func (r *PostgresWorkspaceRepository) AcceptInvitation(ctx context.Context, inviteID, wsID, userID, role string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Update invitation status to accepted
	_, err = tx.ExecContext(ctx, `
		UPDATE workspace_invitations
		SET status = 'accepted'
		WHERE id = $1 AND status = 'pending'
	`, inviteID)
	if err != nil {
		return err
	}

	// 2. Add to workspace_members
	_, err = tx.ExecContext(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (workspace_id, user_id)
		DO UPDATE SET role = EXCLUDED.role
	`, wsID, userID, role)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (r *PostgresWorkspaceRepository) RevokeInvitation(ctx context.Context, inviteID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE workspace_invitations
		SET status = 'revoked'
		WHERE id = $1 AND status = 'pending'
	`, inviteID)
	return err
}

func (r *PostgresWorkspaceRepository) ListInvitations(ctx context.Context, wsID string) ([]InvitationRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, workspace_id, email, role, token_hash, invited_by, status, expires_at, created_at
		FROM workspace_invitations
		WHERE workspace_id = $1 AND status = 'pending'
		ORDER BY created_at DESC
	`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []InvitationRow{}
	for rows.Next() {
		var invite InvitationRow
		err := rows.Scan(&invite.ID, &invite.WorkspaceID, &invite.Email, &invite.Role, &invite.TokenHash, &invite.InvitedBy, &invite.Status, &invite.ExpiresAt, &invite.CreatedAt)
		if err == nil {
			result = append(result, invite)
		}
	}
	return result, nil
}


