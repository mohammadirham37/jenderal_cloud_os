// Package images manages the container image allowlist. Only allow-listed
// images can be used by workspace templates.
package images

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/modules/agent"
	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// Image is an allow-listed image.
type Image struct {
	ID            uuid.UUID  `json:"id"`
	Name          string     `json:"name"`
	Reference     string     `json:"reference"`
	Description   string     `json:"description"`
	GPUCapable    bool       `json:"gpu_capable"`
	IsAllowed     bool       `json:"is_allowed"`
	PulledAt      *time.Time `json:"pulled_at"`
	SizeBytes     *int64     `json:"size_bytes"`
	TemplateCount int        `json:"template_count"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

const selectImage = `SELECT i.id, i.name, i.reference, i.description, i.gpu_capable, i.is_allowed, i.pulled_at, i.size_bytes,
	(SELECT count(*) FROM workspace_templates t WHERE t.image_id=i.id), i.created_at, i.updated_at FROM container_images i`

func scan(row pgx.Row) (Image, error) {
	var i Image
	err := row.Scan(&i.ID, &i.Name, &i.Reference, &i.Description, &i.GPUCapable, &i.IsAllowed, &i.PulledAt, &i.SizeBytes, &i.TemplateCount, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

// Handler serves /images.
type Handler struct {
	Pool  *pgxpool.Pool
	Agent *agent.Agent
}

// List handles GET /images.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	if !authz.FromContext(r.Context()).CanAny("images.view", "images.manage") {
		return httpx.Forbidden("")
	}
	rows, err := h.Pool.Query(r.Context(), selectImage+" ORDER BY i.name")
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Image{}
	for rows.Next() {
		i, err := scan(rows)
		if err != nil {
			return err
		}
		out = append(out, i)
	}
	return httpx.OK(w, out)
}

var refRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:@-]{1,250}$`)

type input struct {
	Name        *string `json:"name"`
	Reference   *string `json:"reference"`
	Description *string `json:"description"`
	GPUCapable  *bool   `json:"gpu_capable"`
	IsAllowed   *bool   `json:"is_allowed"`
}

// Create handles POST /images.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).Can("images.manage") {
		return httpx.Forbidden("")
	}
	var in input
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	d := map[string]string{}
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" || len(*in.Name) > 80 {
		d["name"] = "is required (max 80 characters)"
	}
	if in.Reference == nil || !refRe.MatchString(*in.Reference) || strings.Contains(*in.Reference, "..") {
		d["reference"] = "must be a valid lowercase image reference, e.g. registry.example/org/image:tag"
	}
	if len(d) > 0 {
		return httpx.Validation(d)
	}
	desc := ""
	if in.Description != nil {
		desc = *in.Description
	}
	var id uuid.UUID
	err := db.WithTx(ctx, h.Pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO container_images (name, reference, description, gpu_capable, is_allowed, created_by)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, strings.TrimSpace(*in.Name), *in.Reference, desc,
			in.GPUCapable != nil && *in.GPUCapable, in.IsAllowed == nil || *in.IsAllowed, authz.FromContext(ctx).UserID).Scan(&id)
		if db.IsUniqueViolation(err, "") {
			return httpx.Validation(map[string]string{"reference": "already in the catalog"})
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "image.create", ResourceType: "image", ResourceID: id.String(), Metadata: map[string]any{"reference": *in.Reference}})
	})
	if err != nil {
		return err
	}
	i, err := scan(h.Pool.QueryRow(ctx, selectImage+" WHERE i.id=$1", id))
	if err != nil {
		return err
	}
	return httpx.Created(w, i)
}

// Update handles PATCH /images/{id}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).Can("images.manage") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Image")
	}
	var in input
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.Reference != nil {
		return httpx.Validation(map[string]string{"reference": "cannot be changed; add a new image instead"})
	}
	if in.Name != nil && (strings.TrimSpace(*in.Name) == "" || len(*in.Name) > 80) {
		return httpx.Validation(map[string]string{"name": "must be 1-80 characters"})
	}
	err = db.WithTx(ctx, h.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE container_images SET name=coalesce($2,name), description=coalesce($3,description),
			gpu_capable=coalesce($4,gpu_capable), is_allowed=coalesce($5,is_allowed), updated_at=now() WHERE id=$1`,
			id, in.Name, in.Description, in.GPUCapable, in.IsAllowed)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("Image")
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "image.update", ResourceType: "image", ResourceID: id.String(), Metadata: map[string]any{"input": in}})
	})
	if err != nil {
		return err
	}
	i, err := scan(h.Pool.QueryRow(ctx, selectImage+" WHERE i.id=$1", id))
	if err != nil {
		return err
	}
	return httpx.OK(w, i)
}

// Pull handles POST /images/{id}/pull: checks availability on the host and
// pulls from the registry when missing.
func (h *Handler) Pull(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).Can("images.manage") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Image")
	}
	img, err := scan(h.Pool.QueryRow(ctx, selectImage+" WHERE i.id=$1", id))
	if db.IsNotFound(err) {
		return httpx.NotFound("Image")
	}
	if err != nil {
		return err
	}
	pctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	var info agentproto.ImageInfo
	err = h.Agent.Do(pctx, agentproto.OpImageEnsure, id.String(), "administrator pull/check image", agentproto.ImageEnsure{Reference: img.Reference, Pull: true}, &info)
	outcome := audit.Success
	if err != nil {
		outcome = audit.Failure
	}
	_ = audit.Record(ctx, h.Pool, audit.Entry{Action: "image.pull", Outcome: outcome, ResourceType: "image", ResourceID: id.String(), Metadata: map[string]any{"reference": img.Reference}})
	if err != nil {
		return agent.MapError(err)
	}
	if _, err := h.Pool.Exec(ctx, `UPDATE container_images SET pulled_at=now(), size_bytes=$2, updated_at=now() WHERE id=$1`, id, info.SizeBytes); err != nil {
		return err
	}
	img, err = scan(h.Pool.QueryRow(ctx, selectImage+" WHERE i.id=$1", id))
	if err != nil {
		return err
	}
	return httpx.OK(w, img)
}
