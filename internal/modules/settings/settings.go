// Package settings manages platform settings stored in platform_settings.
// Only keys declared in the schema can be changed, and each value is
// validated against its declared type and range.
package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
)

// Def describes one setting.
type Def struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"` // bool | int | string | enum
	Default     any      `json:"default"`
	Min         int      `json:"min,omitempty"`
	Max         int      `json:"max,omitempty"`
	Options     []string `json:"options,omitempty"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
}

// Schema lists every configurable setting.
var Schema = []Def{
	{Key: "platform.name", Type: "string", Default: "ITK Cloud", Max: 60, Category: "platform", Description: "Platform display name"},
	{Key: "platform.organization", Type: "string", Default: "Informatika dan Teknik Komputer", Max: 120, Category: "platform", Description: "Organization name"},
	{Key: "security.require_mfa_for_admins", Type: "bool", Default: true, Category: "security", Description: "Require MFA for roles marked as requiring MFA"},
	{Key: "security.login_max_attempts", Type: "int", Default: 5, Min: 3, Max: 20, Category: "security", Description: "Failed logins before temporary lockout"},
	{Key: "security.lockout_minutes", Type: "int", Default: 15, Min: 1, Max: 1440, Category: "security", Description: "Lockout duration in minutes"},
	{Key: "quota.default_package", Type: "string", Default: "basic", Max: 63, Category: "quota", Description: "Quota package key assigned to users without an explicit package"},
	{Key: "workspaces.idle_stop_enabled", Type: "bool", Default: true, Category: "workspaces", Description: "Automatically stop idle workspaces"},
	{Key: "workspaces.max_per_user_hard_limit", Type: "int", Default: 20, Min: 1, Max: 200, Category: "workspaces", Description: "Absolute maximum workspaces per user"},
	{Key: "scheduler.cpu_overcommit_pct", Type: "int", Default: 200, Min: 100, Max: 1000, Category: "scheduler", Description: "Total CPU that may be allocated to running workloads, as % of host threads"},
	{Key: "gpu.release_after_stopped_minutes", Type: "int", Default: 60, Min: 5, Max: 10080, Category: "gpu", Description: "Release GPU allocations of workspaces stopped longer than this"},
	{Key: "gpu.max_duration_hours", Type: "int", Default: 72, Min: 1, Max: 720, Category: "gpu", Description: "Maximum GPU allocation duration"},
	{Key: "gpu.allow_shared", Type: "bool", Default: false, Category: "gpu", Description: "Allow shared (non-exclusive) GPU allocations"},
	{Key: "backup.schedule_enabled", Type: "bool", Default: true, Category: "backup", Description: "Run the daily scheduled backup"},
	{Key: "backup.schedule_hour_utc", Type: "int", Default: 18, Min: 0, Max: 23, Category: "backup", Description: "Hour (UTC) of the daily backup"},
	{Key: "backup.include_workspace_files", Type: "bool", Default: false, Category: "backup", Description: "Include workspace files in scheduled backups"},
	{Key: "monitoring.sample_interval_seconds", Type: "int", Default: 30, Min: 10, Max: 600, Category: "monitoring", Description: "Metrics sampling interval"},
	{Key: "monitoring.retention_days", Type: "int", Default: 14, Min: 1, Max: 365, Category: "monitoring", Description: "Days of metric history to keep"},
	{Key: "alerts.disk_pct", Type: "int", Default: 90, Min: 50, Max: 99, Category: "alerts", Description: "Disk usage alert threshold (%)"},
	{Key: "alerts.memory_pct", Type: "int", Default: 90, Min: 50, Max: 99, Category: "alerts", Description: "Memory usage alert threshold (%)"},
	{Key: "alerts.cpu_pct", Type: "int", Default: 90, Min: 50, Max: 100, Category: "alerts", Description: "Sustained CPU alert threshold (%)"},
	{Key: "alerts.cpu_sustain_minutes", Type: "int", Default: 10, Min: 1, Max: 120, Category: "alerts", Description: "Minutes CPU must stay above threshold"},
	{Key: "alerts.gpu_temp_c", Type: "int", Default: 85, Min: 50, Max: 110, Category: "alerts", Description: "GPU temperature alert threshold (°C)"},
	{Key: "alerts.gpu_util_pct", Type: "int", Default: 98, Min: 50, Max: 100, Category: "alerts", Description: "GPU utilization alert threshold (%)"},
	{Key: "alerts.queue_backlog", Type: "int", Default: 50, Min: 5, Max: 10000, Category: "alerts", Description: "Queued job backlog alert threshold"},
	{Key: "audit.retention_days", Type: "int", Default: 365, Min: 30, Max: 3650, Category: "audit", Description: "Days of audit log to keep"},
}

var schemaByKey = func() map[string]Def {
	m := map[string]Def{}
	for _, d := range Schema {
		m[d.Key] = d
	}
	return m
}()

// Store reads and writes settings.
type Store struct{ Pool *pgxpool.Pool }

// All returns current values for every schema key (defaults filled in).
func (s *Store) All(ctx context.Context) (map[string]any, error) {
	out := map[string]any{}
	for _, d := range Schema {
		out[d.Key] = d.Default
	}
	rows, err := s.Pool.Query(ctx, `SELECT key, value FROM platform_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var raw []byte
		if err := rows.Scan(&k, &raw); err != nil {
			return nil, err
		}
		def, ok := schemaByKey[k]
		if !ok {
			continue
		}
		if v, err := decode(def, raw); err == nil {
			out[k] = v
		}
	}
	return out, rows.Err()
}

