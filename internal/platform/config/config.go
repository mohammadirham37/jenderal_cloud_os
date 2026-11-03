// Package config loads and validates ITK Cloud configuration from the
// environment. Configuration is validated at startup; the process refuses to
// start with an invalid or insecure production configuration.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds runtime configuration shared by the API, worker and CLI.
type Config struct {
	Env      string // development | test | production
	BaseURL  string
	LogLevel string

	APIHost string
	APIPort int

	DatabaseURL string
	RedisURL    string

	SessionSecret []byte
	EncryptionKey []byte // 32 bytes, AES-256-GCM for recoverable secrets

	SessionTTL     time.Duration
	SessionIdleTTL time.Duration
	CookieSecure   bool

	AgentSocketPath   string
	AgentSharedSecret []byte

	WorkspaceRoot          string
	WorkspaceDefaultDriver string
	NativeWorkspaces       bool

	UploadMaxBytes      int64
	EditorMaxBytes      int64
	TerminalSessionTTL  time.Duration
	TerminalIdleTimeout time.Duration

	GPUEnabled bool

	BackupRoot          string
	BackupAgeRecipient  string
	BackupRetentionDays int
	// BackupCopyDir is a second location (separate disk/NFS) that receives a
	// copy of every backup file.
	BackupCopyDir string

	ProjectDBAdminURL string
	ProjectDBHost     string
	ProjectDBPort     int

	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string

	PrometheusURL string
	GrafanaURL    string

	// AppProxyOrigin is the separate origin (scheme://host:port) on which
	// workspace web apps are proxied. Empty disables the app proxy.
	AppProxyOrigin string
	// WebHealthURL is probed to report the web frontend status.
	WebHealthURL string
	// ConfigDir holds configuration files included in config backups.
	ConfigDir string
}

// IsProduction reports whether the configuration targets production.
func (c *Config) IsProduction() bool { return c.Env == "production" }

// SMTPEnabled reports whether outgoing mail is configured.
func (c *Config) SMTPEnabled() bool { return c.SMTPHost != "" && c.SMTPFrom != "" }

// ProjectDatabasesEnabled reports whether project database provisioning is configured.
func (c *Config) ProjectDatabasesEnabled() bool { return c.ProjectDBAdminURL != "" }

// Load reads configuration from environment variables. If ITK_CLOUD_ENV_FILE
// (or .env in the working directory during development) exists, it is read
// first; real environment variables always take precedence.
func Load() (*Config, error) {
	envFile := os.Getenv("ITK_CLOUD_ENV_FILE")
	if envFile == "" {
		if _, err := os.Stat(".env"); err == nil {
			envFile = ".env"
		}
	}
	if envFile != "" {
		if err := loadEnvFile(envFile); err != nil {
			return nil, fmt.Errorf("load env file: %w", err)
		}
	}
	return FromEnv(os.Getenv)
}

