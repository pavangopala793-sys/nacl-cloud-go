package service

import (
	"context"
	"time"

	"github.com/nacl-org/nacl-cloud-go/internal/repository"
	enginev1 "github.com/nacl-org/nacl-cloud-go/pkg/pb/engine/v1"
)

type WorkspaceService struct {
	repo repository.WorkspaceRepository
}

func NewWorkspaceService(repo repository.WorkspaceRepository) *WorkspaceService {
	return &WorkspaceService{repo: repo}
}

func (s *WorkspaceService) ListWorkspaces(ctx context.Context, userID string) ([]repository.WorkspaceRow, error) {
	return s.repo.ListWorkspaces(ctx, userID)
}

func (s *WorkspaceService) CreateWorkspace(ctx context.Context, id, name, ownerID string) error {
	return s.repo.CreateWorkspace(ctx, id, name, ownerID)
}

func (s *WorkspaceService) AddWorkspaceMember(ctx context.Context, wsID, userID, role string) error {
	return s.repo.AddWorkspaceMember(ctx, wsID, userID, role)
}

func (s *WorkspaceService) RemoveWorkspaceMember(ctx context.Context, wsID, userID string) error {
	return s.repo.RemoveWorkspaceMember(ctx, wsID, userID)
}

func (s *WorkspaceService) GetWorkspaceOwner(ctx context.Context, wsID string) (string, error) {
	return s.repo.GetWorkspaceOwner(ctx, wsID)
}

func (s *WorkspaceService) ListWorkspaceMembers(ctx context.Context, wsID string) ([]repository.WorkspaceMemberRow, error) {
	return s.repo.ListWorkspaceMembers(ctx, wsID)
}

func (s *WorkspaceService) DeleteWorkspace(ctx context.Context, wsID string) error {
	return s.repo.DeleteWorkspace(ctx, wsID)
}

func (s *WorkspaceService) GetWorkspaceSettings(ctx context.Context, wsID string) (*enginev1.WorkspaceSettings, error) {
	return s.repo.GetWorkspaceSettings(ctx, wsID)
}

func (s *WorkspaceService) UpdateWorkspaceSettings(ctx context.Context, wsID string, settings *enginev1.WorkspaceSettings, userID string) error {
	return s.repo.UpdateWorkspaceSettings(ctx, wsID, settings, userID)
}

func (s *WorkspaceService) GetSettingsAuditLogs(ctx context.Context, wsID string) ([]*repository.SettingsAuditEntry, error) {
	return s.repo.GetSettingsAuditLogs(ctx, wsID)
}

func (s *WorkspaceService) PruneSettingsAuditLogs(ctx context.Context, olderThan time.Duration) (int64, error) {
	return s.repo.PruneSettingsAuditLogs(ctx, olderThan)
}

func (s *WorkspaceService) CreateInvitation(ctx context.Context, invite *repository.InvitationRow) error {
	return s.repo.CreateInvitation(ctx, invite)
}

func (s *WorkspaceService) GetInvitationByTokenHash(ctx context.Context, tokenHash string) (*repository.InvitationRow, error) {
	return s.repo.GetInvitationByTokenHash(ctx, tokenHash)
}

func (s *WorkspaceService) AcceptInvitation(ctx context.Context, inviteID, wsID, userID, role string) error {
	return s.repo.AcceptInvitation(ctx, inviteID, wsID, userID, role)
}

func (s *WorkspaceService) RevokeInvitation(ctx context.Context, inviteID string) error {
	return s.repo.RevokeInvitation(ctx, inviteID)
}

func (s *WorkspaceService) ListInvitations(ctx context.Context, wsID string) ([]repository.InvitationRow, error) {
	return s.repo.ListInvitations(ctx, wsID)
}

