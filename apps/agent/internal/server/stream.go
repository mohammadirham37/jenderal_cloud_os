package server

import (
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/itk-cloud/itk-cloud/apps/agent/internal/fsops"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

func (s *Server) openFS(r *http.Request, workspaceID string) (*fsops.FS, error) {
	if err := s.storage.EnsureMounted(r.Context(), workspaceID); err != nil {
		return nil, &agentproto.Error{Code: agentproto.ErrCodeNotFound, Message: "workspace storage is not available"}
	}
	dir, err := s.storage.DataDir(workspaceID)
	if err != nil {
		return nil, invalid(err.Error())
	}
	f, err := fsops.Open(dir)
	if errors.Is(err, fsops.ErrNotFound) {
		return nil, &agentproto.Error{Code: agentproto.ErrCodeNotFound, Message: "workspace storage is not provisioned"}
	}
	return f, err
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	req, p, err := s.streamRequest(r, agentproto.OpFilesDownload)
	if err != nil {
		writeErr(w, err)
		return
	}
	start := time.Now()
	fp := p.(*agentproto.FilePath)
	f, err := s.openFS(r, fp.WorkspaceID)
	if err != nil {
		s.logOp(req, start, err)
		writeErr(w, err)
		return
	}
	defer f.Close()
	fh, info, err := f.OpenDownload(fp.Path)
	if err != nil {
		s.logOp(req, start, err)
		writeErr(w, err)
		return
	}
	defer fh.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("X-ITK-File-Name", path.Base(fp.Path))
	w.WriteHeader(http.StatusOK)
	_, err = io.CopyN(w, fh, info.Size())
	s.logOp(req, start, err)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	req, p, err := s.streamRequest(r, agentproto.OpFilesUpload)
	if err != nil {
		writeErr(w, err)
		return
	}
	start := time.Now()
	up := p.(*agentproto.FileUpload)
	f, err := s.openFS(r, up.WorkspaceID)
	if err != nil {
		s.logOp(req, start, err)
		writeErr(w, err)
		return
	}
	defer f.Close()
	entry, err := f.Upload(up.Path, r.Body, up.MaxBytes, up.Overwrite)
	s.logOp(req, start, err)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, entry)
}
