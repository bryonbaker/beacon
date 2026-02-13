// Package main is the entrypoint for the test-endpoint binary. It loads
// configuration, starts the HTTP server, and handles graceful shutdown.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bryonbaker/beacon/test-endpoint/internal/config"
	"github.com/bryonbaker/beacon/test-endpoint/internal/server"
)

func main() {
	configPath := flag.String("config", "", "Path to YAML configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}

	log.Printf("starting test-endpoint on port %d (mode=%s, path=%s)",
		cfg.Server.Port, cfg.Behavior.Mode, cfg.Server.Path)

	srv := server.New(cfg)

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      srv.Handler(),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Start server in a goroutine
	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		log.Printf("received signal %s, shutting down gracefully", sig)
	case err := <-errCh:
		log.Printf("server error: %v, shutting down", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("server shutdown failed: %v", err)
	}

	log.Println("server stopped")
}
