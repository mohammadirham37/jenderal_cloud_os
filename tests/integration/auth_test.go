package integration

import (
	"context"
	"testing"
)

func TestAuthMFAAndRBAC(t *testing.T) {
	e := setup(t, envOpts{})
	e.CreateAdmin("root", "Root-Passw0rd!x")
	admin := e.NewClient()

	// Wrong password and unknown users get the same response.
	r := admin.Do("POST", "/api/v1/auth/login", map[string]string{"username": "root", "password": "nope"})
	r.Expect(t, 401, "wrong password")
	if r.ErrCode() != "invalid_credentials" {
		t.Fatalf("unexpected code %s", r.ErrCode())
	}
	admin.Do("POST", "/api/v1/auth/login", map[string]string{"username": "ghost", "password": "nope"}).Expect(t, 401, "unknown user")

	// Admin must enroll MFA before anything else.
	me := admin.Login("root", "Root-Passw0rd!x").Expect(t, 200, "admin login").Data()
	if me["mfa_setup_required"] != true || me["mfa_verified"] != false {
		t.Fatalf("expected MFA setup to be required: %v", me)
	}
	if r := admin.Do("GET", "/api/v1/users", nil); r.Status != 403 || r.ErrCode() != "mfa_required" {
		t.Fatalf("expected mfa_required before enrollment, got %d %s", r.Status, r.Raw)
	}
	admin.EnrollMFA()
	me = admin.Do("GET", "/api/v1/auth/me", nil).Expect(t, 200, "me").Data()
	if me["mfa_verified"] != true || me["mfa_enrolled"] != true {
		t.Fatalf("expected verified MFA session: %v", me)
	}

	// CSRF: missing token is rejected; wrong origin is rejected.
	csrf := admin.CSRF
	admin.CSRF = ""
	if r := admin.Do("POST", "/api/v1/projects", map[string]string{"name": "x"}); r.Status != 403 || r.ErrCode() != "csrf_failed" {
		t.Fatalf("missing CSRF token must be rejected: %d %s", r.Status, r.Raw)
	}
	admin.CSRF = csrf
	if r := admin.Do("POST", "/api/v1/projects", map[string]string{"name": "x"}, "Origin", "http://evil.example"); r.Status != 403 {
		t.Fatalf("cross-origin request must be rejected: %d", r.Status)
	}

	// A new login (new session) requires an MFA code.
	admin2 := e.NewClient()
	admin2.MFASecret = admin.MFASecret
	admin2.LastStep = admin.LastStep
	admin2.Login("root", "Root-Passw0rd!x").Expect(t, 200, "second admin login")
	admin2.Do("GET", "/api/v1/users", nil).Expect(t, 200, "admin2 list users")

	// Create users; temporary password forces a password change.
	cr := admin.Do("POST", "/api/v1/users", map[string]any{"username": "mhs1", "email": "mhs1@itk.test", "full_name": "Mahasiswa Satu", "roles": []string{"student"}}).
		Expect(t, 201, "create student")
	temp := str(cr.Data(), "temporary_password")
	if len(temp) < 12 {
		t.Fatal("temporary password missing")
	}
	student := e.NewClient()
	student.Login("mhs1", temp).Expect(t, 200, "student login")
	if r := student.Do("GET", "/api/v1/dashboard", nil); r.Status != 403 || r.ErrCode() != "password_change_required" {
		t.Fatalf("expected password change requirement: %d %s", r.Status, r.Raw)
	}
	student.Do("POST", "/api/v1/auth/change-password", map[string]string{"current_password": temp, "new_password": "weak"}).Expect(t, 422, "weak password")
	student.Do("POST", "/api/v1/auth/change-password", map[string]string{"current_password": temp, "new_password": "Mahasiswa-Pass1"}).Expect(t, 204, "change password")
	student.Do("GET", "/api/v1/dashboard", nil).Expect(t, 200, "dashboard")

	// RBAC: students cannot manage users or read audit logs/settings.
	student.Do("GET", "/api/v1/users", nil).Expect(t, 403, "student list users")
	student.Do("GET", "/api/v1/admin/audit-logs", nil).Expect(t, 403, "student audit")
	student.Do("GET", "/api/v1/admin/settings", nil).Expect(t, 403, "student settings")
	student.Do("POST", "/api/v1/users", map[string]any{"username": "x1x", "email": "x@x.io", "full_name": "x", "roles": []string{"student"}}).Expect(t, 403, "student create user")
	student.Do("POST", "/api/v1/admin/host/emergency-stop", map[string]any{"confirm": "STOP ALL WORKLOADS", "reason": "trying to break things"}).Expect(t, 403, "student emergency stop")

	// Privilege escalation: an admin (rank 80) cannot grant super_admin.
	cr = admin.Do("POST", "/api/v1/users", map[string]any{"username": "adm2", "email": "adm2@itk.test", "full_name": "Admin Dua", "roles": []string{"admin"}}).Expect(t, 201, "create admin")
	adm2 := e.NewClient()
	adm2.Login("adm2", str(cr.Data(), "temporary_password")).Expect(t, 200, "adm2 login")
	adm2.EnrollMFA()
	adm2.Do("POST", "/api/v1/auth/change-password", map[string]string{"current_password": str(cr.Data(), "temporary_password"), "new_password": "Admin-Dua-Pass1"}).Expect(t, 204, "adm2 password")
	adm2.Do("POST", "/api/v1/users", map[string]any{"username": "sneaky", "email": "s@itk.test", "full_name": "S", "roles": []string{"super_admin"}}).Expect(t, 403, "grant super_admin")
	adm2.Do("POST", "/api/v1/users", map[string]any{"username": "sneaky", "email": "s@itk.test", "full_name": "S", "roles": []string{"admin"}}).Expect(t, 403, "grant equal rank")
	rootID := str(admin.Do("GET", "/api/v1/auth/me", nil).Data(), "id")
	adm2.Do("POST", "/api/v1/users/"+rootID+"/suspend", map[string]string{"reason": "takeover"}).Expect(t, 403, "suspend higher rank")
	adm2.Do("PATCH", "/api/v1/users/"+rootID, map[string]any{"full_name": "Hacked"}).Expect(t, 403, "edit higher rank")

	// Suspension revokes sessions immediately and blocks login.
	studentID := str(student.Do("GET", "/api/v1/auth/me", nil).Data(), "id")
	admin.Do("POST", "/api/v1/users/"+studentID+"/suspend", map[string]string{"reason": "policy violation"}).Expect(t, 200, "suspend")
	student.Do("GET", "/api/v1/dashboard", nil).Expect(t, 401, "suspended session")
	student.Do("POST", "/api/v1/auth/login", map[string]string{"username": "mhs1", "password": "Mahasiswa-Pass1"}).Expect(t, 403, "suspended login")
	admin.Do("POST", "/api/v1/users/"+studentID+"/activate", map[string]string{}).Expect(t, 200, "activate")
	student.Login("mhs1", "Mahasiswa-Pass1").Expect(t, 200, "login after activation")

	// Password reset link (admin-issued) works once and revokes sessions.
	rr := admin.Do("POST", "/api/v1/users/"+studentID+"/reset-password", nil).Expect(t, 200, "reset link").Data()
	link := str(rr, "reset_link")
	token := link[len(e.Base+"/reset-password#token="):]
	anon := e.NewClient()
	anon.Do("POST", "/api/v1/auth/reset-password", map[string]string{"token": token, "password": "Brand-New-Pass1"}).Expect(t, 204, "reset password")
	anon.Do("POST", "/api/v1/auth/reset-password", map[string]string{"token": token, "password": "Brand-New-Pass2"}).Expect(t, 400, "reuse token")
	student.Do("GET", "/api/v1/dashboard", nil).Expect(t, 401, "session revoked after reset")

	// Forgot password never reveals whether an account exists.
	anon.Do("POST", "/api/v1/auth/forgot-password", map[string]string{"email": "nobody@itk.test"}).Expect(t, 202, "forgot unknown")
	anon.Do("POST", "/api/v1/auth/forgot-password", map[string]string{"email": "mhs1@itk.test"}).Expect(t, 202, "forgot known")

	// Account lockout after repeated failures.
	for i := 0; i < 5; i++ {
		anon.Do("POST", "/api/v1/auth/login", map[string]string{"username": "mhs1", "password": "wrong-password"})
	}
	anon.Do("POST", "/api/v1/auth/login", map[string]string{"username": "mhs1", "password": "Brand-New-Pass1"}).Expect(t, 423, "locked account")

	// Audit trail recorded the sensitive actions; it cannot be modified.
	logs := admin.Do("GET", "/api/v1/admin/audit-logs?per_page=100", nil).Expect(t, 200, "audit logs").List()
	seen := map[string]bool{}
	for _, l := range logs {
		seen[str(l.(map[string]any), "action")] = true
	}
	for _, a := range []string{"user.create", "user.suspend", "user.activate", "user.reset_password", "auth.mfa_enable", "auth.login", "auth.password_reset"} {
		if !seen[a] {
			t.Errorf("audit log missing %s", a)
		}
	}
	if _, err := e.Deps.Pool.Exec(context.Background(), `DELETE FROM audit_logs`); err == nil {
		t.Fatal("audit log must be append-only")
	}

	// Logout invalidates the session.
	admin2.Do("POST", "/api/v1/auth/logout", nil).Expect(t, 204, "logout")
	admin2.Do("GET", "/api/v1/auth/me", nil).Expect(t, 401, "after logout")
}

func TestLoginRateLimit(t *testing.T) {
	e := setup(t, envOpts{})
	c := e.NewClient()
	limited := false
	for i := 0; i < 12; i++ {
		r := c.Do("POST", "/api/v1/auth/login", map[string]string{"username": "someone", "password": "x"})
		if r.Status == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("expected login rate limiting per username")
	}
}
