package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// HealthChecks tracks per-component health status in a thread-safe manner.
type HealthChecks struct {
	mu     sync.RWMutex
	checks map[string]string
}

// NewHealthChecks creates an empty HealthChecks instance.
func NewHealthChecks() *HealthChecks {
	return &HealthChecks{
		checks: make(map[string]string),
	}
}

// Update sets the status for the given component.
func (h *HealthChecks) Update(component string, status string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks[component] = status
}

// All returns a snapshot of all component statuses.
func (h *HealthChecks) All() map[string]string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]string, len(h.checks))
	for k, v := range h.checks {
		out[k] = v
	}
	return out
}

// AllOK returns true if every registered component has the status "ok".
func (h *HealthChecks) AllOK() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, v := range h.checks {
		if v != "ok" {
			return false
		}
	}
	return true
}

// Server exposes Prometheus metrics and health/readiness probes over HTTP.
type Server struct {
	httpServer   *http.Server
	registry     *prometheus.Registry
	healthChecks *HealthChecks

	mu    sync.RWMutex
	ready bool
}

// NewServer creates a new metrics/health HTTP server.
//
// Parameters:
//   - port: TCP port to listen on
//   - metricsPath: URL path for Prometheus metrics (e.g. "/metrics")
//   - healthPath: URL path for the liveness probe (e.g. "/healthz")
//   - readyPath: URL path for the readiness probe (e.g. "/ready")
//   - registry: Prometheus registry to expose (may be nil for default)
func NewServer(port int, metricsPath string, healthPath string, readyPath string, registry *prometheus.Registry) *Server {
	s := &Server{
		registry:     registry,
		healthChecks: NewHealthChecks(),
		ready:        false,
	}

	mux := http.NewServeMux()

	// Prometheus metrics handler.
	if registry != nil {
		mux.Handle(metricsPath, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	} else {
		mux.Handle(metricsPath, promhttp.Handler())
	}

	// Liveness probe -- always returns 200 if the process is running.
	mux.HandleFunc(healthPath, s.handleHealth)

	// Readiness probe -- returns 200 only when all components are healthy.
	mux.HandleFunc(readyPath, s.handleReady)

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	return s
}

// Start begins serving HTTP requests. It blocks until the server is stopped
// or encounters a fatal error. ErrServerClosed is not returned.
func (s *Server) Start() error {
	err := s.httpServer.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown gracefully shuts down the HTTP server using the provided context.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// UpdateHealthCheck updates the status of the named component.
func (s *Server) UpdateHealthCheck(component string, status string) {
	s.healthChecks.Update(component, status)
}

// SetReady sets the overall readiness state of the server.
func (s *Server) SetReady(ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = ready
}

// isReady returns the current readiness state.
func (s *Server) isReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ready
}

// handleHealth is the liveness handler. It always returns HTTP 200 to indicate
// that the process is alive.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	resp := map[string]string{
		"status":    "ok",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// handleReady is the readiness handler. It returns HTTP 200 when the server
// is marked ready and all health checks pass, otherwise HTTP 503.
func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	checks := s.healthChecks.All()
	allOK := s.isReady() && s.healthChecks.AllOK()

	status := "ok"
	code := http.StatusOK
	if !allOK {
		status = "unavailable"
		code = http.StatusServiceUnavailable
	}

	resp := map[string]interface{}{
		"status":    status,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"checks":    checks,
	}

	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(resp)
}
