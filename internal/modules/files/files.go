// Package files implements the workspace file manager. All paths are
// workspace-relative; the API normalises them and the host agent resolves
// them inside the workspace root (os.Root), rejecting traversal and symlink
// escapes. Host paths are never sent to the browser.
package files

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/itk-cloud/itk-cloud/internal/modules/agent"
	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	ws "github.com/itk-cloud/itk-cloud/internal/modules/workspaces"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/config"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// Handler serves file endpoints.
type Handler struct {
	Svc *ws.Service
	Cfg *config.Config
}

// workspace authorizes file access (edit level) and checks storage state.
func (h *Handler) workspace(r *http.Request) (*ws.Workspace, error) {
	ctx := r.Context()
	if !authz.FromContext(ctx).Can("files.manage") {
		return nil, httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return nil, httpx.NotFound("Workspace")
	}
	w, _, err := ws.Authorize(ctx, h.Svc.Pool, r, id, ws.LevelEdit)
	if err != nil {
		return nil, err
	}
	switch w.Status {
	case ws.StatusReady, ws.StatusRunning, ws.StatusStopped:
	default:
		return nil, httpx.Conflict("Files are not available while the workspace is " + w.Status + ".")
	}
	ws.Touch(ctx, h.Svc.Pool, w.ID)
	return w, nil
}

func cleanPath(p string) (string, error) {
	c, err := agentproto.CleanRelPath(p)
	if err != nil {
		return "", httpx.Validation(map[string]string{"path": "invalid path"})
	}
	return c, nil
}

func (h *Handler) do(ctx context.Context, w *ws.Workspace, op, reason string, payload, out any) error {
	return agent.MapError(h.Svc.Agent.Do(ctx, op, w.ID.String(), reason, payload, out))
}

// List handles GET /workspaces/{id}/files?path=.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	wsp, err := h.workspace(r)
	if err != nil {
		return err
	}
	p, err := cleanPath(r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	var res struct {
		Entries   []agentproto.FileEntry `json:"entries"`
		Truncated bool                   `json:"truncated"`
	}
	if err := h.do(r.Context(), wsp, agentproto.OpFilesList, "list files", agentproto.FilePath{WorkspaceID: wsp.ID.String(), Path: p}, &res); err != nil {
		return err
	}
	if res.Entries == nil {
		res.Entries = []agentproto.FileEntry{}
	}
	return httpx.OK(w, map[string]any{"path": p, "entries": res.Entries, "truncated": res.Truncated})
}

