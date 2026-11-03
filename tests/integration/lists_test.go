package integration

import "testing"

// TestListEndpointsForAllRoles calls every list endpoint (with filters and
// sorting) as a super administrator, an assistant (view-all permissions) and
// a student, guarding against query-construction bugs that only show up for
// one permission set.
func TestListEndpointsForAllRoles(t *testing.T) {
	e := setup(t, envOpts{GPU: true})
	admin := e.AdminClient()
	assistant := e.CreateUser(admin, "asisten", []string{"assistant"}, "")
	student := e.CreateUser(admin, "mhs1", []string{"student"}, "")
	lecturer := e.CreateUser(admin, "dosen", []string{"lecturer"}, "")
	student.Do("POST", "/api/v1/workspaces", map[string]any{"name": "ws", "template_key": "ubuntu-dev"}).Expect(t, 202, "ws")
	student.Do("POST", "/api/v1/projects", map[string]any{"name": "proj"}).Expect(t, 201, "proj")

	paths := []string{
		"/api/v1/workspaces", "/api/v1/workspaces?q=ws&status=requested&sort=-name&page=1&per_page=5", "/api/v1/workspaces?mine=true",
		"/api/v1/containers", "/api/v1/containers?status=created&mine=true&sort=name&q=main",
		"/api/v1/projects", "/api/v1/projects?mine=true&q=proj&sort=-created_at&status=active",
		"/api/v1/databases", "/api/v1/notifications?unread=true", "/api/v1/notifications?type=system",
		"/api/v1/templates", "/api/v1/images", "/api/v1/quotas", "/api/v1/gpu/devices", "/api/v1/gpu/requests",
		"/api/v1/gpu/allocations?status=active", "/api/v1/terminals", "/api/v1/announcements", "/api/v1/dashboard",
		"/api/v1/me/usage", "/api/v1/auth/sessions",
	}
	adminOnly := []string{
		"/api/v1/users", "/api/v1/users?q=mhs&status=active&role=student&sort=-created_at", "/api/v1/roles", "/api/v1/permissions",
		"/api/v1/admin/audit-logs", "/api/v1/admin/audit-logs?action=user.*&outcome=success&sort=action",
		"/api/v1/admin/settings", "/api/v1/admin/backups", "/api/v1/admin/agent/status", "/api/v1/monitoring/host",
		"/api/v1/monitoring/history?scope=host&range=6h", "/api/v1/monitoring/services", "/api/v1/monitoring/alerts?status=firing",
		"/api/v1/monitoring/jobs?status=dead", "/api/v1/monitoring/gpu", "/api/v1/templates?all=true", "/api/v1/announcements?all=true",
	}
	for _, c := range []*Client{admin, assistant, student, lecturer} {
		for _, p := range paths {
			c.Do("GET", p, nil).Expect(t, 200, p)
		}
	}
	for _, p := range adminOnly {
		admin.Do("GET", p, nil).Expect(t, 200, p)
	}
	assistant.Do("GET", "/api/v1/users", nil).Expect(t, 200, "assistant users")
	assistant.Do("GET", "/api/v1/monitoring/services", nil).Expect(t, 200, "assistant services")
	student.Do("GET", "/api/v1/users", nil).Expect(t, 403, "student users")
	// View-all users see other users' workspaces; students only their own.
	if n := len(assistant.Do("GET", "/api/v1/workspaces", nil).List()); n != 1 {
		t.Fatalf("assistant should see 1 workspace, got %d", n)
	}
	if n := len(lecturer.Do("GET", "/api/v1/workspaces", nil).List()); n != 0 {
		t.Fatalf("lecturer should see 0 workspaces, got %d", n)
	}
}

func TestProjectsWithSameNameGetUniqueSlugs(t *testing.T) {
	e := setup(t, envOpts{})
	admin := e.AdminClient()
	a := e.CreateUser(admin, "mhsa", []string{"student"}, "")
	b := e.CreateUser(admin, "mhsb", []string{"student"}, "")
	s1 := str(a.Do("POST", "/api/v1/projects", map[string]any{"name": "Praktikum Jaringan"}).Expect(t, 201, "a").Data(), "slug")
	s2 := str(b.Do("POST", "/api/v1/projects", map[string]any{"name": "Praktikum Jaringan"}).Expect(t, 201, "b").Data(), "slug")
	if s1 != "praktikum-jaringan" || s2 == s1 {
		t.Fatalf("unexpected slugs %q %q", s1, s2)
	}
	b.Do("POST", "/api/v1/projects", map[string]any{"name": "X", "slug": "praktikum-jaringan"}).Expect(t, 422, "explicit taken slug")
}
