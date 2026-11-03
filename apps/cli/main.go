// Command itkctl is the Jenderal Cloud OS administration CLI used by the installer
// and operators: database migrations, bootstrap of the first administrator,
// secret generation, backup key management and offline restore.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/term"

	"github.com/itk-cloud/itk-cloud/internal/modules/backups"
	"github.com/itk-cloud/itk-cloud/internal/modules/settings"
	"github.com/itk-cloud/itk-cloud/internal/modules/users"
	"github.com/itk-cloud/itk-cloud/internal/platform/config"
	"github.com/itk-cloud/itk-cloud/internal/platform/crypto"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
)

var version = "dev"

const usage = `itkctl - Jenderal Cloud OS administration

Usage:
  itkctl migrate [status]                 Apply (or show) database migrations
  itkctl admin create --username U --email E --name N [--password-stdin]
                                          Create a super administrator
  itkctl gen-secrets                      Print random values for SESSION_SECRET, ENCRYPTION_KEY, AGENT_SHARED_SECRET
  itkctl backup keygen                    Generate an age key pair for backup encryption
  itkctl backup decrypt --identity-file F --in IN --out OUT
                                          Decrypt a backup file
  itkctl backup run --kind platform_db|config|workspace_metadata [--trigger pre_upgrade]
                                          Run an encrypted backup now (synchronously)
  itkctl restore platform-db --identity-file F --file BACKUP [--yes]
                                          Restore the platform database (services must be stopped)
  itkctl health [--url URL]               Check API readiness
  itkctl version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "migrate":
		err = cmdMigrate(os.Args[2:])
	case "admin":
		err = cmdAdmin(os.Args[2:])
	case "gen-secrets":
		err = cmdGenSecrets()
	case "backup":
		err = cmdBackup(os.Args[2:])
	case "restore":
		err = cmdRestore(os.Args[2:])
	case "health":
		err = cmdHealth(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("itkctl", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func connect(ctx context.Context) (*config.Config, *pgxpool.Pool, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("configuration error:\n%w", err)
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	return cfg, pool, err
}

func cmdMigrate(args []string) error {
	ctx := context.Background()
	_, pool, err := connect(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	if len(args) > 0 && args[0] == "status" {
		cur, latest, err := db.MigrationStatus(ctx, pool)
		if err != nil {
			return err
		}
		fmt.Printf("current version: %d\nlatest version:  %d\n", cur, latest)
		if cur != latest {
			fmt.Println("status: migrations pending")
		} else {
			fmt.Println("status: up to date")
		}
		return nil
	}
	before, _, _ := db.MigrationStatus(ctx, pool)
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	after, _, _ := db.MigrationStatus(ctx, pool)
	fmt.Printf("migrations applied: version %d -> %d\n", before, after)
	return nil
}

func readPassword(prompt string, fromStdin bool) (string, error) {
	if fromStdin || !term.IsTerminal(int(syscall.Stdin)) {
		r := bufio.NewReader(os.Stdin)
		line, err := r.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	return string(b), err
}

func cmdAdmin(args []string) error {
	if len(args) == 0 || args[0] != "create" {
		return errors.New("usage: itkctl admin create --username U --email E --name N [--password-stdin]")
	}
	fs := flag.NewFlagSet("admin create", flag.ContinueOnError)
	username := fs.String("username", "", "username")
	email := fs.String("email", "", "email")
	name := fs.String("name", "", "full name")
	stdin := fs.Bool("password-stdin", false, "read the password from stdin")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *username == "" || *email == "" || *name == "" {
		return errors.New("--username, --email and --name are required")
	}
	pw, err := readPassword("Password (min 12 chars, 3 character classes): ", *stdin)
	if err != nil {
		return err
	}
	if msg := crypto.ValidatePasswordStrength(pw); msg != "" {
		return errors.New("password " + msg)
	}
	if !*stdin && term.IsTerminal(int(syscall.Stdin)) {
		again, err := readPassword("Repeat password: ", false)
		if err != nil {
			return err
		}
		if again != pw {
			return errors.New("passwords do not match")
		}
	}
	ctx := context.Background()
	_, pool, err := connect(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	id, err := users.CreateAdmin(ctx, pool, *username, *email, *name, pw)
	if err != nil {
		return err
	}
	fmt.Printf("super administrator %q created (id %s). MFA enrollment is required at first login.\n", *username, id)
	return nil
}

func randomB64(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func cmdGenSecrets() error {
	fmt.Printf("SESSION_SECRET=%s\n", randomB64(48))
	fmt.Printf("ENCRYPTION_KEY=%s\n", randomB64(32))
	fmt.Printf("AGENT_SHARED_SECRET=%s\n", randomB64(48))
	return nil
}

func cmdBackup(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: itkctl backup keygen | decrypt")
	}
	switch args[0] {
	case "keygen":
		id, err := age.GenerateX25519Identity()
		if err != nil {
			return err
		}
		fmt.Printf("# Store the PRIVATE key OFFLINE (password manager / sealed envelope). It is required to restore backups.\n")
		fmt.Printf("# private key: %s\n", id.String())
		fmt.Printf("BACKUP_AGE_RECIPIENT=%s\n", id.Recipient().String())
		return nil
	case "run":
		fs := flag.NewFlagSet("backup run", flag.ContinueOnError)
		kind := fs.String("kind", "platform_db", "backup kind")
		trigger := fs.String("trigger", "manual", "manual | pre_upgrade")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		switch *kind {
		case "platform_db", "config", "workspace_metadata":
		default:
			return errors.New("--kind must be platform_db, config or workspace_metadata (other kinds run in the worker)")
		}
		if *trigger != "manual" && *trigger != "pre_upgrade" {
			return errors.New("--trigger must be manual or pre_upgrade")
		}
		ctx := context.Background()
		cfg, pool, err := connect(ctx)
		if err != nil {
			return err
		}
		defer pool.Close()
		svc := &backups.Service{Pool: pool, Cfg: cfg, Settings: &settings.Store{Pool: pool}}
		b, err := svc.RunNow(ctx, *kind, *trigger)
		if err != nil {
			return fmt.Errorf("backup failed: %w", err)
		}
		fmt.Printf("backup %s succeeded: %s (%d bytes, sha256 %s)\n", b.ID, deref(b.FileName), derefInt(b.SizeBytes), deref(b.ChecksumSHA256))
		return nil
	case "decrypt":
		fs := flag.NewFlagSet("backup decrypt", flag.ContinueOnError)
		idFile := fs.String("identity-file", "", "file containing the age private key")
		in := fs.String("in", "", "encrypted backup file")
		out := fs.String("out", "", "output file")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *idFile == "" || *in == "" || *out == "" {
			return errors.New("--identity-file, --in and --out are required")
		}
		return decryptFile(*idFile, *in, *out)
	}
	return errors.New("unknown backup subcommand")
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt(n *int64) int64 {
	if n == nil {
		return 0
	}
	return *n
}

func loadIdentity(path string) (age.Identity, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "# private key:"))
		if strings.HasPrefix(line, "AGE-SECRET-KEY-") {
			return age.ParseX25519Identity(line)
		}
	}
	return nil, errors.New("no AGE-SECRET-KEY found in identity file")
}

func decryptFile(idFile, in, out string) error {
	id, err := loadIdentity(idFile)
	if err != nil {
		return err
	}
	f, err := os.Open(in)
	if err != nil {
		return err
	}
	defer f.Close()
	r, err := age.Decrypt(f, id)
	if err != nil {
		return fmt.Errorf("decrypt: %w", err)
	}
	o, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(o, r); err != nil {
		o.Close()
		os.Remove(out)
		return err
	}
	return o.Close()
}

func cmdRestore(args []string) error {
	if len(args) == 0 || args[0] != "platform-db" {
		return errors.New("usage: itkctl restore platform-db --identity-file F --file BACKUP [--yes]")
	}
	fs := flag.NewFlagSet("restore platform-db", flag.ContinueOnError)
	idFile := fs.String("identity-file", "", "file containing the age private key")
	file := fs.String("file", "", "encrypted platform_db backup (.dump.age)")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *idFile == "" || *file == "" {
		return errors.New("--identity-file and --file are required")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !*yes {
		fmt.Println("This will OVERWRITE the Jenderal Cloud OS platform database with the backup contents.")
		fmt.Println("Stop itk-cloud-api and itk-cloud-worker first (systemctl stop itk-cloud-api itk-cloud-worker).")
		fmt.Print("Type RESTORE to continue: ")
		var answer string
		_, _ = fmt.Scanln(&answer)
		if answer != "RESTORE" {
			return errors.New("aborted")
		}
	}
	tmp, err := os.CreateTemp("", "itk-restore-*.dump")
	if err != nil {
		return err
	}
	tmp.Close()
	os.Remove(tmp.Name())
	defer os.Remove(tmp.Name())
	if err := decryptFile(*idFile, *file, tmp.Name()); err != nil {
		return err
	}
	pgCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	cc := pgCfg.ConnConfig
	cmd := exec.Command("pg_restore", "--clean", "--if-exists", "--no-owner", "--no-privileges", "--single-transaction", "--exit-on-error", tmp.Name())
	cmd.Env = append(os.Environ(), "PGHOST="+cc.Host, fmt.Sprintf("PGPORT=%d", cc.Port), "PGUSER="+cc.User, "PGDATABASE="+cc.Database, "PGPASSWORD="+cc.Password)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore failed: %w", err)
	}
	fmt.Println("platform database restored. Run `itkctl migrate` and start the services.")
	return nil
}

func cmdHealth(args []string) error {
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	url := fs.String("url", "", "readiness URL (default http://API_HOST:API_PORT/health/ready)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *url == "" {
		host, port := os.Getenv("API_HOST"), os.Getenv("API_PORT")
		if host == "" {
			host = "127.0.0.1"
		}
		if port == "" {
			port = "8080"
		}
		*url = "http://" + host + ":" + port + "/health/ready"
	}
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get(*url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	fmt.Println(strings.TrimSpace(string(body)))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("not ready (HTTP %d)", resp.StatusCode)
	}
	return nil
}
