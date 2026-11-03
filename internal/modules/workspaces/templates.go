package workspaces

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
)

// ListTemplates handles GET /templates.
func (h *Handler) ListTemplates(w http.ResponseWriter, r *http.Request) error {
	p := authz.FromContext(r.Context())
	if !p.CanAny("templates.view", "templates.manage") {
		return httpx.Forbidden("")
	}
	cond := " WHERE t.is_active AND i.is_allowed"
	if p.Can("templates.manage") && r.URL.Query().Get("all") == "true" {
		cond = ""
	}
	rows, err := h.Svc.Pool.Query(r.Context(), selectTemplate+cond+" ORDER BY t.category, t.name")
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Template{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return err
		}
		out = append(out, t)
	}
	return httpx.OK(w, out)
}

// GetTemplateHandler handles GET /templates/{id}.
func (h *Handler) GetTemplateHandler(w http.ResponseWriter, r *http.Request) error {
	p := authz.FromContext(r.Context())
	if !p.CanAny("templates.view", "templates.manage") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Template")
	}
	t, err := GetTemplate(r.Context(), h.Svc.Pool, id)
	if db.IsNotFound(err) || (err == nil && !t.IsActive && !p.Can("templates.manage")) {
		return httpx.NotFound("Template")
	}
	if err != nil {
		return err
	}
	return httpx.OK(w, t)
}

type templateInput struct {
	Key                 *string            `json:"key"`
	Name                *string            `json:"name"`
	Description         *string            `json:"description"`
	Category            *string            `json:"category"`
	ImageID             *uuid.UUID         `json:"image_id"`
	RuntimeVersions     *map[string]string `json:"runtime_versions"`
	MinCPUMillis        *int               `json:"min_cpu_millis"`
	MinMemoryBytes      *int64             `json:"min_memory_bytes"`
	RecCPUMillis        *int               `json:"rec_cpu_millis"`
	RecMemoryBytes      *int64             `json:"rec_memory_bytes"`
	DefaultStorageBytes *int64             `json:"default_storage_bytes"`
	Ports               *[]Port            `json:"ports"`
	GPUMode             *string            `json:"gpu_mode"`
	Env                 *map[string]string `json:"env"`
	Command             *[]string          `json:"command"`
	ReadOnlyRootfs      *bool              `json:"read_only_rootfs"`
	IsActive            *bool              `json:"is_active"`
}

var (
	templateKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
	envKeyRe      = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
)

func (in *templateInput) validate(create bool) map[string]string {
	d := map[string]string{}
	if create {
		if in.Key == nil || !templateKeyRe.MatchString(*in.Key) {
			d["key"] = "must be 2-63 lowercase letters, digits or '-'"
		}
		for k, missing := range map[string]bool{"name": in.Name == nil, "image_id": in.ImageID == nil, "min_cpu_millis": in.MinCPUMillis == nil,
			"min_memory_bytes": in.MinMemoryBytes == nil, "rec_cpu_millis": in.RecCPUMillis == nil, "rec_memory_bytes": in.RecMemoryBytes == nil,
			"default_storage_bytes": in.DefaultStorageBytes == nil} {
			if missing {
				d[k] = "is required"
			}
		}
	}
	if in.Name != nil && (strings.TrimSpace(*in.Name) == "" || len(*in.Name) > 80) {
		d["name"] = "must be 1-80 characters"
	}
	if in.MinCPUMillis != nil && (*in.MinCPUMillis < 100 || *in.MinCPUMillis > 64000) {
		d["min_cpu_millis"] = "must be between 100 and 64000"
	}
	if in.MinMemoryBytes != nil && *in.MinMemoryBytes < 64<<20 {
		d["min_memory_bytes"] = "must be at least 64 MiB"
	}
	if in.MinCPUMillis != nil && in.RecCPUMillis != nil && *in.RecCPUMillis < *in.MinCPUMillis {
		d["rec_cpu_millis"] = "must be at least the minimum"
	}
	if in.MinMemoryBytes != nil && in.RecMemoryBytes != nil && *in.RecMemoryBytes < *in.MinMemoryBytes {
		d["rec_memory_bytes"] = "must be at least the minimum"
	}
	if in.DefaultStorageBytes != nil && *in.DefaultStorageBytes < 256<<20 {
		d["default_storage_bytes"] = "must be at least 256 MiB"
	}
	if in.GPUMode != nil && *in.GPUMode != "none" && *in.GPUMode != "optional" && *in.GPUMode != "required" {
		d["gpu_mode"] = "must be none, optional or required"
	}
	if in.Ports != nil {
		if len(*in.Ports) > 16 {
			d["ports"] = "at most 16 ports"
		}
		for _, p := range *in.Ports {
			if p.Port < 1024 || p.Port > 65535 || len(p.Name) > 40 {
				d["ports"] = "ports must be 1024-65535 with names up to 40 characters"
			}
		}
	}
	if in.Env != nil {
		for k, v := range *in.Env {
			if !envKeyRe.MatchString(k) || len(v) > 4096 {
				d["env"] = "invalid environment variable " + k
			}
		}
	}
	if in.Command != nil && len(*in.Command) > 64 {
		d["command"] = "at most 64 arguments"
	}
	return d
}

func (h *Handler) checkImage(r *http.Request, q db.Querier, id uuid.UUID) error {
	var allowed bool
	if err := q.QueryRow(r.Context(), `SELECT is_allowed FROM container_images WHERE id=$1`, id).Scan(&allowed); err != nil {
		if db.IsNotFound(err) {
			return httpx.Validation(map[string]string{"image_id": "image not found"})
		}
		return err
	}
	if !allowed {
		return httpx.Validation(map[string]string{"image_id": "image is not on the allowlist"})
	}
	return nil
}

