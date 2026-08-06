package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/miloszkolber/legacy-image-lab/internal/api"
	"github.com/miloszkolber/legacy-image-lab/internal/config"
	"github.com/miloszkolber/legacy-image-lab/internal/database"
	"github.com/miloszkolber/legacy-image-lab/internal/events"
	"github.com/miloszkolber/legacy-image-lab/internal/orchestrator"
)

var version = "development"

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

	broker := events.NewBroker()
	runner := orchestrator.New(repo, broker, cfg, logger)
	if err := runner.Recover(context.Background()); err != nil {
		logger.Error("recover jobs", "error", err)
		os.Exit(1)
	}
	runner.Start()
	defer runner.Stop()

	handler, err := api.New(repo, runner, broker, cfg, version, logger)
	if err != nil {
		logger.Error("create HTTP handler", "error", err)
		os.Exit(1)
	}
	server := &http.Server{
		Addr:              cfg.Server.Address(),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0, // SSE responses remain open.
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server started", "address", server.Addr, "version", version)
		errCh <- server.ListenAndServe()
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
}
