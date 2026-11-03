// Package server implements the itk-cloud-agent Unix socket server: request
// authentication, the operation allowlist dispatcher, streaming file
// transfer and terminal sessions.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"github.com/itk-cloud/itk-cloud/apps/agent/internal/config"
	"github.com/itk-cloud/itk-cloud/apps/agent/internal/docker"
	"github.com/itk-cloud/itk-cloud/apps/agent/internal/fsops"
	"github.com/itk-cloud/itk-cloud/apps/agent/internal/hostinfo"
	"github.com/itk-cloud/itk-cloud/apps/agent/internal/storage"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// Version is set at build time.
var Version = "dev"

// Server is the agent server.
type Server struct {
	cfg       *config.Config
	log       *slog.Logger
	verifier  *agentproto.Verifier
	docker    *docker.Client
	storage   *storage.Manager
	collector *hostinfo.Collector
	sem       chan struct{}
}

// New creates a server.
func New(cfg *config.Config, log *slog.Logger) *Server {
	return &Server{
		cfg: cfg, log: log,
		verifier:  agentproto.NewVerifier(cfg.SharedSecret),
		docker:    docker.New(cfg.DockerSocket),
		storage:   storage.New(cfg.WorkspaceRoot, cfg.StorageDriver),
		collector: &hostinfo.Collector{ExcludeMountPrefixes: []string{cfg.WorkspaceRoot}},
		sem:       make(chan struct{}, cfg.MaxConcurrentOps),
	}
}

// Handler returns the HTTP handler (exposed for tests).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+agentproto.PathOps, s.handleOp)
	mux.HandleFunc("GET "+agentproto.PathDownload, s.handleDownload)
	mux.HandleFunc("POST "+agentproto.PathUpload, s.handleUpload)
	mux.HandleFunc("GET "+agentproto.PathTerminal, s.handleTerminal)
	return mux
}

// Startup performs startup reconciliation (remount workspace volumes).
func (s *Server) Startup(ctx context.Context) {
	if err := os.MkdirAll(s.cfg.WorkspaceRoot, 0o711); err != nil {
		s.log.Error("create workspace root", "error", err)
	}
	if err := os.MkdirAll(s.cfg.BackupRoot, 0o700); err != nil {
		s.log.Error("create backup root", "error", err)
	}
	for _, err := range s.storage.RemountAll(ctx) {
		s.log.Error("remount workspace volume", "error", err)
	}
}

// ListenAndServe serves on the Unix socket until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.cfg.SocketPath), 0o750); err != nil {
		return err
	}
	_ = os.Remove(s.cfg.SocketPath)
	ln, err := net.Listen("unix", s.cfg.SocketPath)
	if err != nil {
		return err
	}
	if err := os.Chmod(s.cfg.SocketPath, 0o660); err != nil {
		ln.Close()
		return err
	}
	if g, err := user.LookupGroup(s.cfg.SocketGroup); err == nil {
		gid, _ := strconv.Atoi(g.Gid)
		if err := os.Chown(s.cfg.SocketPath, -1, gid); err != nil {
			s.log.Warn("could not set socket group", "group", s.cfg.SocketGroup, "error", err)
		}
		_ = os.Chown(filepath.Dir(s.cfg.SocketPath), -1, gid)
	} else {
		s.log.Warn("socket group not found; socket is accessible to the agent user only", "group", s.cfg.SocketGroup)
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    128 << 10,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	s.log.Info("agent listening", "socket", s.cfg.SocketPath, "version", Version, "protocol", agentproto.ProtocolVersion)
	err = srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// ---------------------------------------------------------------------------
// Responses and error mapping
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeOK(w http.ResponseWriter, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		writeErr(w, &agentproto.Error{Code: agentproto.ErrCodeInternal, Message: "encode response"})
		return
	}
	writeJSON(w, http.StatusOK, agentproto.Response{OK: true, Data: b})
}

