package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/nacl-org/nacl-cloud-go/internal/auth"
	"github.com/nacl-org/nacl-cloud-go/internal/repository"
	"github.com/nacl-org/nacl-cloud-go/internal/service"
	enginev1 "github.com/nacl-org/nacl-cloud-go/pkg/pb/engine/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"strings"
)

type WorkspaceHandler struct {
	svc *service.WorkspaceService
}

func NewWorkspaceHandler(svc *service.WorkspaceService) *WorkspaceHandler {
	return &WorkspaceHandler{svc: svc}
}

func (h *WorkspaceHandler) ListWorkspaces(c *fiber.Ctx) error {
	clerkUserID, ok := c.Locals("clerk_user_id").(string)
	if !ok || clerkUserID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Missing x-clerk-user-id header"})
	}

	workspaces, err := h.svc.ListWorkspaces(c.UserContext(), clerkUserID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to fetch workspaces: %v", err)})
	}

	return c.JSON(workspaces)
}

type CreateWorkspaceRequest struct {
	Name string `json:"name"`
}

func (h *WorkspaceHandler) CreateWorkspace(c *fiber.Ctx) error {
	clerkUserID, ok := c.Locals("clerk_user_id").(string)
	if !ok || clerkUserID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Missing x-clerk-user-id header"})
	}

	var req CreateWorkspaceRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	if strings.TrimSpace(req.Name) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Workspace name cannot be empty"})
	}

	// Generate workspace ID: ws-<uuid_first_part>
	wsID := fmt.Sprintf("ws-%s", strings.Split(uuid.New().String(), "-")[0])

	err := h.svc.CreateWorkspace(c.UserContext(), wsID, req.Name, clerkUserID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to create workspace: %v", err)})
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"id":       wsID,
		"name":     req.Name,
		"role":     "Admin",
		"owner_id": clerkUserID,
	})
}

func (h *WorkspaceHandler) ListWorkspaceMembers(c *fiber.Ctx) error {
	activeWSID, ok := c.Locals("workspace_id").(string)
	if !ok || activeWSID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Access denied: No active workspace context"})
	}

	members, err := h.svc.ListWorkspaceMembers(c.UserContext(), activeWSID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to fetch workspace members: %v", err)})
	}

	return c.JSON(members)
}

type AddWorkspaceMemberRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

func (h *WorkspaceHandler) AddWorkspaceMember(c *fiber.Ctx) error {
	activeWSID, ok := c.Locals("workspace_id").(string)
	callerRole, _ := c.Locals("role").(string)

	if !ok || activeWSID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Access denied: No active workspace context"})
	}

	// Only Admin can manage members
	if !auth.IsAuthorized(auth.Role(callerRole), auth.ActionManageMembers) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: Only workspace Admins can manage members"})
	}

	var req AddWorkspaceMemberRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	if strings.TrimSpace(req.UserID) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "User ID cannot be empty"})
	}

	if req.Role != "Admin" && req.Role != "SRE" && req.Role != "Developer" && req.Role != "Viewer" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid role. Must be Admin, SRE, Developer, or Viewer"})
	}

	err := h.svc.AddWorkspaceMember(c.UserContext(), activeWSID, req.UserID, req.Role)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to add/update workspace member: %v", err)})
	}

	return c.JSON(fiber.Map{
		"workspace_id": activeWSID,
		"user_id":      req.UserID,
		"role":         req.Role,
		"joined_at":    uuid.New().String(), // Simulating timestamp or returning standard string
	})
}

type RemoveWorkspaceMemberRequest struct {
	UserID string `json:"user_id"`
}

func (h *WorkspaceHandler) RemoveWorkspaceMember(c *fiber.Ctx) error {
	activeWSID, ok := c.Locals("workspace_id").(string)
	callerRole, _ := c.Locals("role").(string)

	if !ok || activeWSID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Access denied: No active workspace context"})
	}

	// Only Admin can remove members
	if !auth.IsAuthorized(auth.Role(callerRole), auth.ActionManageMembers) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: Only workspace Admins can remove members"})
	}

	var req RemoveWorkspaceMemberRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	ownerID, err := h.svc.GetWorkspaceOwner(c.UserContext(), activeWSID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to check workspace owner: %v", err)})
	}

	if req.UserID == ownerID {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Cannot remove the workspace owner"})
	}

	err = h.svc.RemoveWorkspaceMember(c.UserContext(), activeWSID, req.UserID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to remove workspace member: %v", err)})
	}

	return c.JSON(fiber.Map{
		"status":  "success",
		"message": fmt.Sprintf("Successfully removed member '%s'", req.UserID),
	})
}

func (h *WorkspaceHandler) DeleteWorkspace(c *fiber.Ctx) error {
	clerkUserID, ok := c.Locals("clerk_user_id").(string)
	if !ok || clerkUserID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Missing x-clerk-user-id header"})
	}

	wsID := c.Params("id")
	if wsID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Workspace ID cannot be empty"})
	}

	if strings.HasPrefix(wsID, "personal-") {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Bad Request: Personal sandboxes cannot be deleted"})
	}

	ownerID, err := h.svc.GetWorkspaceOwner(c.UserContext(), wsID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to check workspace owner: %v", err)})
	}

	if ownerID != clerkUserID {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: Only the workspace owner can delete the workspace"})
	}

	err = h.svc.DeleteWorkspace(c.UserContext(), wsID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to delete workspace: %v", err)})
	}

	return c.JSON(fiber.Map{
		"status":  "success",
		"message": fmt.Sprintf("Successfully deleted workspace '%s'", wsID),
	})
}