// Stat handles GET /workspaces/{id}/files/stat?path=.
func (h *Handler) Stat(w http.ResponseWriter, r *http.Request) error {
	wsp, err := h.workspace(r)
	if err != nil {
		return err
	}
	p, err := cleanPath(r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	var e agentproto.FileEntry
	if err := h.do(r.Context(), wsp, agentproto.OpFilesStat, "stat file", agentproto.FilePath{WorkspaceID: wsp.ID.String(), Path: p}, &e); err != nil {
		return err
	}
	return httpx.OK(w, e)
}

// editable lists extensions/names the text editor supports.
var editable = map[string]bool{
	".txt": true, ".md": true, ".go": true, ".py": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true, ".svelte": true,
	".vue": true, ".php": true, ".java": true, ".kt": true, ".c": true, ".h": true, ".cpp": true, ".hpp": true, ".cc": true,
	".rs": true, ".rb": true, ".sh": true, ".bash": true, ".zsh": true, ".json": true, ".yaml": true, ".yml": true, ".toml": true,
	".ini": true, ".cfg": true, ".conf": true, ".env": true, ".xml": true, ".html": true, ".htm": true, ".css": true, ".scss": true,
	".sql": true, ".csv": true, ".tsv": true, ".r": true, ".m": true, ".ipynb": true, ".gitignore": true, ".dockerignore": true,
	".mod": true, ".sum": true, ".lock": true, ".log": true, ".tex": true, ".bib": true, ".gradle": true, ".properties": true,
}

var editableNames = map[string]bool{"Makefile": true, "Dockerfile": true, "README": true, "LICENSE": true, "Procfile": true, "Gemfile": true, ".bashrc": true, ".profile": true}

// Editable reports whether the editor supports a file name.
func Editable(name string) bool {
	base := path.Base(name)
	return editable[strings.ToLower(path.Ext(base))] || editableNames[base]
}

// ReadContent handles GET /workspaces/{id}/files/content?path=.
func (h *Handler) ReadContent(w http.ResponseWriter, r *http.Request) error {
	wsp, err := h.workspace(r)
	if err != nil {
		return err
	}
	p, err := cleanPath(r.URL.Query().Get("path"))
	if err != nil || p == "" {
		return httpx.Validation(map[string]string{"path": "a file path is required"})
	}
	if !Editable(p) {
		return httpx.NewError(http.StatusUnsupportedMediaType, "unsupported_file_type", "This file type cannot be edited in the browser. Download it instead.")
	}
	var fc agentproto.FileContent
	if err := h.do(r.Context(), wsp, agentproto.OpFilesRead, "read file for editor",
		agentproto.FileRead{WorkspaceID: wsp.ID.String(), Path: p, MaxBytes: h.Cfg.EditorMaxBytes}, &fc); err != nil {
		var he *httpx.Error
		if errors.As(err, &he) && he.Code == "payload_too_large" {
			return httpx.NewError(http.StatusRequestEntityTooLarge, "payload_too_large", "The file is too large for the browser editor. Download it instead.")
		}
		return err
	}
	if fc.Binary {
		return httpx.NewError(http.StatusUnsupportedMediaType, "binary_file", "This file appears to be binary and cannot be edited in the browser.")
	}
	return httpx.OK(w, map[string]any{"path": p, "content": fc.Content, "size": fc.Size, "mod_time": fc.ModTime.Format(time.RFC3339Nano)})
}

type writeInput struct {
	Path            string `json:"path"`
	Content         string `json:"content"`
	ExpectedModTime string `json:"expected_mod_time"`
	Create          bool   `json:"create"`
}

// WriteContent handles PUT /workspaces/{id}/files/content.
func (h *Handler) WriteContent(w http.ResponseWriter, r *http.Request) error {
	wsp, err := h.workspace(r)
	if err != nil {
		return err
	}
	var in writeInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	p, err := cleanPath(in.Path)
	if err != nil || p == "" {
		return httpx.Validation(map[string]string{"path": "a file path is required"})
	}
	if !Editable(p) {
		return httpx.Validation(map[string]string{"path": "this file type cannot be edited in the browser"})
	}
	if int64(len(in.Content)) > h.Cfg.EditorMaxBytes {
		return httpx.NewError(http.StatusRequestEntityTooLarge, "payload_too_large", "Content exceeds the editor size limit.")
	}
	var e agentproto.FileEntry
	if err := h.do(r.Context(), wsp, agentproto.OpFilesWrite, "save file from editor", agentproto.FileWrite{
		WorkspaceID: wsp.ID.String(), Path: p, Content: in.Content, ExpectedModTime: in.ExpectedModTime, Create: in.Create}, &e); err != nil {
		return err
	}
	return httpx.OK(w, e)
}

type pathInput struct {
	Path string `json:"path"`
}

// Mkdir handles POST /workspaces/{id}/files/mkdir.
func (h *Handler) Mkdir(w http.ResponseWriter, r *http.Request) error {
	wsp, err := h.workspace(r)
	if err != nil {
		return err
	}
	var in pathInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	p, err := cleanPath(in.Path)
	if err != nil || p == "" {
		return httpx.Validation(map[string]string{"path": "a folder path is required"})
	}
	var e agentproto.FileEntry
	if err := h.do(r.Context(), wsp, agentproto.OpFilesMkdir, "create folder", agentproto.FilePath{WorkspaceID: wsp.ID.String(), Path: p}, &e); err != nil {
		return err
	}
	return httpx.Created(w, e)
}

type renameInput struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Rename handles PATCH /workspaces/{id}/files/rename.
func (h *Handler) Rename(w http.ResponseWriter, r *http.Request) error {
	wsp, err := h.workspace(r)
	if err != nil {
		return err
	}
	var in renameInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	from, err1 := cleanPath(in.From)
	to, err2 := cleanPath(in.To)
	if err1 != nil || err2 != nil || from == "" || to == "" {
		return httpx.Validation(map[string]string{"to": "source and destination paths are required"})
	}
	if to == from || strings.HasPrefix(to, from+"/") {
		return httpx.Validation(map[string]string{"to": "cannot move a folder into itself"})
	}
	var e agentproto.FileEntry
	ctx := r.Context()
	if err := h.do(ctx, wsp, agentproto.OpFilesRename, "rename file", agentproto.FileRename{WorkspaceID: wsp.ID.String(), From: from, To: to}, &e); err != nil {
		return err
	}
	_ = audit.Record(ctx, h.Svc.Pool, audit.Entry{Action: "files.rename", ResourceType: "workspace", ResourceID: wsp.ID.String(),
		Metadata: map[string]any{"from": from, "to": to}})
	return httpx.OK(w, e)
}

// Delete handles DELETE /workspaces/{id}/files?path=&recursive=.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	wsp, err := h.workspace(r)
	if err != nil {
		return err
	}
	p, err := cleanPath(r.URL.Query().Get("path"))
	if err != nil || p == "" {
		return httpx.Validation(map[string]string{"path": "cannot delete the workspace root"})
	}
	recursive := r.URL.Query().Get("recursive") == "true"
	ctx := r.Context()
	err = h.do(ctx, wsp, agentproto.OpFilesDelete, "delete file", agentproto.FileDelete{WorkspaceID: wsp.ID.String(), Path: p, Recursive: recursive}, nil)
	outcome := audit.Success
	if err != nil {
		outcome = audit.Failure
	}
	_ = audit.Record(ctx, h.Svc.Pool, audit.Entry{Action: "files.delete", Outcome: outcome, ResourceType: "workspace", ResourceID: wsp.ID.String(),
		Metadata: map[string]any{"path": p, "recursive": recursive}})
	if err != nil {
		return err
	}
	return httpx.NoContent(w)
}

