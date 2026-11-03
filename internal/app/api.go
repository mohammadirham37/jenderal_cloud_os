package app

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/itk-cloud/itk-cloud/internal/modules/agent"
	"github.com/itk-cloud/itk-cloud/internal/modules/appproxy"
	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/modules/auth"
	"github.com/itk-cloud/itk-cloud/internal/modules/backups"
	"github.com/itk-cloud/itk-cloud/internal/modules/containers"
	"github.com/itk-cloud/itk-cloud/internal/modules/dashboard"
	"github.com/itk-cloud/itk-cloud/internal/modules/databases"
	"github.com/itk-cloud/itk-cloud/internal/modules/files"
	"github.com/itk-cloud/itk-cloud/internal/modules/gpu"
	"github.com/itk-cloud/itk-cloud/internal/modules/hostops"
	"github.com/itk-cloud/itk-cloud/internal/modules/images"
	"github.com/itk-cloud/itk-cloud/internal/modules/monitoring"
	"github.com/itk-cloud/itk-cloud/internal/modules/notifications"
	"github.com/itk-cloud/itk-cloud/internal/modules/projects"
	"github.com/itk-cloud/itk-cloud/internal/modules/rbac"
	"github.com/itk-cloud/itk-cloud/internal/modules/resources"
	"github.com/itk-cloud/itk-cloud/internal/modules/settings"
	"github.com/itk-cloud/itk-cloud/internal/modules/terminals"
	"github.com/itk-cloud/itk-cloud/internal/modules/users"
	"github.com/itk-cloud/itk-cloud/internal/modules/workspaces"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/internal/platform/idempotency"
	"github.com/itk-cloud/itk-cloud/internal/platform/redisx"
)

// Services groups module services shared by the API and the worker.
type Services struct {
	Agent      *agent.Agent
	Resources  *resources.Service
	Workspaces *workspaces.Service
	Databases  *databases.Service
	GPU        *gpu.Service
	Monitoring *monitoring.Service
	Backups    *backups.Service
}

// NewServices builds module services.
func NewServices(d *Deps) *Services {
	ag := &agent.Agent{Client: d.Agent, Pool: d.Pool}
	res := &resources.Service{Pool: d.Pool, Settings: d.Settings}
	wsSvc := &workspaces.Service{Pool: d.Pool, Cfg: d.Cfg, Settings: d.Settings, Resources: res, Agent: ag}
	dbSvc := &databases.Service{Pool: d.Pool, Cfg: d.Cfg, Box: d.Box, Resources: res}
	return &Services{
		Agent: ag, Resources: res, Workspaces: wsSvc, Databases: dbSvc,
		GPU:        &gpu.Service{Pool: d.Pool, Cfg: d.Cfg, Settings: d.Settings, Resources: res, Agent: ag},
		Monitoring: &monitoring.Service{Pool: d.Pool, Redis: d.Redis, Cfg: d.Cfg, Settings: d.Settings, Agent: ag},
		Backups:    &backups.Service{Pool: d.Pool, Cfg: d.Cfg, Settings: d.Settings, Agent: ag, Box: d.Box, DB: dbSvc},
	}
}

// streaming reports routes that manage their own deadlines/body limits.
func streaming(r *http.Request) bool {
	p := r.URL.Path
	return strings.HasSuffix(p, "/ws") || strings.HasSuffix(p, "/files/upload") || strings.HasSuffix(p, "/files/download") ||
		strings.Contains(p, "/proxy/") || strings.HasSuffix(p, "/pull") || strings.Contains(p, "/emergency-stop")
}

