package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/miloszkolber/uncanny-lab/internal/api"
	"github.com/miloszkolber/uncanny-lab/internal/config"
	"github.com/miloszkolber/uncanny-lab/internal/database"
	"github.com/miloszkolber/uncanny-lab/internal/events"
	"github.com/miloszkolber/uncanny-lab/internal/modelinstall"
	"github.com/miloszkolber/uncanny-lab/internal/orchestrator"
)

var version = "development"
var revision = "unknown"

func main() {
	configPath := flag.String("config", "/config/config.yaml", "configuration file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(1)
	}
	if err := cfg.EnsureDirectories(); err != nil {
		logger.Error("prepare directories", "error", err)
		os.Exit(1)
	}

	repo, err := database.Open(cfg.DatabasePath())
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer repo.Close()
	if restored, err := repo.Reconcile(context.Background(), cfg.JobRoot()); err != nil {
		logger.Error("reconcile artifact index", "error", err)
		os.Exit(1)
	} else if restored > 0 {
		logger.Info("restored jobs from artifact metadata", "count", restored)
	}

	broker := events.NewBroker()
	runner := orchestrator.New(repo, broker, cfg, logger)
	if err := runner.Recover(context.Background()); err != nil {
		logger.Error("recover jobs", "error", err)
		os.Exit(1)
	}
	runner.Start()
	defer runner.Stop()

	installer, err := modelinstall.New(cfg.CheckpointDownloads.Enabled, cfg.Paths.Workspace, cfg.Paths.Models, revision, logger)
	if err != nil {
		logger.Error("create model installer", "error", err)
		os.Exit(1)
	}
	handler, err := api.New(repo, runner, broker, cfg, version, logger, installer)
	if err != nil {
		logger.Error("create HTTP handler", "error", err)
		os.Exit(1)
	}
	server := &http.Server{
		Addr:              cfg.Server.Address(),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	listener, err := net.Listen(listenNetwork(cfg.Server.Host), server.Addr)
	if err != nil {
		logger.Error("listen", "address", server.Addr, "error", err)
		os.Exit(1)
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server started", "address", server.Addr, "version", version)
		errCh <- server.Serve(listener)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigCh:
		logger.Info("shutting down", "signal", sig.String())
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "shutdown:", err)
	}
	if err := installer.Close(ctx); err != nil {
		logger.Error("stop model installer", "error", err)
	}
}

func listenNetwork(host string) string {
	ip := net.ParseIP(host)
	if ip == nil {
		return "tcp"
	}
	if ip.To4() != nil {
		return "tcp4"
	}
	return "tcp6"
}
