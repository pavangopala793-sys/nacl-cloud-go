package auth

type Role string
type Action string

const (
	RoleAdmin     Role = "Admin"
	RoleSRE       Role = "SRE"
	RoleDeveloper Role = "Developer"
	RoleViewer    Role = "Viewer"
)

const (
	ActionModifySettings Action = "modify_settings"
	ActionApprovePlan    Action = "approve_plan"
	ActionDispatchQueue  Action = "dispatch_queue"
	ActionCompileSchema  Action = "compile_schema"
	ActionReadLogs       Action = "read_logs"
	ActionManageMembers  Action = "manage_members"
)

// IsAuthorized checks if a role is authorized to perform an action.
func IsAuthorized(role Role, action Action) bool {
	switch role {
	case RoleAdmin:
		// Admins can do everything
		return true
	case RoleSRE:
		// SREs manage settings, approve/reject plans, and dispatch queues, but cannot manage team members
		return action == ActionModifySettings ||
			action == ActionApprovePlan ||
			action == ActionDispatchQueue ||
			action == ActionCompileSchema ||
			action == ActionReadLogs
	case RoleDeveloper:
		// Developers can compile/lint schemas and read logs
		return action == ActionCompileSchema ||
			action == ActionReadLogs
	case RoleViewer:
		// Viewers are read-only
		return action == ActionReadLogs
	default:
		return false
	}
}