func decode(def Def, raw []byte) (any, error) {
	switch def.Type {
	case "bool":
		var b bool
		err := json.Unmarshal(raw, &b)
		return b, err
	case "int":
		var n int
		err := json.Unmarshal(raw, &n)
		return n, err
	default:
		var s string
		err := json.Unmarshal(raw, &s)
		return s, err
	}
}

func (s *Store) raw(ctx context.Context, key string) ([]byte, Def, error) {
	def, ok := schemaByKey[key]
	if !ok {
		return nil, def, fmt.Errorf("unknown setting %q", key)
	}
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT value FROM platform_settings WHERE key=$1`, key).Scan(&raw)
	if err == pgx.ErrNoRows {
		return nil, def, nil
	}
	return raw, def, err
}

// Bool returns a boolean setting (default on error).
func (s *Store) Bool(ctx context.Context, key string) bool {
	raw, def, err := s.raw(ctx, key)
	if err != nil || raw == nil {
		b, _ := def.Default.(bool)
		return b
	}
	v, err := decode(def, raw)
	if err != nil {
		b, _ := def.Default.(bool)
		return b
	}
	return v.(bool)
}

// Int returns an integer setting (default on error).
func (s *Store) Int(ctx context.Context, key string) int {
	raw, def, err := s.raw(ctx, key)
	if err != nil || raw == nil {
		n, _ := def.Default.(int)
		return n
	}
	v, err := decode(def, raw)
	if err != nil {
		n, _ := def.Default.(int)
		return n
	}
	return v.(int)
}

// String returns a string setting (default on error).
func (s *Store) String(ctx context.Context, key string) string {
	raw, def, err := s.raw(ctx, key)
	if err != nil || raw == nil {
		v, _ := def.Default.(string)
		return v
	}
	v, err := decode(def, raw)
	if err != nil {
		d, _ := def.Default.(string)
		return d
	}
	return v.(string)
}

// Validate checks a proposed value and returns the normalised JSON.
func Validate(key string, v any) ([]byte, string) {
	def, ok := schemaByKey[key]
	if !ok {
		return nil, "unknown setting"
	}
	switch def.Type {
	case "bool":
		b, ok := v.(bool)
		if !ok {
			return nil, "must be a boolean"
		}
		out, _ := json.Marshal(b)
		return out, ""
	case "int":
		f, ok := v.(float64)
		if !ok || f != float64(int(f)) {
			return nil, "must be an integer"
		}
		n := int(f)
		if n < def.Min || n > def.Max {
			return nil, fmt.Sprintf("must be between %d and %d", def.Min, def.Max)
		}
		out, _ := json.Marshal(n)
		return out, ""
	default:
		s, ok := v.(string)
		if !ok {
			return nil, "must be a string"
		}
		if len(s) == 0 || (def.Max > 0 && len(s) > def.Max) {
			return nil, fmt.Sprintf("must be 1-%d characters", def.Max)
		}
		out, _ := json.Marshal(s)
		return out, ""
	}
}

// Handler exposes settings endpoints.
type Handler struct{ Store *Store }

type settingView struct {
	Def
	Value any `json:"value"`
}

// Get handles GET /admin/settings.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	if !authz.FromContext(r.Context()).Can("settings.manage") {
		return httpx.Forbidden("")
	}
	vals, err := h.Store.All(r.Context())
	if err != nil {
		return err
	}
	out := make([]settingView, 0, len(Schema))
	for _, d := range Schema {
		out = append(out, settingView{Def: d, Value: vals[d.Key]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Category < out[j].Category })
	return httpx.OK(w, out)
}

// Update handles PATCH /admin/settings with body {"values": {key: value}}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).Can("settings.manage") {
		return httpx.Forbidden("")
	}
	var body struct {
		Values map[string]any `json:"values"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	if len(body.Values) == 0 {
		return httpx.Validation(map[string]string{"values": "at least one setting is required"})
	}
	details := map[string]string{}
	normalized := map[string][]byte{}
	for k, v := range body.Values {
		raw, msg := Validate(k, v)
		if msg != "" {
			details[k] = msg
			continue
		}
		normalized[k] = raw
	}
	if len(details) > 0 {
		return httpx.Validation(details)
	}
	p := authz.FromContext(ctx)
	before, err := h.Store.All(ctx)
	if err != nil {
		return err
	}
	err = db.WithTx(ctx, h.Store.Pool, func(tx pgx.Tx) error {
		if pkg, ok := body.Values["quota.default_package"].(string); ok {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM resource_quotas WHERE key=$1)`, pkg).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return httpx.Validation(map[string]string{"quota.default_package": "unknown quota package"})
			}
		}
		changes := map[string]any{}
		for k, raw := range normalized {
			if _, err := tx.Exec(ctx, `INSERT INTO platform_settings (key, value, updated_by, updated_at) VALUES ($1,$2,$3,$4)
				ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_by=EXCLUDED.updated_by, updated_at=EXCLUDED.updated_at`,
				k, raw, p.UserID, time.Now()); err != nil {
				return err
			}
			changes[k] = map[string]any{"from": before[k], "to": json.RawMessage(raw)}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "settings.update", ResourceType: "settings", Metadata: map[string]any{"changes": changes}})
	})
	if err != nil {
		return err
	}
	return h.Get(w, r)
}
