package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testdataPath(name string) string {
	return filepath.Join("testdata", name)
}

func TestLoadValidConfig(t *testing.T) {
	cfg, err := Load(testdataPath("valid_config.yaml"))
	require.NoError(t, err)

	// App
	assert.Equal(t, "beacon", cfg.App.Name)
	assert.Equal(t, "1.0.0", cfg.App.Version)
	assert.Equal(t, "debug", cfg.App.LogLevel)
	assert.Equal(t, "json", cfg.App.LogFormat)

	// Resources
	require.Len(t, cfg.Resources, 2)
	assert.Equal(t, "maas.io/v1", cfg.Resources[0].APIVersion)
	assert.Equal(t, "Machine", cfg.Resources[0].Kind)
	assert.Equal(t, []string{"maas-system", "maas-production"}, cfg.Resources[0].Namespaces)
	assert.Equal(t, "v1", cfg.Resources[1].APIVersion)
	assert.Equal(t, "ConfigMap", cfg.Resources[1].Kind)

	// Annotation
	assert.Equal(t, "bakerapps.net.maas", cfg.Annotation.Key)
	assert.Equal(t, []string{"managed", "tracked"}, cfg.Annotation.Values)

	// Endpoint
	assert.Equal(t, "https://example.com/api/notify", cfg.Endpoint.URL)
	assert.Equal(t, "POST", cfg.Endpoint.Method)
	assert.Equal(t, 30*time.Second, cfg.Endpoint.Timeout.Duration)
	assert.Equal(t, 10, cfg.Endpoint.Retry.MaxAttempts)
	assert.Equal(t, 1*time.Second, cfg.Endpoint.Retry.InitialBackoff.Duration)
	assert.Equal(t, 5*time.Minute, cfg.Endpoint.Retry.MaxBackoff.Duration)
	assert.Equal(t, 2.0, cfg.Endpoint.Retry.BackoffMultiplier)
	assert.Equal(t, 0.1, cfg.Endpoint.Retry.Jitter)
	assert.Equal(t, "application/json", cfg.Endpoint.Headers["Content-Type"])
	assert.Equal(t, "beacon", cfg.Endpoint.Headers["X-Source"])
	assert.False(t, cfg.Endpoint.TLS.InsecureSkipVerify)
	assert.Equal(t, "/etc/ssl/certs/ca.pem", cfg.Endpoint.TLS.CAFile)

	// Worker
	assert.Equal(t, 5*time.Second, cfg.Worker.PollInterval.Duration)
	assert.Equal(t, 10, cfg.Worker.BatchSize)
	assert.Equal(t, 5, cfg.Worker.Concurrency)

	// Reconciliation
	assert.True(t, cfg.Reconciliation.Enabled)
	assert.Equal(t, 15*time.Minute, cfg.Reconciliation.Interval.Duration)
	assert.True(t, cfg.Reconciliation.OnStartup)
	assert.Equal(t, 10*time.Minute, cfg.Reconciliation.Timeout.Duration)

	// Retention
	assert.True(t, cfg.Retention.Enabled)
	assert.Equal(t, 1*time.Hour, cfg.Retention.CleanupInterval.Duration)
	assert.Equal(t, 48*time.Hour, cfg.Retention.RetentionPeriod.Duration)

	// Storage
	assert.Equal(t, 1*time.Minute, cfg.Storage.MonitorInterval.Duration)
	assert.Equal(t, "/data/events.db", cfg.Storage.DBPath)
	assert.Equal(t, "/data", cfg.Storage.VolumePath)
	assert.Equal(t, 80, cfg.Storage.WarningThreshold)
	assert.Equal(t, 90, cfg.Storage.CriticalThreshold)

	// Metrics
	assert.True(t, cfg.Metrics.Enabled)
	assert.Equal(t, 8080, cfg.Metrics.Port)
	assert.Equal(t, "/metrics", cfg.Metrics.Path)

	// Health
	assert.Equal(t, "/healthz", cfg.Health.LivenessPath)
	assert.Equal(t, "/ready", cfg.Health.ReadinessPath)
	assert.Equal(t, 8080, cfg.Health.Port)
}

