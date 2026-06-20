package auth

import "testing"

func TestIsAuthorized(t *testing.T) {
	tests := []struct {
		desc     string
		role     Role
		action   Action
		expected bool
	}{
		// Admin
		{"Admin can modify settings", RoleAdmin, ActionModifySettings, true},
		{"Admin can manage members", RoleAdmin, ActionManageMembers, true},
		// SRE
		{"SRE can modify settings", RoleSRE, ActionModifySettings, true},
		{"SRE can approve plan", RoleSRE, ActionApprovePlan, true},
		{"SRE cannot manage members", RoleSRE, ActionManageMembers, false},
		// Developer
		{"Developer can compile schema", RoleDeveloper, ActionCompileSchema, true},
		{"Developer cannot modify settings", RoleDeveloper, ActionModifySettings, false},
		// Viewer
		{"Viewer can read logs", RoleViewer, ActionReadLogs, true},
		{"Viewer cannot compile schema", RoleViewer, ActionCompileSchema, false},
		// Unknown Role
		{"Unknown role cannot do anything", Role("Unknown"), ActionReadLogs, false},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			res := IsAuthorized(tc.role, tc.action)
			if res != tc.expected {
				t.Errorf("Expected %v, got %v", tc.expected, res)
			}
		})
	}
}