// Download handles GET /workspaces/{id}/files/download?path= (streamed).
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) error {
	wsp, err := h.workspace(r)
	if err != nil {
		return err
	}
	p, err := cleanPath(r.URL.Query().Get("path"))
	if err != nil || p == "" {
		return httpx.Validation(map[string]string{"path": "a file path is required"})
	}
	ctx := r.Context()
	body, size, err := h.Svc.Agent.Client.Download(ctx, h.Svc.Agent.Call(ctx, agentproto.OpFilesDownload, wsp.ID.String(), "download file",
		agentproto.FilePath{WorkspaceID: wsp.ID.String(), Path: p}))
	if err != nil {
		return agent.MapError(err)
	}
	defer body.Close()
	name := path.Base(p)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, body)
	return nil
}

// Upload handles POST /workspaces/{id}/files/upload?path=<dir>&overwrite=.
// The multipart body is streamed part by part to the agent.
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) error {
	wsp, err := h.workspace(r)
	if err != nil {
		return err
	}
	dir, err := cleanPath(r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	overwrite := r.URL.Query().Get("overwrite") == "true"
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return httpx.NewError(http.StatusUnsupportedMediaType, "unsupported_media_type", "Upload must be multipart/form-data.")
	}
	limit := h.Cfg.UploadMaxBytes
	r.Body = http.MaxBytesReader(w, r.Body, limit+1<<20)
	// Best-effort storage quota check (hard limit is enforced by the volume
	// when the loopback storage driver is used).
	if wsp.StorageUsedBytes != nil && r.ContentLength > 0 && *wsp.StorageUsedBytes+r.ContentLength > wsp.StorageBytes {
		return httpx.NewError(http.StatusInsufficientStorage, "quota_exceeded", "The upload would exceed the workspace storage quota.")
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return httpx.BadRequest("Invalid multipart body.")
	}
	ctx := r.Context()
	uploaded := []agentproto.FileEntry{}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return httpx.NewError(http.StatusRequestEntityTooLarge, "payload_too_large", "Upload exceeds the size limit.")
			}
			return httpx.BadRequest("Invalid multipart body.")
		}
		if part.FormName() != "file" || part.FileName() == "" {
			part.Close()
			continue
		}
		name := path.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
		if name == "." || name == "/" || name == ".." || len(name) > 255 {
			part.Close()
			return httpx.Validation(map[string]string{"file": "invalid file name"})
		}
		dest, err := cleanPath(path.Join(dir, name))
		if err != nil || dest == "" {
			part.Close()
			return httpx.Validation(map[string]string{"file": "invalid file name"})
		}
		entry, err := h.Svc.Agent.Client.Upload(ctx, h.Svc.Agent.Call(ctx, agentproto.OpFilesUpload, wsp.ID.String(), "upload file",
			agentproto.FileUpload{WorkspaceID: wsp.ID.String(), Path: dest, MaxBytes: limit, Overwrite: overwrite}), part)
		part.Close()
		if err != nil {
			return agent.MapError(err)
		}
		uploaded = append(uploaded, *entry)
		if len(uploaded) > 100 {
			return httpx.Validation(map[string]string{"file": "at most 100 files per upload"})
		}
	}
	if len(uploaded) == 0 {
		return httpx.Validation(map[string]string{"file": "no file was uploaded"})
	}
	names := make([]string, 0, len(uploaded))
	for _, u := range uploaded {
		names = append(names, u.Path)
	}
	_ = audit.Record(ctx, h.Svc.Pool, audit.Entry{Action: "files.upload", ResourceType: "workspace", ResourceID: wsp.ID.String(),
		Metadata: map[string]any{"paths": names}})
	return httpx.Created(w, uploaded)
}