// FromEnv builds a Config from a lookup function. Exposed for tests.
func FromEnv(get func(string) string) (*Config, error) {
	var errs []error
	str := func(key, def string) string {
		if v := strings.TrimSpace(get(key)); v != "" {
			return v
		}
		return def
	}
	integer := func(key string, def int) int {
		v := str(key, "")
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s must be an integer", key))
			return def
		}
		return n
	}
	int64v := func(key string, def int64) int64 {
		v := str(key, "")
		if v == "" {
			return def
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s must be an integer", key))
			return def
		}
		return n
	}
	boolean := func(key string, def bool) bool {
		v := str(key, "")
		if v == "" {
			return def
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s must be a boolean", key))
			return def
		}
		return b
	}

	c := &Config{
		Env:                    str("APP_ENV", "development"),
		BaseURL:                strings.TrimRight(str("APP_BASE_URL", "http://localhost:5173"), "/"),
		LogLevel:               str("APP_LOG_LEVEL", "info"),
		APIHost:                str("API_HOST", "127.0.0.1"),
		APIPort:                integer("API_PORT", 8080),
		DatabaseURL:            str("DATABASE_URL", ""),
		RedisURL:               str("REDIS_URL", ""),
		SessionTTL:             time.Duration(integer("SESSION_TTL_HOURS", 12)) * time.Hour,
		SessionIdleTTL:         time.Duration(integer("SESSION_IDLE_MINUTES", 120)) * time.Minute,
		AgentSocketPath:        str("AGENT_SOCKET_PATH", "/run/itk-cloud/agent.sock"),
		WorkspaceRoot:          str("WORKSPACE_ROOT", "/srv/itk-cloud/workspaces"),
		WorkspaceDefaultDriver: str("WORKSPACE_DEFAULT_DRIVER", "docker"),
		NativeWorkspaces:       boolean("WORKSPACE_NATIVE_ENABLED", false),
		UploadMaxBytes:         int64v("UPLOAD_MAX_BYTES", 100<<20),
		EditorMaxBytes:         int64v("EDITOR_MAX_BYTES", 1<<20),
		TerminalSessionTTL:     time.Duration(integer("TERMINAL_SESSION_TTL_MINUTES", 60)) * time.Minute,
		TerminalIdleTimeout:    time.Duration(integer("TERMINAL_IDLE_TIMEOUT_MINUTES", 30)) * time.Minute,
		GPUEnabled:             boolean("GPU_ENABLED", false),
		BackupRoot:             str("BACKUP_ROOT", "/srv/itk-cloud/backups"),
		BackupAgeRecipient:     str("BACKUP_AGE_RECIPIENT", ""),
		BackupRetentionDays:    integer("BACKUP_RETENTION_DAYS", 14),
		BackupCopyDir:          str("BACKUP_COPY_DIR", ""),
		ProjectDBAdminURL:      str("PROJECT_DB_ADMIN_URL", ""),
		ProjectDBHost:          str("PROJECT_DB_HOST", "127.0.0.1"),
		ProjectDBPort:          integer("PROJECT_DB_PORT", 5432),
		SMTPHost:               str("SMTP_HOST", ""),
		SMTPPort:               integer("SMTP_PORT", 587),
		SMTPUsername:           str("SMTP_USERNAME", ""),
		SMTPPassword:           str("SMTP_PASSWORD", ""),
		SMTPFrom:               str("SMTP_FROM", ""),
		PrometheusURL:          str("PROMETHEUS_URL", ""),
		GrafanaURL:             str("GRAFANA_URL", ""),
		AppProxyOrigin:         strings.TrimRight(str("APP_PROXY_ORIGIN", ""), "/"),
		WebHealthURL:           str("WEB_HEALTH_URL", ""),
		ConfigDir:              str("CONFIG_DIR", "/etc/itk-cloud"),
	}

	switch c.Env {
	case "development", "test", "production":
	default:
		errs = append(errs, fmt.Errorf("APP_ENV must be development, test or production"))
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("APP_LOG_LEVEL must be debug, info, warn or error"))
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, fmt.Errorf("APP_BASE_URL must be an absolute http(s) URL"))
	} else {
		c.CookieSecure = boolean("COOKIE_SECURE", u.Scheme == "https")
	}
	if c.AppProxyOrigin != "" {
		pu, err := url.Parse(c.AppProxyOrigin)
		if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" || pu.Path != "" {
			errs = append(errs, fmt.Errorf("APP_PROXY_ORIGIN must be an origin like https://itk-cloud.local:8443"))
		} else if u != nil && pu.Host == u.Host {
			errs = append(errs, fmt.Errorf("APP_PROXY_ORIGIN must differ from APP_BASE_URL (separate origin isolates workspace apps)"))
		}
	}
	if c.APIPort <= 0 || c.APIPort > 65535 {
		errs = append(errs, fmt.Errorf("API_PORT out of range"))
	}
	if c.DatabaseURL == "" {
		errs = append(errs, fmt.Errorf("DATABASE_URL is required"))
	}
	if c.RedisURL == "" {
		errs = append(errs, fmt.Errorf("REDIS_URL is required"))
	}

	c.SessionSecret = []byte(str("SESSION_SECRET", ""))
	if len(c.SessionSecret) < 32 {
		errs = append(errs, fmt.Errorf("SESSION_SECRET must be at least 32 characters"))
	}
	key, err := decodeKey(str("ENCRYPTION_KEY", ""))
	if err != nil {
		errs = append(errs, fmt.Errorf("ENCRYPTION_KEY: %w", err))
	}
	c.EncryptionKey = key
	c.AgentSharedSecret = []byte(str("AGENT_SHARED_SECRET", ""))
	if len(c.AgentSharedSecret) < 32 {
		errs = append(errs, fmt.Errorf("AGENT_SHARED_SECRET must be at least 32 characters"))
	}

	if !filepath.IsAbs(c.WorkspaceRoot) {
		errs = append(errs, fmt.Errorf("WORKSPACE_ROOT must be an absolute path"))
	}
	if !filepath.IsAbs(c.BackupRoot) {
		errs = append(errs, fmt.Errorf("BACKUP_ROOT must be an absolute path"))
	}
	if !filepath.IsAbs(c.AgentSocketPath) {
		errs = append(errs, fmt.Errorf("AGENT_SOCKET_PATH must be an absolute path"))
	}
	switch c.WorkspaceDefaultDriver {
	case "docker", "native":
	default:
		errs = append(errs, fmt.Errorf("WORKSPACE_DEFAULT_DRIVER must be docker or native"))
	}
	if c.WorkspaceDefaultDriver == "native" && !c.NativeWorkspaces {
		errs = append(errs, fmt.Errorf("WORKSPACE_DEFAULT_DRIVER=native requires WORKSPACE_NATIVE_ENABLED=true"))
	}
	if c.UploadMaxBytes <= 0 || c.EditorMaxBytes <= 0 {
		errs = append(errs, fmt.Errorf("UPLOAD_MAX_BYTES and EDITOR_MAX_BYTES must be positive"))
	}
	if c.TerminalSessionTTL <= 0 || c.SessionTTL <= 0 || c.SessionIdleTTL <= 0 {
		errs = append(errs, fmt.Errorf("session and terminal TTLs must be positive"))
	}
	if c.BackupRetentionDays < 1 {
		errs = append(errs, fmt.Errorf("BACKUP_RETENTION_DAYS must be at least 1"))
	}

	if c.IsProduction() {
		if u != nil && u.Scheme != "https" {
			errs = append(errs, fmt.Errorf("APP_BASE_URL must use https in production"))
		}
		if !c.CookieSecure {
			errs = append(errs, fmt.Errorf("COOKIE_SECURE must be true in production"))
		}
		if c.BackupAgeRecipient == "" {
			errs = append(errs, fmt.Errorf("BACKUP_AGE_RECIPIENT is required in production (backups must be encrypted)"))
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return c, nil
}

func decodeKey(v string) ([]byte, error) {
	if v == "" {
		return nil, errors.New("is required (32 random bytes, base64 encoded)")
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		b, err = base64.RawURLEncoding.DecodeString(v)
	}
	if err != nil {
		return nil, errors.New("must be base64 encoded")
	}
	if len(b) != 32 {
		return nil, errors.New("must decode to exactly 32 bytes")
	}
	return b, nil
}

// loadEnvFile reads KEY=VALUE lines and sets variables that are not already
// present in the process environment.
func loadEnvFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(strings.TrimPrefix(k, "export "))
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if _, exists := os.LookupEnv(k); !exists {
			_ = os.Setenv(k, v)
		}
	}
	return nil
}
