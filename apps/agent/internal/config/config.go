// Package config loads the host agent configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the agent configuration.
type Config struct {
	SocketPath       string
	SocketGroup      string // group allowed to connect (API/worker service group)
	SharedSecret     []byte
	WorkspaceRoot    string
	BackupRoot       string
	StorageDriver    string // loopback | directory
	DockerSocket     string
	NativeEnabled    bool
	NativeGroup      string // primary group for native workspace users
	NvidiaSMI        string
	RootfsSize       string // optional Docker storage-opt size (overlay2 on xfs+pquota)
	TmpfsSize        string
	LogLevel         string
	AppArmorProfile  string
	SeccompProfile   string // path to a custom seccomp JSON; empty = Docker default
	MaxConcurrentOps int
}

// Load reads configuration from the environment.
func Load() (*Config, error) {
	get := func(k, def string) string {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
		return def
	}
	c := &Config{
		SocketPath:      get("AGENT_SOCKET_PATH", "/run/itk-cloud/agent.sock"),
		SocketGroup:     get("AGENT_SOCKET_GROUP", "itk-cloud"),
		SharedSecret:    []byte(get("AGENT_SHARED_SECRET", "")),
		WorkspaceRoot:   get("WORKSPACE_ROOT", "/srv/itk-cloud/workspaces"),
		BackupRoot:      get("BACKUP_ROOT", "/srv/itk-cloud/backups"),
		StorageDriver:   get("AGENT_STORAGE_DRIVER", "loopback"),
		DockerSocket:    get("DOCKER_SOCKET", "/var/run/docker.sock"),
		NativeGroup:     get("AGENT_NATIVE_GROUP", "itk-workspace"),
		NvidiaSMI:       get("NVIDIA_SMI_PATH", "nvidia-smi"),
		RootfsSize:      get("AGENT_CONTAINER_ROOTFS_SIZE", ""),
		TmpfsSize:       get("AGENT_CONTAINER_TMPFS_SIZE", "512m"),
		LogLevel:        get("APP_LOG_LEVEL", "info"),
		AppArmorProfile: get("AGENT_APPARMOR_PROFILE", "docker-default"),
		SeccompProfile:  get("AGENT_SECCOMP_PROFILE", ""),
	}
	if f := get("AGENT_SHARED_SECRET_FILE", ""); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("read AGENT_SHARED_SECRET_FILE: %w", err)
		}
		c.SharedSecret = []byte(strings.TrimSpace(string(b)))
	}
	native, err := strconv.ParseBool(get("WORKSPACE_NATIVE_ENABLED", "false"))
	if err != nil {
		return nil, errors.New("WORKSPACE_NATIVE_ENABLED must be a boolean")
	}
	c.NativeEnabled = native
	c.MaxConcurrentOps, err = strconv.Atoi(get("AGENT_MAX_CONCURRENT_OPS", "32"))
	if err != nil || c.MaxConcurrentOps < 1 {
		return nil, errors.New("AGENT_MAX_CONCURRENT_OPS must be a positive integer")
	}
	var errs []error
	if len(c.SharedSecret) < 32 {
		errs = append(errs, errors.New("AGENT_SHARED_SECRET must be at least 32 characters"))
	}
	for k, v := range map[string]string{"AGENT_SOCKET_PATH": c.SocketPath, "WORKSPACE_ROOT": c.WorkspaceRoot, "BACKUP_ROOT": c.BackupRoot, "DOCKER_SOCKET": c.DockerSocket} {
		if !filepath.IsAbs(v) {
			errs = append(errs, fmt.Errorf("%s must be absolute", k))
		}
	}
	if c.StorageDriver != "loopback" && c.StorageDriver != "directory" {
		errs = append(errs, errors.New("AGENT_STORAGE_DRIVER must be loopback or directory"))
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return c, nil
}
