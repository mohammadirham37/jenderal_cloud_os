// Command itk-cloud-api serves the ITK Cloud REST API (/api/v1) and the
// terminal WebSocket relay. It never runs as root and never talks to Docker
// directly; host operations go through itk-cloud-agent.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/itk-cloud/itk-cloud/internal/app"
	"github.com/itk-cloud/itk-cloud/internal/platform/config"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/logging"
)

var version = "dev"

func main() {
	migrate := flag.Bool("migrate", false, "apply pending database migrations before starting (development)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("itk-cloud-api", version)
		return
	}
	app.Version = version
	if err := run(*migrate); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(migrate bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration error:\n%w", err)
	}
	if os.Geteuid() == 0 && cfg.IsProduction() {
		return errors.New("refusing to run the API as root")
	}
	log := logging.New(cfg.LogLevel, os.Stdout).With("service", "itk-cloud-api")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	deps, err := app.NewDeps(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer deps.Close()
	if migrate {
		if err := db.Migrate(ctx, deps.Pool); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	if cur, latest, err := db.MigrationStatus(ctx, deps.Pool); err == nil && cur != latest {
		log.Warn("database migrations are pending; run `itkctl migrate`", "current", cur, "latest", latest)
	}
	services := app.NewServices(deps)
	srv := &http.Server{
		Addr:              net.JoinHostPort(cfg.APIHost, strconv.Itoa(cfg.APIPort)),
		Handler:           app.NewAPIHandler(deps, services),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("api listening", "addr", srv.Addr, "version", version, "env", cfg.Env)
		errCh <- srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}