func writeErr(w http.ResponseWriter, err error) {
	ae := toAgentError(err)
	status := http.StatusInternalServerError
	switch ae.Code {
	case agentproto.ErrCodeInvalid:
		status = http.StatusBadRequest
	case agentproto.ErrCodeForbidden:
		status = http.StatusForbidden
	case agentproto.ErrCodeNotFound:
		status = http.StatusNotFound
	case agentproto.ErrCodeConflict:
		status = http.StatusConflict
	case agentproto.ErrCodeTooLarge:
		status = http.StatusRequestEntityTooLarge
	case agentproto.ErrCodeUnavailable:
		status = http.StatusServiceUnavailable
	case agentproto.ErrCodeQuota:
		status = http.StatusInsufficientStorage
	}
	writeJSON(w, status, agentproto.Response{OK: false, Error: ae})
}

type invalidErr struct{ msg string }

func (e invalidErr) Error() string { return e.msg }

func invalid(msg string) error { return invalidErr{msg} }

func toAgentError(err error) *agentproto.Error {
	var ae *agentproto.Error
	if errors.As(err, &ae) {
		return ae
	}
	var ie invalidErr
	var de *docker.APIError
	switch {
	case errors.As(err, &ie):
		return &agentproto.Error{Code: agentproto.ErrCodeInvalid, Message: ie.msg}
	case errors.Is(err, docker.ErrUnavailable):
		return &agentproto.Error{Code: agentproto.ErrCodeUnavailable, Message: "container runtime unavailable"}
	case errors.Is(err, fsops.ErrNotFound), errors.Is(err, docker.ErrNotFound), errors.Is(err, os.ErrNotExist):
		return &agentproto.Error{Code: agentproto.ErrCodeNotFound, Message: "not found"}
	case errors.Is(err, fsops.ErrExists):
		return &agentproto.Error{Code: agentproto.ErrCodeConflict, Message: "a file or directory with that name already exists"}
	case errors.Is(err, fsops.ErrNotEmpty):
		return &agentproto.Error{Code: agentproto.ErrCodeConflict, Message: "directory is not empty"}
	case errors.Is(err, fsops.ErrModified):
		return &agentproto.Error{Code: agentproto.ErrCodeConflict, Message: "file was modified since it was opened"}
	case errors.Is(err, fsops.ErrNotDir):
		return &agentproto.Error{Code: agentproto.ErrCodeInvalid, Message: "not a directory"}
	case errors.Is(err, fsops.ErrNotFile):
		return &agentproto.Error{Code: agentproto.ErrCodeInvalid, Message: "not a regular file"}
	case errors.Is(err, fsops.ErrTooLarge):
		return &agentproto.Error{Code: agentproto.ErrCodeTooLarge, Message: "file exceeds the size limit"}
	case errors.Is(err, errNoSpace):
		return &agentproto.Error{Code: agentproto.ErrCodeQuota, Message: "workspace storage quota exceeded"}
	case errors.As(err, &de):
		if de.Status == http.StatusConflict {
			return &agentproto.Error{Code: agentproto.ErrCodeConflict, Message: de.Message}
		}
		return &agentproto.Error{Code: agentproto.ErrCodeInternal, Message: "container runtime error: " + de.Message}
	}
	if isNoSpace(err) {
		return &agentproto.Error{Code: agentproto.ErrCodeQuota, Message: "workspace storage quota exceeded"}
	}
	msg := err.Error()
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return &agentproto.Error{Code: agentproto.ErrCodeInternal, Message: msg}
}

// ---------------------------------------------------------------------------
// Authentication
// ---------------------------------------------------------------------------

// authenticate verifies the signature. For /v1/ops the digest covers the
// body; for streaming endpoints it covers the operation header.
func (s *Server) authenticate(r *http.Request, digest string) error {
	return s.verifier.Verify(r.Method, r.URL.Path,
		r.Header.Get(agentproto.HeaderTimestamp), r.Header.Get(agentproto.HeaderNonce),
		r.Header.Get(agentproto.HeaderSignature), digest)
}

