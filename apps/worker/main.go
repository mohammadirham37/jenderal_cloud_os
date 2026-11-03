// Command itk-cloud-worker runs background jobs (provisioning, lifecycle,
// backups) and periodic tasks (metrics, alerts, idle stop, retention).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/itk-cloud/itk-cloud/internal/app"
	"github.com/itk-cloud/itk-cloud/internal/platform/config"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/logging"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("itk-cloud-worker", version)
		return
	}
	app.Version = version
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration error:\n%w", err)
	}
	if os.Geteuid() == 0 && cfg.IsProduction() {
		return errors.New("refusing to run the worker as root")
	}
	log := logging.New(cfg.LogLevel, os.Stdout).With("service", "itk-cloud-worker")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	deps, err := app.NewDeps(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer deps.Close()
	if cur, latest, err := db.MigrationStatus(ctx, deps.Pool); err != nil || cur != latest {
		return fmt.Errorf("database schema is not up to date (current %d, latest %d); run `itkctl migrate`", cur, latest)
	}
	app.NewWorker(deps, app.NewServices(deps)).Run(ctx)
	return nil
}
