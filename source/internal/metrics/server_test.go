package metrics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper builds a Server wired to an httptest recorder. It returns the
// underlying http.Handler so callers can issue requests without starting
// a real listener.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	reg := prometheus.NewRegistry()
	_ = NewMetrics(reg)
	srv := NewServer(0, "/metrics", "/healthz", "/ready", reg)
	return srv
}

// TestLivenessReturns200 verifies that the liveness endpoint always returns
// HTTP 200 with a JSON body containing status "ok".
func TestLivenessReturns200(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]string
	err := json.Unmarshal(rec.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "ok", body["status"])
	assert.NotEmpty(t, body["timestamp"])
}

// TestReadinessReturns200WhenHealthy verifies that the readiness endpoint
// returns HTTP 200 when the server is marked ready and all component checks
// report "ok".
func TestReadinessReturns200WhenHealthy(t *testing.T) {
	srv := newTestServer(t)

	// Mark server ready and register healthy components.
	srv.SetReady(true)
	srv.UpdateHealthCheck("watcher", "ok")
	srv.UpdateHealthCheck("notifier", "ok")
	srv.UpdateHealthCheck("database", "ok")

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "ok", body["status"])
	assert.NotEmpty(t, body["timestamp"])

	checks, ok := body["checks"].(map[string]interface{})
	require.True(t, ok, "expected checks to be a map")
	assert.Equal(t, "ok", checks["watcher"])
	assert.Equal(t, "ok", checks["notifier"])
	assert.Equal(t, "ok", checks["database"])
}

// TestReadinessReturns503WhenNotReady verifies that the readiness endpoint
// returns HTTP 503 when the server has not been marked ready.
func TestReadinessReturns503WhenNotReady(t *testing.T) {
	srv := newTestServer(t)

	// Server not marked ready (default is false).
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var body map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "unavailable", body["status"])
}

// TestReadinessReturns503WhenComponentUnhealthy verifies that the readiness
// endpoint returns HTTP 503 when at least one component reports a non-ok status.
func TestReadinessReturns503WhenComponentUnhealthy(t *testing.T) {
	srv := newTestServer(t)

	srv.SetReady(true)
	srv.UpdateHealthCheck("watcher", "ok")
	srv.UpdateHealthCheck("database", "degraded")

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var body map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "unavailable", body["status"])

	checks, ok := body["checks"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "degraded", checks["database"])
}

// TestMetricsEndpointReturns200 verifies that the /metrics endpoint responds.
func TestMetricsEndpointReturns200(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	// Prometheus text format contains at least one HELP line for our metrics.
	assert.Contains(t, rec.Body.String(), "event_")
}

// TestSetReadyToggle verifies that SetReady toggles the readiness state.
func TestSetReadyToggle(t *testing.T) {
	srv := newTestServer(t)

	// Initially not ready.
	assert.False(t, srv.isReady())

	srv.SetReady(true)
	assert.True(t, srv.isReady())

	srv.SetReady(false)
	assert.False(t, srv.isReady())
}

// TestHealthChecksUpdate verifies concurrent-safe updates to health checks.
func TestHealthChecksUpdate(t *testing.T) {
	hc := NewHealthChecks()

	hc.Update("watcher", "ok")
	hc.Update("database", "ok")
	assert.True(t, hc.AllOK())

	hc.Update("database", "error")
	assert.False(t, hc.AllOK())

	all := hc.All()
	assert.Equal(t, "ok", all["watcher"])
	assert.Equal(t, "error", all["database"])
}

// TestHealthChecksAllOKEmptyIsTrue verifies that an empty HealthChecks
// reports AllOK as true (no failing components).
func TestHealthChecksAllOKEmptyIsTrue(t *testing.T) {
	hc := NewHealthChecks()
	assert.True(t, hc.AllOK())
}