func (s *Server) acquire(ctx context.Context) (func(), error) {
	select {
	case s.sem <- struct{}{}:
		return func() { <-s.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(30 * time.Second):
		return nil, &agentproto.Error{Code: agentproto.ErrCodeUnavailable, Message: "agent is busy"}
	}
}

func (s *Server) logOp(req *agentproto.Request, start time.Time, err error) {
	attrs := []any{"operation", req.Operation, "request_id", req.RequestID, "actor_id", req.ActorID,
		"resource_id", req.ResourceID, "reason", req.Reason, "duration_ms", time.Since(start).Milliseconds()}
	if err != nil {
		s.log.Warn("agent operation failed", append(attrs, "error", toAgentError(err).Message)...)
		return
	}
	if sensitive[req.Operation] {
		s.log.Info("agent operation", attrs...)
	} else {
		s.log.Debug("agent operation", attrs...)
	}
}

// sensitive operations are always logged at info level.
var sensitive = map[string]bool{
	agentproto.OpWorkspaceProvision: true, agentproto.OpWorkspaceDestroy: true,
	agentproto.OpContainerCreate: true, agentproto.OpContainerStart: true, agentproto.OpContainerStop: true,
	agentproto.OpContainerRestart: true, agentproto.OpContainerRemove: true, agentproto.OpImageEnsure: true,
	agentproto.OpFilesDelete: true, agentproto.OpFilesRename: true, agentproto.OpFilesUpload: true,
	agentproto.OpFilesWrite: true, agentproto.OpFilesDownload: true, agentproto.OpTerminalAttach: true,
	agentproto.OpNativeUserEnsure: true, agentproto.OpBackupWorkspace: true, agentproto.OpRestoreWorkspace: true,
	agentproto.OpEmergencyStopAll: true,
}

// streamOps may only be invoked through their dedicated endpoints.
var streamOps = map[string]string{
	agentproto.OpFilesDownload:  agentproto.PathDownload,
	agentproto.OpFilesUpload:    agentproto.PathUpload,
	agentproto.OpTerminalAttach: agentproto.PathTerminal,
}

func (s *Server) handleOp(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 {
		writeErr(w, invalid("request body too large"))
		return
	}
	if err := s.authenticate(r, agentproto.Digest(body)); err != nil {
		s.log.Warn("rejected unauthenticated agent request", "error", err.Error())
		writeErr(w, &agentproto.Error{Code: agentproto.ErrCodeForbidden, Message: "authentication failed"})
		return
	}
	var req agentproto.Request
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErr(w, invalid("malformed request envelope"))
		return
	}
	if err := req.Validate(); err != nil {
		writeErr(w, invalid(err.Error()))
		return
	}
	if _, isStream := streamOps[req.Operation]; isStream {
		writeErr(w, invalid("operation must use its streaming endpoint"))
		return
	}
	payload, err := agentproto.DecodePayload(&req)
	if err != nil {
		writeErr(w, invalid(err.Error()))
		return
	}
	release, err := s.acquire(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	defer release()
	start := time.Now()
	data, err := s.dispatch(r.Context(), &req, payload)
	s.logOp(&req, start, err)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, data)
}

// streamRequest authenticates and decodes a streaming endpoint request.
func (s *Server) streamRequest(r *http.Request, wantOp string) (*agentproto.Request, agentproto.Payload, error) {
	op := r.Header.Get(agentproto.HeaderOp)
	if op == "" {
		return nil, nil, invalid("missing operation header")
	}
	if err := s.authenticate(r, agentproto.Digest([]byte(op))); err != nil {
		s.log.Warn("rejected unauthenticated agent stream request", "error", err.Error())
		return nil, nil, &agentproto.Error{Code: agentproto.ErrCodeForbidden, Message: "authentication failed"}
	}
	req, err := agentproto.DecodeOpHeader(op)
	if err != nil {
		return nil, nil, invalid(err.Error())
	}
	if err := req.Validate(); err != nil {
		return nil, nil, invalid(err.Error())
	}
	if req.Operation != wantOp {
		return nil, nil, invalid("operation does not match endpoint")
	}
	payload, err := agentproto.DecodePayload(req)
	if err != nil {
		return nil, nil, invalid(err.Error())
	}
	return req, payload, nil
}
