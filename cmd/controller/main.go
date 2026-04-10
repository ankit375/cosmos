package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
//	"time"

	"github.com/yourorg/cloudctrl/internal/api"
	"github.com/yourorg/cloudctrl/internal/config"
	"github.com/yourorg/cloudctrl/pkg/logger"
	"go.uber.org/zap"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

func main() {
	configPath := flag.String("config", "configs/controller.dev.yaml", "Path to configuration file")
	showVersion := flag.Bool("version", false, "Show version info")
	flag.Parse()

	if *showVersion {
		fmt.Printf("cloudctrl %s (commit: %s, built: %s)\n", Version, GitCommit, BuildTime)
		os.Exit(0)
	}

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	log, err := logger.New(cfg.Log.Level, cfg.Log.Format, cfg.Log.Output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer log.Sync()

	log.Info("starting cloud controller",
		zap.String("version", Version),
		zap.String("commit", GitCommit),
		zap.String("built", BuildTime),
		zap.String("config", *configPath),
	)

	// Create root context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Build and start the application
	app, err := api.NewApp(ctx, cfg, log)
	if err != nil {
		log.Fatal("failed to initialize application", zap.Error(err))
	}

	// Start the application
	if err := app.Start(); err != nil {
		log.Fatal("failed to start application", zap.Error(err))
	}

	log.Info("cloud controller is ready",
		zap.String("http_addr", cfg.Server.HTTPAddr),
		zap.String("ws_addr", cfg.Server.WSAddr),
	)

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	log.Info("shutdown signal received", zap.String("signal", sig.String()))

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownGrace)
	defer shutdownCancel()

	if err := app.Stop(shutdownCtx); err != nil {
		log.Error("shutdown error", zap.Error(err))
	}

	log.Info("cloud controller stopped")
}