func (h *WorkspaceHandler) GetSettings(c *fiber.Ctx) error {
	wsID := c.Params("id")
	if wsID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Workspace ID cannot be empty"})
	}

	clerkUserID, ok := c.Locals("clerk_user_id").(string)
	if !ok || clerkUserID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Missing x-clerk-user-id header"})
	}

	// Verify membership
	members, err := h.svc.ListWorkspaceMembers(c.UserContext(), wsID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to fetch workspace members: %v", err)})
	}

	isMember := false
	for _, member := range members {
		if member.UserID == clerkUserID {
			isMember = true
			break
		}
	}

	if !isMember {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: You are not a member of this workspace"})
	}

	settings, err := h.svc.GetWorkspaceSettings(c.UserContext(), wsID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to retrieve settings: %v", err)})
	}

	opts := protojson.MarshalOptions{
		EmitUnpopulated: true,
		UseProtoNames:   true,
	}
	jsonBytes, err := opts.Marshal(settings)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to marshal settings: %v", err)})
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return c.Send(jsonBytes)
}

func (h *WorkspaceHandler) UpdateSettings(c *fiber.Ctx) error {
	wsID := c.Params("id")
	if wsID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Workspace ID cannot be empty"})
	}

	clerkUserID, ok := c.Locals("clerk_user_id").(string)
	if !ok || clerkUserID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Missing x-clerk-user-id header"})
	}

	// 1. RBAC Validation: Find member's role
	members, err := h.svc.ListWorkspaceMembers(c.UserContext(), wsID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to fetch workspace members: %v", err)})
	}

	callerRole := ""
	for _, member := range members {
		if member.UserID == clerkUserID {
			callerRole = member.Role
			break
		}
	}

	ownerID, err := h.svc.GetWorkspaceOwner(c.UserContext(), wsID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to check owner: %v", err)})
	}

	isAuthorized := auth.IsAuthorized(auth.Role(callerRole), auth.ActionModifySettings) || clerkUserID == ownerID
	if !isAuthorized {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: Only workspace Admins, SREs, or the Owner can modify settings"})
	}

	// 2. Parse request payload using protojson (standard library json fallback)
	reqSettings := &enginev1.WorkspaceSettings{}
	if err := protojson.Unmarshal(c.Body(), reqSettings); err != nil {
		if err := c.BodyParser(reqSettings); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("Invalid JSON schema: %v", err)})
		}
	}

	// 3. Validation limits (Google Production Guardrails)
	if reqSettings.DefaultChunkSize < 100 || reqSettings.DefaultChunkSize > 100000 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Validation Error: Default chunk size must be between 100 and 100,000 rows"})
	}
	if reqSettings.LockTimeoutMs < 100 || reqSettings.LockTimeoutMs > 30000 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Validation Error: Lock timeout must be between 100ms and 30,000ms"})
	}
	if reqSettings.MaxRetries < 0 || reqSettings.MaxRetries > 20 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Validation Error: Deadlock retry limit must be between 0 and 20"})
	}

	// 4. Save updates
	err = h.svc.UpdateWorkspaceSettings(c.UserContext(), wsID, reqSettings, clerkUserID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to save settings: %v", err)})
	}

	opts := protojson.MarshalOptions{
		EmitUnpopulated: true,
		UseProtoNames:   true,
	}
	jsonBytes, err := opts.Marshal(reqSettings)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to marshal settings: %v", err)})
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return c.Send(jsonBytes)
}

func (h *WorkspaceHandler) GetSettingsAuditLogs(c *fiber.Ctx) error {
	wsID := c.Params("id")
	if wsID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Workspace ID cannot be empty"})
	}

	clerkUserID, ok := c.Locals("clerk_user_id").(string)
	if !ok || clerkUserID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Missing x-clerk-user-id header"})
	}

	// Verify membership
	members, err := h.svc.ListWorkspaceMembers(c.UserContext(), wsID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to fetch workspace members: %v", err)})
	}

	isMember := false
	for _, member := range members {
		if member.UserID == clerkUserID {
			isMember = true
			break
		}
	}

	if !isMember {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: You are not a member of this workspace"})
	}

	logs, err := h.svc.GetSettingsAuditLogs(c.UserContext(), wsID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to fetch settings audit logs: %v", err)})
	}

	return c.JSON(logs)
}

func (h *WorkspaceHandler) GetService() *service.WorkspaceService {
	return h.svc
}

type CreateInvitationRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (h *WorkspaceHandler) CreateInvitation(c *fiber.Ctx) error {
	activeWSID, ok := c.Locals("workspace_id").(string)
	callerRole, _ := c.Locals("role").(string)
	clerkUserID, _ := c.Locals("clerk_user_id").(string)

	if !ok || activeWSID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Access denied: No active workspace context"})
	}

	// 1. RBAC Enforce (Only Admin can invite)
	if !auth.IsAuthorized(auth.Role(callerRole), auth.ActionManageMembers) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: Only workspace Admins can invite new members"})
	}

	var req CreateInvitationRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid email address format"})
	}

	if req.Role != "Admin" && req.Role != "SRE" && req.Role != "Developer" && req.Role != "Viewer" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid role. Must be Admin, SRE, Developer, or Viewer"})
	}

	// 2. Generate secure token
	rawToken := uuid.New().String() + "-" + uuid.New().String()
	hashBytes := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hashBytes[:])

	invite := &repository.InvitationRow{
		ID:          fmt.Sprintf("inv-%s", strings.Split(uuid.New().String(), "-")[0]),
		WorkspaceID: activeWSID,
		Email:       req.Email,
		Role:        req.Role,
		TokenHash:   tokenHash,
		InvitedBy:   clerkUserID,
		ExpiresAt:   time.Now().Add(7 * 24 * time.Hour),
	}

	err := h.svc.CreateInvitation(c.UserContext(), invite)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to generate invitation: %v", err)})
	}

	clerkErr := sendClerkInvitation(invite.Email)
	clerkSent := true
	if clerkErr != nil {
		log.Printf("Warning: failed to dispatch Clerk invitation: %v", clerkErr)
		clerkSent = false
	}

	return c.JSON(fiber.Map{
		"status":     "success",
		"invite_id":  invite.ID,
		"token":      rawToken,
		"role":       invite.Role,
		"expires_at": invite.ExpiresAt,
		"clerk_sent": clerkSent,
	})
}

func sendClerkInvitation(email string) error {
	clerkSecretKey := os.Getenv("CLERK_SECRET_KEY")
	if clerkSecretKey == "" {
		log.Println("Warning: CLERK_SECRET_KEY not set. Skipping Clerk invitation email dispatch.")
		return nil
	}

	payload := map[string]interface{}{
		"email_address": email,
		"ignore_existing": true,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", "https://api.clerk.com/v1/invitations", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+clerkSecretKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("clerk api returned status %d: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("Successfully triggered Clerk email invitation for %s", email)
	return nil
}

type AcceptInvitationRequest struct {
	Token string `json:"token"`
	Email string `json:"email"`
}

func (h *WorkspaceHandler) AcceptInvitation(c *fiber.Ctx) error {
	clerkUserID, ok := c.Locals("clerk_user_id").(string)
	if !ok || clerkUserID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Missing authenticated session context"})
	}

	var req AcceptInvitationRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Token is required"})
	}

	// 1. Hash incoming token
	hashBytes := sha256.Sum256([]byte(req.Token))
	tokenHash := hex.EncodeToString(hashBytes[:])

	// 2. Lookup invitation
	invite, err := h.svc.GetInvitationByTokenHash(c.UserContext(), tokenHash)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Invalid or expired invitation token"})
	}

	if invite.Status != "pending" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("Invitation cannot be claimed. Status is %s", invite.Status)})
	}

	if time.Now().After(invite.ExpiresAt) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invitation has expired"})
	}

	// Enforce email alignment constraint (Ilya)
	if req.Email != "" && strings.ToLower(req.Email) != strings.ToLower(invite.Email) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: Logged-in email claim does not match invitation recipient"})
	}

	// Human-only check
	if clerkUserID == "m2m-agent" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: Machine accounts cannot claim invitations"})
	}

	// 3. Complete onboarding transaction
	err = h.svc.AcceptInvitation(c.UserContext(), invite.ID, invite.WorkspaceID, clerkUserID, invite.Role)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to accept invitation: %v", err)})
	}

	return c.JSON(fiber.Map{
		"status":       "success",
		"workspace_id": invite.WorkspaceID,
		"role":         invite.Role,
		"message":      "Successfully joined workspace!",
	})
}

func (h *WorkspaceHandler) RevokeInvitation(c *fiber.Ctx) error {
	activeWSID, ok := c.Locals("workspace_id").(string)
	callerRole, _ := c.Locals("role").(string)

	if !ok || activeWSID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Access denied: No active workspace context"})
	}

	if !auth.IsAuthorized(auth.Role(callerRole), auth.ActionManageMembers) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Forbidden: Only workspace Admins can revoke invitations"})
	}

	id := c.Params("id")
	if id == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invitation ID is required"})
	}

	err := h.svc.RevokeInvitation(c.UserContext(), id)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to revoke invitation: %v", err)})
	}

	return c.JSON(fiber.Map{
		"status":  "success",
		"message": "Invitation successfully revoked",
	})
}

func (h *WorkspaceHandler) ListInvitations(c *fiber.Ctx) error {
	activeWSID, ok := c.Locals("workspace_id").(string)
	if !ok || activeWSID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Access denied: No active workspace context"})
	}

	invites, err := h.svc.ListInvitations(c.UserContext(), activeWSID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("Failed to list invitations: %v", err)})
	}

	return c.JSON(invites)
}


