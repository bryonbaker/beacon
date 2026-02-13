// Package main is the entry point for the beacon service.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"

	"github.com/bryonbaker/beacon/internal/cleaner"
	"github.com/bryonbaker/beacon/internal/config"
	"github.com/bryonbaker/beacon/internal/database"
	"github.com/bryonbaker/beacon/internal/metrics"
	"github.com/bryonbaker/beacon/internal/notifier"
	"github.com/bryonbaker/beacon/internal/reconciler"
	"github.com/bryonbaker/beacon/internal/storage"
	"github.com/bryonbaker/beacon/internal/watcher"
	k8sclient "github.com/bryonbaker/beacon/pkg/kubernetes"
)

func main() {
	// Determine config path
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "/config/config.yaml"
	}

	// Load configuration
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	logger, err := newLogger(cfg.App.LogLevel, cfg.App.LogFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("starting beacon",
		zap.String("name", cfg.App.Name),
		zap.String("version", cfg.App.Version),
		zap.String("log_level", cfg.App.LogLevel),
	)

	// Open database
	db, err := database.NewSQLiteDB(cfg.Storage.DBPath, logger)
	if err != nil {
		logger.Fatal("failed to open database", zap.Error(err))
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		logger.Fatal("database ping failed", zap.Error(err))
	}

	// Initialize Prometheus metrics
	registry := prometheus.NewRegistry()
	m := metrics.NewMetrics(registry)

	// Start metrics/health server
	metricsServer := metrics.NewServer(
		cfg.Metrics.Port,
		cfg.Metrics.Path,
		cfg.Health.LivenessPath,
		cfg.Health.ReadinessPath,
		registry,
	)
	metricsServer.UpdateHealthCheck("database", "ok")

	// Create Kubernetes clients
	typedClient, dynClient, err := k8sclient.NewClients(logger)
	if err != nil {
		logger.Fatal("failed to create kubernetes clients", zap.Error(err))
	}
	metricsServer.UpdateHealthCheck("kubernetes", "ok")

	// Create context with cancellation for shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create components
	w := watcher.NewWatcher(db, typedClient, dynClient, cfg, m, logger)
	n := notifier.NewNotifier(db, &http.Client{Timeout: cfg.Endpoint.Timeout.Duration}, cfg, m, logger)
	r := reconciler.NewReconciler(db, typedClient, dynClient, cfg, m, logger)
	c := cleaner.NewCleaner(db, cfg, m, logger)
	sm := storage.NewMonitor(db, cfg, m, logger)

	// Use errgroup for goroutine lifecycle
	g, gCtx := errgroup.WithContext(ctx)

	// Start metrics server
	g.Go(func() error {
		logger.Info("starting metrics server", zap.Int("port", cfg.Metrics.Port))
		return metricsServer.Start()
	})

	// Start watcher
	g.Go(func() error {
		logger.Info("starting watcher")
		metricsServer.UpdateHealthCheck("watchers", "ok")
		return w.Start(gCtx)
	})

	// Start notifier
	g.Go(func() error {
		logger.Info("starting notifier")
		n.Start(gCtx)
		return nil
	})

	// Start reconciler
	if cfg.Reconciliation.Enabled {
		g.Go(func() error {
			logger.Info("starting reconciler",
				zap.Duration("interval", cfg.Reconciliation.Interval.Duration),
				zap.Bool("on_startup", cfg.Reconciliation.OnStartup),
			)
			r.Start(gCtx)
			return nil
		})
	}

	// Start cleaner
	if cfg.Retention.Enabled {
		g.Go(func() error {
			logger.Info("starting cleaner",
				zap.Duration("interval", cfg.Retention.CleanupInterval.Duration),
				zap.Duration("retention", cfg.Retention.RetentionPeriod.Duration),
			)
			c.Start(gCtx)
			return nil
		})
	}

	// Start storage monitor
	g.Go(func() error {
		logger.Info("starting storage monitor",
			zap.Duration("interval", cfg.Storage.MonitorInterval.Duration),
		)
		sm.Start(gCtx)
		return nil
	})

	// Mark as ready
	metricsServer.SetReady(true)
	logger.Info("beacon is ready")

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-sigCh:
		logger.Info("received shutdown signal", zap.String("signal", sig.String()))
	case <-gCtx.Done():
		logger.Info("context cancelled")
	}

	// Graceful shutdown sequence
	logger.Info("starting graceful shutdown")
	metricsServer.SetReady(false)

	// Stop watcher first
	w.Stop()
	logger.Info("watcher stopped")

	// Cancel context to stop all other components
	cancel()

	// Wait for notifier to drain (max 30s)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Shutdown metrics server
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("metrics server shutdown error", zap.Error(err))
	}

	// Wait for all goroutines
	if err := g.Wait(); err != nil {
		logger.Error("error during shutdown", zap.Error(err))
	}

	logger.Info("beacon shutdown complete")
}

func newLogger(level, format string) (*zap.Logger, error) {
	var cfg zap.Config
	if format == "json" {
		cfg = zap.NewProductionConfig()
	} else {
		cfg = zap.NewDevelopmentConfig()
	}

	switch level {
	case "debug":
		cfg.Level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
	case "info":
		cfg.Level = zap.NewAtomicLevelAt(zapcore.InfoLevel)
	case "warn":
		cfg.Level = zap.NewAtomicLevelAt(zapcore.WarnLevel)
	case "error":
		cfg.Level = zap.NewAtomicLevelAt(zapcore.ErrorLevel)
	default:
		cfg.Level = zap.NewAtomicLevelAt(zapcore.InfoLevel)
	}

	return cfg.Build()
}