func TestLoadMinimalConfigAppliesDefaults(t *testing.T) {
	cfg, err := Load(testdataPath("minimal_config.yaml"))
	require.NoError(t, err)

	// Endpoint URL was provided, should be kept.
	assert.Equal(t, "https://example.com/api/notify", cfg.Endpoint.URL)

	// Resources were provided.
	require.Len(t, cfg.Resources, 1)
	assert.Equal(t, "Machine", cfg.Resources[0].Kind)

	// All defaults should be applied.
	assert.Equal(t, "info", cfg.App.LogLevel)
	assert.Equal(t, "json", cfg.App.LogFormat)
	assert.Equal(t, "bakerapps.net.maas", cfg.Annotation.Key)
	assert.Equal(t, "POST", cfg.Endpoint.Method)
	assert.Equal(t, 30*time.Second, cfg.Endpoint.Timeout.Duration)
	assert.Equal(t, 10, cfg.Endpoint.Retry.MaxAttempts)
	assert.Equal(t, 1*time.Second, cfg.Endpoint.Retry.InitialBackoff.Duration)
	assert.Equal(t, 5*time.Minute, cfg.Endpoint.Retry.MaxBackoff.Duration)
	assert.Equal(t, 2.0, cfg.Endpoint.Retry.BackoffMultiplier)
	assert.Equal(t, 0.1, cfg.Endpoint.Retry.Jitter)
	assert.Equal(t, 5*time.Second, cfg.Worker.PollInterval.Duration)
	assert.Equal(t, 10, cfg.Worker.BatchSize)
	assert.Equal(t, 5, cfg.Worker.Concurrency)
	assert.True(t, cfg.Reconciliation.Enabled)
	assert.Equal(t, 15*time.Minute, cfg.Reconciliation.Interval.Duration)
	assert.True(t, cfg.Reconciliation.OnStartup)
	assert.Equal(t, 10*time.Minute, cfg.Reconciliation.Timeout.Duration)
	assert.True(t, cfg.Retention.Enabled)
	assert.Equal(t, 1*time.Hour, cfg.Retention.CleanupInterval.Duration)
	assert.Equal(t, 48*time.Hour, cfg.Retention.RetentionPeriod.Duration)
	assert.Equal(t, 1*time.Minute, cfg.Storage.MonitorInterval.Duration)
	assert.Equal(t, "/data/events.db", cfg.Storage.DBPath)
	assert.Equal(t, "/data", cfg.Storage.VolumePath)
	assert.Equal(t, 80, cfg.Storage.WarningThreshold)
	assert.Equal(t, 90, cfg.Storage.CriticalThreshold)
	assert.True(t, cfg.Metrics.Enabled)
	assert.Equal(t, 8080, cfg.Metrics.Port)
	assert.Equal(t, "/metrics", cfg.Metrics.Path)
	assert.Equal(t, "/healthz", cfg.Health.LivenessPath)
	assert.Equal(t, "/ready", cfg.Health.ReadinessPath)
	assert.Equal(t, 8080, cfg.Health.Port)
}

func TestLoadMissingEndpointURL(t *testing.T) {
	content := `
resources:
  - apiVersion: v1
    kind: ConfigMap
    namespaces:
      - default
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint.url is required")
}

func TestLoadMissingResources(t *testing.T) {
	content := `
endpoint:
  url: https://example.com/api/notify
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one resource must be configured")
}

func TestLoadMalformedYAML(t *testing.T) {
	content := `
this is: [not: valid yaml
  broken: {
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing config file")
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading config file")
}

func TestLoadInvalidLogLevel(t *testing.T) {
	content := `
app:
  logLevel: verbose
resources:
  - apiVersion: v1
    kind: Pod
    namespaces: [default]
endpoint:
  url: https://example.com/notify
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "app.logLevel must be one of")
}

func TestLoadInvalidLogFormat(t *testing.T) {
	content := `
app:
  logFormat: xml
resources:
  - apiVersion: v1
    kind: Pod
    namespaces: [default]
endpoint:
  url: https://example.com/notify
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "app.logFormat must be one of")
}

func TestEnvOverrideDBPath(t *testing.T) {
	t.Setenv("DB_PATH", "/override/events.db")

	cfg, err := Load(testdataPath("minimal_config.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "/override/events.db", cfg.Storage.DBPath)
}

func TestEnvOverrideEndpointURL(t *testing.T) {
	t.Setenv("ENDPOINT_URL", "https://override.example.com/notify")

	cfg, err := Load(testdataPath("minimal_config.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "https://override.example.com/notify", cfg.Endpoint.URL)
}

func TestEnvOverrideAuthToken(t *testing.T) {
	t.Setenv("ENDPOINT_AUTH_TOKEN", "secret-token-123")

	cfg, err := Load(testdataPath("minimal_config.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "secret-token-123", cfg.AuthToken)
}

func TestEnvOverrideEndpointURLValidation(t *testing.T) {
	// Config file has no endpoint URL, but env var provides it.
	content := `
resources:
  - apiVersion: v1
    kind: Pod
    namespaces: [default]
`
	path := writeTempConfig(t, content)

	t.Setenv("ENDPOINT_URL", "https://env-provided.example.com/notify")

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "https://env-provided.example.com/notify", cfg.Endpoint.URL)
}

func TestDurationUnmarshalYAML(t *testing.T) {
	content := `
resources:
  - apiVersion: v1
    kind: Pod
    namespaces: [default]
endpoint:
  url: https://example.com/notify
  timeout: 45s
worker:
  pollInterval: 10s
`
	path := writeTempConfig(t, content)
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, 45*time.Second, cfg.Endpoint.Timeout.Duration)
	assert.Equal(t, 10*time.Second, cfg.Worker.PollInterval.Duration)
}

func TestInvalidDurationValue(t *testing.T) {
	content := `
resources:
  - apiVersion: v1
    kind: Pod
    namespaces: [default]
endpoint:
  url: https://example.com/notify
  timeout: not-a-duration
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing config file")
}

// writeTempConfig writes the given YAML content to a temporary file and returns its path.
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(path, []byte(content), 0o644)
	require.NoError(t, err)
	return path
}
