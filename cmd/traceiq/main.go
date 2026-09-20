// Command traceiq is the composition root: it wires store, topology,
// sampler, ingest, anomaly (baseline) and rca (rules reasoner) into a
// single dev-mode process (DR-2's cmd/traceiq row; X-OPS §4.1's DevMode
// box). internal/api and web/ are being built concurrently by a sibling
// agent and are NOT imported here (see System.Start's TODO comment in
// system.go) — this binary serves /healthz, /readyz and /metrics on
// selfobs.metrics_endpoint as a stand-in until that package is stable.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"traceiq/internal/config"
)

// version is overridden at release-build time via
// -ldflags "-X main.version=...". It defaults to "dev" for local builds.
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("traceiq", flag.ContinueOnError)
	fs.SetOutput(stderr)

	showVersion := fs.Bool("version", false, "print the traceiq version and exit")
	configPath := fs.String("config", "traceiq.yaml", "path to the traceiq YAML configuration file (01 §7)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "traceiq: loading config %q: %v\n", *configPath, err)
		return 1
	}

	sys, err := newSystem(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "traceiq: assembling system: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := sys.Start(ctx); err != nil {
		fmt.Fprintf(stderr, "traceiq: starting system: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "traceiq: running (profile=%s mode=%s data_dir=%s selfobs=%s)\n",
		cfg.Server.Profile, cfg.Server.Mode, cfg.Server.DataDir, cfg.SelfObs.MetricsEndpoint)

	<-ctx.Done()
	fmt.Fprintln(stderr, "traceiq: shutdown signal received, draining")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Ops.ShutdownGrace+5*time.Second)
	defer cancel()
	if err := sys.Shutdown(shutdownCtx, cfg.Ops.ShutdownGrace); err != nil {
		fmt.Fprintf(stderr, "traceiq: shutdown error: %v\n", err)
		return 1
	}
	fmt.Fprintln(stderr, "traceiq: shutdown complete")
	return 0
}
