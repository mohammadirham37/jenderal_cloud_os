// Command itk-cloud-agent is the privileged host agent of ITK Cloud. It
// listens on a Unix domain socket and executes only the allow-listed,
// schema-validated operations defined in packages/agentproto.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/itk-cloud/itk-cloud/apps/agent/internal/config"
	"github.com/itk-cloud/itk-cloud/apps/agent/internal/server"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// version is set with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("itk-cloud-agent %s (protocol %d)\n", version, agentproto.ProtocolVersion)
		return
	}
	server.Version = version
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:\n"+err.Error())
		os.Exit(2)
	}
	level := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})).With("service", "itk-cloud-agent")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	srv := server.New(cfg, log)
	srv.Startup(ctx)
	if err := srv.ListenAndServe(ctx); err != nil {
		log.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}