// CreateTemplate handles POST /templates.
func (h *Handler) CreateTemplate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).Can("templates.manage") {
		return httpx.Forbidden("")
	}
	var in templateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if d := in.validate(true); len(d) > 0 {
		return httpx.Validation(d)
	}
	def := func(s *string, d string) string {
		if s == nil {
			return d
		}
		return *s
	}
	ports, runtime, env, cmd := []Port{}, map[string]string{}, map[string]string{}, []string{}
	if in.Ports != nil {
		ports = *in.Ports
	}
	if in.RuntimeVersions != nil {
		runtime = *in.RuntimeVersions
	}
	if in.Env != nil {
		env = *in.Env
	}
	if in.Command != nil {
		cmd = *in.Command
	}
	sec := Security{RunAsUID: 1000, RunAsGID: 1000, NoNewPrivileges: true, ReadOnlyRootfs: in.ReadOnlyRootfs != nil && *in.ReadOnlyRootfs, CapAdd: []string{}}
	var id uuid.UUID
	err := db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		if err := h.checkImage(r, tx, *in.ImageID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `INSERT INTO workspace_templates (key, name, description, category, image_id, runtime_versions, min_cpu_millis,
			min_memory_bytes, rec_cpu_millis, rec_memory_bytes, default_storage_bytes, ports, gpu_mode, security, env, command, is_active)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) RETURNING id`,
			*in.Key, strings.TrimSpace(*in.Name), def(in.Description, ""), def(in.Category, "general"), *in.ImageID, mustJSON(runtime),
			*in.MinCPUMillis, *in.MinMemoryBytes, *in.RecCPUMillis, *in.RecMemoryBytes, *in.DefaultStorageBytes, mustJSON(ports),
			def(in.GPUMode, "none"), mustJSON(sec), mustJSON(env), mustJSON(cmd), in.IsActive == nil || *in.IsActive).Scan(&id)
		if db.IsUniqueViolation(err, "") {
			return httpx.Validation(map[string]string{"key": "already exists"})
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "template.create", ResourceType: "template", ResourceID: id.String(), Metadata: map[string]any{"key": *in.Key}})
	})
	if err != nil {
		return err
	}
	t, err := GetTemplate(ctx, h.Svc.Pool, id)
	if err != nil {
		return err
	}
	return httpx.Created(w, t)
}

// UpdateTemplate handles PATCH /templates/{id}. Existing workspaces keep the
// limits they were created with.
func (h *Handler) UpdateTemplate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).Can("templates.manage") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Template")
	}
	var in templateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.Key != nil {
		return httpx.Validation(map[string]string{"key": "cannot be changed"})
	}
	if d := in.validate(false); len(d) > 0 {
		return httpx.Validation(d)
	}
	err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		cur, err := GetTemplate(ctx, tx, id)
		if db.IsNotFound(err) {
			return httpx.NotFound("Template")
		}
		if err != nil {
			return err
		}
		if in.ImageID != nil {
			if err := h.checkImage(r, tx, *in.ImageID); err != nil {
				return err
			}
		}
		minCPU, recCPU := cur.MinCPUMillis, cur.RecCPUMillis
		if in.MinCPUMillis != nil {
			minCPU = *in.MinCPUMillis
		}
		if in.RecCPUMillis != nil {
			recCPU = *in.RecCPUMillis
		}
		minMem, recMem := cur.MinMemoryBytes, cur.RecMemoryBytes
		if in.MinMemoryBytes != nil {
			minMem = *in.MinMemoryBytes
		}
		if in.RecMemoryBytes != nil {
			recMem = *in.RecMemoryBytes
		}
		if recCPU < minCPU || recMem < minMem {
			return httpx.Validation(map[string]string{"rec_cpu_millis": "recommended values must be at least the minimums"})
		}
		sec := cur.Security
		if in.ReadOnlyRootfs != nil {
			sec.ReadOnlyRootfs = *in.ReadOnlyRootfs
		}
		var ports, runtime, env, cmd []byte
		if in.Ports != nil {
			ports = mustJSON(*in.Ports)
		}
		if in.RuntimeVersions != nil {
			runtime = mustJSON(*in.RuntimeVersions)
		}
		if in.Env != nil {
			env = mustJSON(*in.Env)
		}
		if in.Command != nil {
			cmd = mustJSON(*in.Command)
		}
		_, err = tx.Exec(ctx, `UPDATE workspace_templates SET name=coalesce($2,name), description=coalesce($3,description), category=coalesce($4,category),
			image_id=coalesce($5,image_id), runtime_versions=coalesce($6,runtime_versions), min_cpu_millis=$7, min_memory_bytes=$8,
			rec_cpu_millis=$9, rec_memory_bytes=$10, default_storage_bytes=coalesce($11,default_storage_bytes), ports=coalesce($12,ports),
			gpu_mode=coalesce($13,gpu_mode), security=$14, env=coalesce($15,env), command=coalesce($16,command), is_active=coalesce($17,is_active),
			updated_at=now() WHERE id=$1`,
			id, in.Name, in.Description, in.Category, in.ImageID, runtime, minCPU, minMem, recCPU, recMem, in.DefaultStorageBytes, ports,
			in.GPUMode, mustJSON(sec), env, cmd, in.IsActive)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "template.update", ResourceType: "template", ResourceID: id.String(), Metadata: map[string]any{"key": cur.Key, "input": in}})
	})
	if err != nil {
		return err
	}
	t, err := GetTemplate(ctx, h.Svc.Pool, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, t)
}
