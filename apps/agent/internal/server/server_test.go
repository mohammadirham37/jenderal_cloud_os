package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itk-cloud/itk-cloud/apps/agent/internal/config"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

const secret = "test-secret-test-secret-test-secret!"

func startAgent(t *testing.T) (*agentproto.Client, *config.Config) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "itkag")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	cfg := &config.Config{
		SocketPath: filepath.Join(dir, "a.sock"), SocketGroup: "nonexistent-group-itk",
		SharedSecret: []byte(secret), WorkspaceRoot: filepath.Join(dir, "ws"), BackupRoot: filepath.Join(dir, "bk"),
		StorageDriver: "directory", DockerSocket: filepath.Join(dir, "no-docker.sock"), NvidiaSMI: "nvidia-smi-missing",
		TmpfsSize: "64m", MaxConcurrentOps: 4,
	}
	srv := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv.Startup(ctx)
	go func() { _ = srv.ListenAndServe(ctx) }()
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("unix", cfg.SocketPath); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return agentproto.NewClient(cfg.SocketPath, []byte(secret)), cfg
}

func call(op string, payload any) agentproto.Call {
	return agentproto.Call{Op: op, ActorID: "test-actor", ResourceID: "r", Reason: "test", Payload: payload}
}

const wsID = "11111111-2222-3333-4444-555555555555"

func TestAgentEndToEnd(t *testing.T) {
	c, cfg := startAgent(t)
	ctx := context.Background()

	var info agentproto.AgentInfo
	if err := c.Do(ctx, call(agentproto.OpAgentInfo, nil), &info); err != nil {
		t.Fatalf("agent.info: %v", err)
	}
	if info.ProtocolVersion != agentproto.ProtocolVersion || info.Capabilities.Docker {
		t.Fatalf("unexpected info: %+v", info)
	}
	if info.Capabilities.StorageEnforced {
		t.Fatal("directory driver must report storage as not enforced")
	}

	var hm agentproto.HostMetrics
	if err := c.Do(ctx, call(agentproto.OpHostMetrics, nil), &hm); err != nil {
		t.Fatalf("host.metrics: %v", err)
	}
	if hm.MemoryTotal == 0 || hm.Docker.Available {
		t.Fatalf("unexpected metrics: %+v", hm)
	}

	var gi agentproto.GPUInventory
	if err := c.Do(ctx, call(agentproto.OpGPUInventory, nil), &gi); err != nil || gi.Available {
		t.Fatalf("gpu.inventory without nvidia-smi must report unavailable: %+v %v", gi, err)
	}

	if err := c.Do(ctx, call(agentproto.OpWorkspaceProvision, agentproto.WorkspaceProvision{WorkspaceID: wsID, StorageBytes: 1 << 30, OwnerUID: 1000}), nil); err != nil {
		t.Fatalf("provision: %v", err)
	}
	// idempotent
	if err := c.Do(ctx, call(agentproto.OpWorkspaceProvision, agentproto.WorkspaceProvision{WorkspaceID: wsID, StorageBytes: 1 << 30, OwnerUID: 1000}), nil); err != nil {
		t.Fatalf("provision again: %v", err)
	}
	if err := c.Do(ctx, call(agentproto.OpFilesMkdir, agentproto.FilePath{WorkspaceID: wsID, Path: "docs"}), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Upload(ctx, call(agentproto.OpFilesUpload, agentproto.FileUpload{WorkspaceID: wsID, Path: "docs/hello.txt", MaxBytes: 100}), strings.NewReader("hello")); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := c.Upload(ctx, call(agentproto.OpFilesUpload, agentproto.FileUpload{WorkspaceID: wsID, Path: "docs/big.txt", MaxBytes: 3}), strings.NewReader("too big")); !agentproto.IsCode(err, agentproto.ErrCodeTooLarge) {
		t.Fatalf("expected too_large, got %v", err)
	}
	body, _, err := c.Download(ctx, call(agentproto.OpFilesDownload, agentproto.FilePath{WorkspaceID: wsID, Path: "/docs/../docs/hello.txt"}))
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	b, _ := io.ReadAll(body)
	body.Close()
	if string(b) != "hello" {
		t.Fatalf("download content %q", b)
	}
	var listing struct {
		Entries []agentproto.FileEntry `json:"entries"`
	}
	if err := c.Do(ctx, call(agentproto.OpFilesList, agentproto.FilePath{WorkspaceID: wsID, Path: "docs"}), &listing); err != nil || len(listing.Entries) != 1 {
		t.Fatalf("list: %+v %v", listing, err)
	}
	// traversal is contained inside the workspace
	if err := c.Do(ctx, call(agentproto.OpFilesRead, agentproto.FileRead{WorkspaceID: wsID, Path: "../../../../etc/passwd", MaxBytes: 1000}), nil); !agentproto.IsCode(err, agentproto.ErrCodeNotFound) {
		t.Fatalf("traversal must resolve inside the workspace and not find host files, got %v", err)
	}
	// symlink escape blocked
	data := filepath.Join(cfg.WorkspaceRoot, wsID, "data")
	_ = os.Symlink("/etc", filepath.Join(data, "etc-link"))
	if err := c.Do(ctx, call(agentproto.OpFilesRead, agentproto.FileRead{WorkspaceID: wsID, Path: "etc-link/hosts", MaxBytes: 100000}), nil); err == nil {
		t.Fatal("symlink escape must fail")
	}
	// container ops fail cleanly when docker is unavailable
	err = c.Do(ctx, call(agentproto.OpContainerList, nil), nil)
	if !agentproto.IsCode(err, agentproto.ErrCodeUnavailable) {
		t.Fatalf("expected unavailable, got %v", err)
	}
	if err := c.Do(ctx, call(agentproto.OpWorkspaceDestroy, agentproto.WorkspaceRef{WorkspaceID: wsID}), nil); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.WorkspaceRoot, wsID)); !os.IsNotExist(err) {
		t.Fatal("workspace dir should be removed")
	}
}

func TestAgentRejectsBadAuthAndStreamOpsOnOpsEndpoint(t *testing.T) {
	_, cfg := startAgent(t)
	bad := agentproto.NewClient(cfg.SocketPath, []byte("wrong-secret-wrong-secret-wrong-secret"))
	if err := bad.Do(context.Background(), call(agentproto.OpAgentInfo, nil), nil); !agentproto.IsCode(err, agentproto.ErrCodeForbidden) {
		t.Fatalf("expected forbidden, got %v", err)
	}
	good := agentproto.NewClient(cfg.SocketPath, []byte(secret))
	err := good.Do(context.Background(), call(agentproto.OpFilesDownload, agentproto.FilePath{WorkspaceID: wsID, Path: "x"}), nil)
	if !agentproto.IsCode(err, agentproto.ErrCodeInvalid) {
		t.Fatalf("stream op via /v1/ops must be rejected, got %v", err)
	}
	// unsigned raw request
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return net.Dial("unix", cfg.SocketPath)
	}}}
	resp, err := hc.Post("http://agent"+agentproto.PathOps, "application/json", strings.NewReader(`{"protocol_version":1,"operation":"agent.info"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unsigned request: status %d", resp.StatusCode)
	}
	// socket permissions
	st, err := os.Stat(cfg.SocketPath)
	if err != nil || st.Mode().Perm() != 0o660 {
		t.Fatalf("socket mode %v %v", st.Mode().Perm(), err)
	}
}