// NewAPIHandler builds the full HTTP handler.
func NewAPIHandler(d *Deps, s *Services) http.Handler {
	sessions := &auth.Sessions{Pool: d.Pool, Settings: d.Settings, TTL: d.Cfg.SessionTTL, IdleTTL: d.Cfg.SessionIdleTTL,
		Secure: d.Cfg.CookieSecure, BaseOrigin: baseOrigin(d.Cfg.BaseURL)}
	limiter := redisx.NewLimiter(d.Redis, "itk:rl")
	roles := &rbac.Repo{Pool: d.Pool}

	authH := &auth.Handler{Sessions: sessions, Settings: d.Settings, Limiter: limiter, Box: d.Box, Mail: d.Mail, BaseURL: d.Cfg.BaseURL, Issuer: "Jenderal Cloud OS"}
	usersH := &users.Handler{Pool: d.Pool, Roles: roles, Sessions: sessions, Mail: d.Mail, BaseURL: d.Cfg.BaseURL, OnSuspend: workspaces.SuspendAllForUser}
	rbacH := &rbac.Handler{Repo: roles}
	auditH := &audit.Handler{Pool: d.Pool}
	settingsH := &settings.Handler{Store: d.Settings}
	agentH := &agent.Handler{Agent: s.Agent}
	resH := &resources.Handler{Svc: s.Resources}
	wsH := &workspaces.Handler{Svc: s.Workspaces}
	ctrH := &containers.Handler{Svc: s.Workspaces}
	imgH := &images.Handler{Pool: d.Pool, Agent: s.Agent}
	termH := &terminals.Handler{Svc: s.Workspaces, Cfg: d.Cfg}
	filesH := &files.Handler{Svc: s.Workspaces, Cfg: d.Cfg}
	proxyH := &appproxy.Handler{Svc: s.Workspaces, Cfg: d.Cfg}
	projH := &projects.Handler{Pool: d.Pool, Resources: s.Resources}
	dbH := &databases.Handler{Svc: s.Databases}
	gpuH := &gpu.Handler{Svc: s.GPU}
	monH := &monitoring.Handler{Svc: s.Monitoring}
	notifH := &notifications.Handler{Pool: d.Pool}
	backupH := &backups.Handler{Svc: s.Backups}
	hostH := &hostops.Handler{Pool: d.Pool, Agent: s.Agent}
	dashH := &dashboard.Handler{Pool: d.Pool, Cfg: d.Cfg, Resources: s.Resources, Version: Version,
		PlatformName: func() string { return d.Settings.String(context.Background(), "platform.name") }}

	mux := http.NewServeMux()
	public := func(pattern string, fn httpx.HandlerFunc) { mux.Handle(pattern, httpx.Handle(fn)) }
	private := func(pattern string, fn httpx.HandlerFunc) { mux.Handle(pattern, auth.RequireAuth(httpx.Handle(fn))) }
	idem := func(fn httpx.HandlerFunc) httpx.HandlerFunc { return idempotency.Wrap(d.Pool, fn) }

	// Health (no auth; no sensitive details).
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		checks := map[string]string{"database": "ok", "redis": "ok", "migrations": "ok"}
		status := http.StatusOK
		if err := d.Pool.Ping(ctx); err != nil {
			checks["database"], status = "unavailable", http.StatusServiceUnavailable
		} else if cur, latest, err := db.MigrationStatus(ctx, d.Pool); err != nil || cur != latest {
			checks["migrations"], status = "pending", http.StatusServiceUnavailable
		}
		if err := d.Redis.Ping(ctx).Err(); err != nil {
			checks["redis"], status = "unavailable", http.StatusServiceUnavailable
		}
		httpx.WriteJSON(w, status, map[string]any{"status": map[bool]string{true: "ready", false: "not_ready"}[status == http.StatusOK], "checks": checks, "version": Version})
	})

	const v1 = "/api/v1"
	public("GET "+v1+"/config", dashH.PublicConfig)

	// Authentication.
	public("POST "+v1+"/auth/login", authH.Login)
	public("POST "+v1+"/auth/forgot-password", authH.ForgotPassword)
	public("POST "+v1+"/auth/reset-password", authH.ResetPassword)
	private("POST "+v1+"/auth/logout", authH.Logout)
	private("GET "+v1+"/auth/me", authH.Me)
	private("POST "+v1+"/auth/change-password", authH.ChangePassword)
	private("POST "+v1+"/auth/mfa/setup", authH.MFASetup)
	private("POST "+v1+"/auth/mfa/enable", authH.MFAEnable)
	private("POST "+v1+"/auth/mfa/verify", authH.MFAVerify)
	private("POST "+v1+"/auth/mfa/disable", authH.MFADisable)
	private("GET "+v1+"/auth/sessions", authH.ListSessions)
	private("DELETE "+v1+"/auth/sessions/{id}", authH.RevokeSession)

	// Dashboard & self.
	private("GET "+v1+"/dashboard", dashH.Summary)
	private("GET "+v1+"/me/usage", resH.MyUsage)

	// Users, roles, quotas.
	private("GET "+v1+"/users", usersH.List)
	private("POST "+v1+"/users", usersH.Create)
	private("GET "+v1+"/users/lookup", usersH.Lookup)
	private("GET "+v1+"/users/{id}", usersH.GetOne)
	private("PATCH "+v1+"/users/{id}", usersH.Update)
	private("POST "+v1+"/users/{id}/suspend", usersH.Suspend)
	private("POST "+v1+"/users/{id}/activate", usersH.Activate)
	private("POST "+v1+"/users/{id}/reset-password", usersH.ResetPassword)
	private("POST "+v1+"/users/{id}/reset-mfa", usersH.ResetMFA)
	private("GET "+v1+"/users/{id}/usage", resH.UserUsage)
	private("GET "+v1+"/roles", rbacH.ListRoles)
	private("POST "+v1+"/roles", rbacH.CreateRole)
	private("PATCH "+v1+"/roles/{id}", rbacH.UpdateRole)
	private("DELETE "+v1+"/roles/{id}", rbacH.DeleteRole)
	private("GET "+v1+"/permissions", rbacH.ListPermissions)
	private("GET "+v1+"/quotas", resH.ListQuotas)
	private("POST "+v1+"/quotas", resH.CreateQuota)
	private("PATCH "+v1+"/quotas/{id}", resH.UpdateQuota)
	private("DELETE "+v1+"/quotas/{id}", resH.DeleteQuota)

	// Projects.
	private("GET "+v1+"/projects", projH.List)
	private("POST "+v1+"/projects", projH.Create)
	private("GET "+v1+"/projects/{id}", projH.GetOne)
	private("PATCH "+v1+"/projects/{id}", projH.Update)
	private("DELETE "+v1+"/projects/{id}", projH.Delete)
	private("POST "+v1+"/projects/{id}/members", projH.AddMember)
	private("DELETE "+v1+"/projects/{id}/members/{userId}", projH.RemoveMember)
	private("GET "+v1+"/projects/{id}/activity", projH.Activity)

	// Templates & images.
	private("GET "+v1+"/templates", wsH.ListTemplates)
	private("POST "+v1+"/templates", wsH.CreateTemplate)
	private("GET "+v1+"/templates/{id}", wsH.GetTemplateHandler)
	private("PATCH "+v1+"/templates/{id}", wsH.UpdateTemplate)
	private("GET "+v1+"/images", imgH.List)
	private("POST "+v1+"/images", imgH.Create)
	private("PATCH "+v1+"/images/{id}", imgH.Update)
	private("POST "+v1+"/images/{id}/pull", imgH.Pull)

	// Workspaces.
	private("GET "+v1+"/workspaces", wsH.List)
	private("POST "+v1+"/workspaces", idem(wsH.Create))
	private("GET "+v1+"/workspaces/{id}", wsH.GetOne)
	private("PATCH "+v1+"/workspaces/{id}", wsH.Update)
	private("DELETE "+v1+"/workspaces/{id}", wsH.Delete)
	for _, a := range []string{"start", "stop", "restart", "suspend", "retry"} {
		private("POST "+v1+"/workspaces/{id}/"+a, wsH.Action(a))
	}
	private("GET "+v1+"/workspaces/{id}/jobs", wsH.Jobs)
	private("POST "+v1+"/workspaces/{id}/members", wsH.AddMember)
	private("DELETE "+v1+"/workspaces/{id}/members/{userId}", wsH.RemoveMember)

	// Files.
	private("GET "+v1+"/workspaces/{id}/files", filesH.List)
	private("DELETE "+v1+"/workspaces/{id}/files", filesH.Delete)
	private("GET "+v1+"/workspaces/{id}/files/stat", filesH.Stat)
	private("GET "+v1+"/workspaces/{id}/files/content", filesH.ReadContent)
	private("PUT "+v1+"/workspaces/{id}/files/content", filesH.WriteContent)
	private("POST "+v1+"/workspaces/{id}/files/upload", filesH.Upload)
	private("GET "+v1+"/workspaces/{id}/files/download", filesH.Download)
	private("POST "+v1+"/workspaces/{id}/files/mkdir", filesH.Mkdir)
	private("PATCH "+v1+"/workspaces/{id}/files/rename", filesH.Rename)

	// Terminals.
	private("POST "+v1+"/workspaces/{id}/terminals", termH.Create)
	private("GET "+v1+"/terminals", termH.List)
	private("DELETE "+v1+"/terminals/{id}", termH.Close)
	private("GET "+v1+"/terminals/{id}/ws", termH.WebSocket)

	// Workspace app proxy (served only on APP_PROXY_ORIGIN).
	private(v1+"/workspaces/{id}/proxy/{port}/{rest...}", proxyH.ServeHTTP)
	private(v1+"/workspaces/{id}/proxy/{port}", func(w http.ResponseWriter, r *http.Request) error {
		http.Redirect(w, r, r.URL.Path+"/", http.StatusFound)
		return nil
	})

	// Containers.
	private("GET "+v1+"/containers", ctrH.List)
	private("POST "+v1+"/containers", idem(ctrH.Create))
	private("GET "+v1+"/containers/{id}", ctrH.GetOne)
	private("DELETE "+v1+"/containers/{id}", ctrH.Delete)
	private("GET "+v1+"/containers/{id}/logs", ctrH.Logs)
	for _, a := range []string{"start", "stop", "restart"} {
		private("POST "+v1+"/containers/{id}/"+a, ctrH.Action(a))
	}

	// Databases.
	private("GET "+v1+"/databases", dbH.List)
	private("POST "+v1+"/databases", idem(dbH.Create))
	private("GET "+v1+"/databases/{id}", dbH.GetOne)
	private("DELETE "+v1+"/databases/{id}", dbH.Delete)
	private("POST "+v1+"/databases/{id}/credentials/reset", dbH.ResetCredentials)

	// GPU.
	private("GET "+v1+"/gpu/devices", gpuH.Devices)
	private("PATCH "+v1+"/gpu/devices/{id}", gpuH.UpdateDevice)
	private("GET "+v1+"/gpu/requests", gpuH.Requests)
	private("POST "+v1+"/gpu/requests", gpuH.CreateRequest)
	private("POST "+v1+"/gpu/requests/{id}/approve", gpuH.Approve)
	private("POST "+v1+"/gpu/requests/{id}/reject", gpuH.Reject)
	private("POST "+v1+"/gpu/requests/{id}/cancel", gpuH.CancelRequest)
	private("GET "+v1+"/gpu/allocations", gpuH.Allocations)
	private("POST "+v1+"/gpu/allocations/{id}/release", gpuH.Release)

	// Monitoring.
	private("GET "+v1+"/monitoring/host", monH.Host)
	private("GET "+v1+"/monitoring/workspaces/{id}", monH.Workspace)
	private("GET "+v1+"/monitoring/gpu", monH.GPU)
	private("GET "+v1+"/monitoring/history", monH.History)
	private("GET "+v1+"/monitoring/alerts", monH.Alerts)
	private("POST "+v1+"/monitoring/alerts/{id}/ack", monH.AckAlert)
	private("GET "+v1+"/monitoring/services", monH.Services)
	private("GET "+v1+"/monitoring/jobs", monH.FailedJobs)
	private("POST "+v1+"/monitoring/jobs/{id}/retry", monH.RetryJob)

	// Notifications & announcements.
	private("GET "+v1+"/notifications", notifH.List)
	private("GET "+v1+"/notifications/unread-count", notifH.UnreadCount)
	private("POST "+v1+"/notifications/read-all", notifH.MarkAllRead)
	private("POST "+v1+"/notifications/{id}/read", notifH.MarkRead)
	private("GET "+v1+"/announcements", notifH.Announcements)
	private("POST "+v1+"/announcements", notifH.CreateAnnouncement)
	private("DELETE "+v1+"/announcements/{id}", notifH.DeleteAnnouncement)

	// Administration.
	private("GET "+v1+"/admin/audit-logs", auditH.List)
	private("GET "+v1+"/admin/agent/status", agentH.Status)
	private("GET "+v1+"/admin/settings", settingsH.Get)
	private("PATCH "+v1+"/admin/settings", settingsH.Update)
	private("GET "+v1+"/admin/backups", backupH.List)
	private("POST "+v1+"/admin/backups", backupH.Create)
	private("POST "+v1+"/admin/backups/{id}/restore", backupH.Restore)
	private("POST "+v1+"/admin/host/emergency-stop", hostH.EmergencyStop)

	// Unknown API routes return JSON 404.
	public("/api/", func(w http.ResponseWriter, r *http.Request) error { return httpx.NotFound("Endpoint") })

	return httpx.Chain(mux,
		proxyOriginGuard(d.Cfg.AppProxyOrigin),
		httpx.RequestIDMiddleware,
		httpx.RealIP,
		httpx.Logger(d.Log),
		httpx.Recover,
		securityHeaders,
		httpx.BodyLimit(httpx.MaxJSONBody, streaming),
		httpx.Timeout(60*time.Second, streaming),
		sessions.Authenticate,
		sessions.CSRF,
	)
}

// proxyOriginGuard makes the app-proxy origin serve nothing but workspace
// app proxy routes, so scripts of workspace apps (same origin as the proxy)
// can never reach the console API there — independent of reverse-proxy
// configuration.
func proxyOriginGuard(origin string) httpx.Middleware {
	host := ""
	if u, err := url.Parse(origin); err == nil {
		host = u.Host
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if host != "" && strings.EqualFold(r.Host, host) &&
				!(strings.HasPrefix(r.URL.Path, "/api/v1/workspaces/") && strings.Contains(r.URL.Path, "/proxy/")) {
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// securityHeaders applies strict headers except on proxied workspace apps
// (which are served on their own origin and set their own headers).
func securityHeaders(next http.Handler) http.Handler {
	strict := httpx.SecurityHeaders(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/proxy/") {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			next.ServeHTTP(w, r)
			return
		}
		strict.ServeHTTP(w, r)
	})
}
