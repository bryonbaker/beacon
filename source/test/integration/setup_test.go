//go:build integration

// Package integration_test contains end-to-end integration tests for the
// beacon service. Tests exercise the full pipeline from database
// operations through notification delivery using an in-memory SQLite database
// and httptest mock endpoints.
package integration_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/bryonbaker/beacon/internal/config"
	"github.com/bryonbaker/beacon/internal/database"
	"github.com/bryonbaker/beacon/internal/metrics"
	"github.com/bryonbaker/beacon/internal/models"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

// testEnv bundles all shared dependencies for an integration test run.
type testEnv struct {
	DB      database.Database
	Config  *config.Config
	Metrics *metrics.Metrics
	Logger  *zap.Logger
	Server  *httptest.Server

	// mu protects received payloads.
	mu       sync.Mutex
	received []models.NotificationPayload
}

// setupTestEnv creates an in-memory SQLite database, a mock HTTP server that
// records received notification payloads, a test configuration pointing at
// that server, and a logger. The returned testEnv must be torn down by calling
// the cleanup function returned as the second value.
func setupTestEnv(t *testing.T) (*testEnv, func()) {
	t.Helper()

	logger, err := zap.NewDevelopment()
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	// Create an in-memory SQLite database.
	db, err := database.NewSQLiteDB(":memory:", logger)
	if err != nil {
		t.Fatalf("failed to create in-memory database: %v", err)
	}

	env := &testEnv{
		DB:     db,
		Logger: logger,
	}

	// Create a mock HTTP server that records payloads.
	env.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload models.NotificationPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		env.mu.Lock()
		env.received = append(env.received, payload)
		env.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))

	// Build a test configuration.
	env.Config = newTestConfig(env.Server.URL)

	// Create isolated Prometheus metrics.
	registry := prometheus.NewRegistry()
	env.Metrics = metrics.NewMetrics(registry)

	cleanup := func() {
		env.Server.Close()
		db.Close()
		_ = logger.Sync()
	}

	return env, cleanup
}

// receivedPayloads returns a snapshot of the payloads received by the mock
// HTTP server.
func (e *testEnv) receivedPayloads() []models.NotificationPayload {
	e.mu.Lock()
	defer e.mu.Unlock()
	cp := make([]models.NotificationPayload, len(e.received))
	copy(cp, e.received)
	return cp
}

// resetReceived clears the recorded payloads.
func (e *testEnv) resetReceived() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.received = nil
}

// newTestConfig creates a minimal Config suitable for integration tests. The
// endpointURL should point to the httptest server.
func newTestConfig(endpointURL string) *config.Config {
	return &config.Config{
		App: config.AppConfig{
			Name:      "beacon-integration-test",
			Version:   "test",
			LogLevel:  "debug",
			LogFormat: "text",
		},
		Resources: []config.ResourceConfig{
			{
				APIVersion: "v1",
				Kind:       "Pod",
			},
		},
		Annotation: config.AnnotationConfig{
			Key:    "bakerapps.net.maas",
			Values: []string{"managed"},
		},
		Endpoint: config.EndpointConfig{
			URL:     endpointURL,
			Method:  "POST",
			Timeout: config.Duration{Duration: 5 * time.Second},
			Retry: config.RetryConfig{
				MaxAttempts:       5,
				InitialBackoff:    config.Duration{Duration: 100 * time.Millisecond},
				MaxBackoff:        config.Duration{Duration: 2 * time.Second},
				BackoffMultiplier: 2.0,
				Jitter:            0.1,
			},
		},
		Worker: config.WorkerConfig{
			PollInterval: config.Duration{Duration: 100 * time.Millisecond},
			BatchSize:    10,
			Concurrency:  2,
		},
		Reconciliation: config.ReconciliationConfig{
			Enabled:   true,
			Interval:  config.Duration{Duration: 1 * time.Second},
			OnStartup: true,
			Timeout:   config.Duration{Duration: 5 * time.Second},
		},
		Retention: config.RetentionConfig{
			Enabled:         true,
			CleanupInterval: config.Duration{Duration: 500 * time.Millisecond},
			RetentionPeriod: config.Duration{Duration: 1 * time.Second},
		},
		Storage: config.StorageConfig{
			MonitorInterval:   config.Duration{Duration: 1 * time.Second},
			DBPath:            ":memory:",
			VolumePath:        "/tmp",
			WarningThreshold:  80,
			CriticalThreshold: 90,
		},
		Metrics: config.MetricsConfig{
			Enabled: true,
			Port:    0, // not used in integration tests
			Path:    "/metrics",
		},
		Health: config.HealthConfig{
			LivenessPath:  "/healthz",
			ReadinessPath: "/ready",
			Port:          0,
		},
	}
}

// newTestManagedObject creates a ManagedObject with sensible defaults for
// integration testing. Fields can be overridden after creation.
func newTestManagedObject(resourceName, resourceUID string) *models.ManagedObject {
	return &models.ManagedObject{
		ID:                uuid.New().String(),
		ResourceUID:       resourceUID,
		ResourceType:      "Pod",
		ResourceName:      resourceName,
		ResourceNamespace: "default",
		AnnotationValue:   "managed",
		ClusterState:      models.ClusterStateExists,
		DetectionSource:   models.DetectionSourceWatch,
		CreatedAt:         time.Now(),
		Labels:            `{"app":"test"}`,
		ResourceVersion:   "1",
		FullMetadata:      "{}",
	}
}
