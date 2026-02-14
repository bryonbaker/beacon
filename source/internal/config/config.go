// Package config handles loading, validating, and applying defaults to the
// beacon configuration. Configuration is read from a YAML file and
// may be overridden by environment variables.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a wrapper around time.Duration that implements yaml.Unmarshaler
// so that Go-style duration strings (e.g. "30s", "5m") can be used in YAML.
type Duration struct {
	time.Duration
}

// UnmarshalYAML parses a YAML scalar as a Go duration string.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = parsed
	return nil
}

// MarshalYAML serialises the duration back to a human-readable string.
func (d Duration) MarshalYAML() (interface{}, error) {
	return d.Duration.String(), nil
}

// PayloadConfig controls which annotations and labels are included in
// notification payloads. Empty slices give backward-compatible behaviour:
// all labels are sent and no annotations are sent.
type PayloadConfig struct {
	Annotations []string `yaml:"annotations"` // Annotation keys to include in payload
	Labels      []string `yaml:"labels"`       // Label keys to include (empty = all)
}

// CloudEventsConfig controls CloudEvents v1.0 envelope attributes.
type CloudEventsConfig struct {
	Source     string `yaml:"source"`     // URI-reference prefix for the "source" attribute
	TypePrefix string `yaml:"typePrefix"` // Reverse-DNS prefix for the "type" attribute
}

// Config is the top-level configuration for the beacon service.
type Config struct {
	App            AppConfig            `yaml:"app"`
	Resources      []ResourceConfig     `yaml:"resources"`
	Annotation     AnnotationConfig     `yaml:"annotation"`
	Payload        PayloadConfig        `yaml:"payload"`
	CloudEvents    CloudEventsConfig    `yaml:"cloudEvents"`
	Endpoint       EndpointConfig       `yaml:"endpoint"`
	Worker         WorkerConfig         `yaml:"worker"`
	Reconciliation ReconciliationConfig `yaml:"reconciliation"`
	Retention      RetentionConfig      `yaml:"retention"`
	Storage        StorageConfig        `yaml:"storage"`
	Metrics        MetricsConfig        `yaml:"metrics"`
	Health         HealthConfig         `yaml:"health"`

	// AuthToken is populated from the ENDPOINT_AUTH_TOKEN environment variable.
	// It is never read from the config file.
	AuthToken string `yaml:"-"`
}

// AppConfig holds general application settings.
type AppConfig struct {
	Name      string `yaml:"name"`
	Version   string `yaml:"version"`
	LogLevel  string `yaml:"logLevel"`
	LogFormat string `yaml:"logFormat"`
}

// ResourceConfig describes a single Kubernetes resource type to watch.
type ResourceConfig struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Resource   string   `yaml:"resource"`
	Namespaces []string `yaml:"namespaces"`
}

// AnnotationConfig specifies the annotation key and accepted values used
// to filter which resources are tracked.
type AnnotationConfig struct {
	Key    string   `yaml:"key"`
	Values []string `yaml:"values"`
}

// EndpointConfig configures the HTTP endpoint that receives notifications.
type EndpointConfig struct {
	URL     string            `yaml:"url"`
	Method  string            `yaml:"method"`
	Timeout Duration          `yaml:"timeout"`
	Retry   RetryConfig       `yaml:"retry"`
	Headers map[string]string `yaml:"headers"`
	TLS     TLSConfig         `yaml:"tls"`
}

// RetryConfig controls the retry behaviour for endpoint calls.
type RetryConfig struct {
	MaxAttempts       int      `yaml:"maxAttempts"`
	InitialBackoff    Duration `yaml:"initialBackoff"`
	MaxBackoff        Duration `yaml:"maxBackoff"`
	BackoffMultiplier float64  `yaml:"backoffMultiplier"`
	Jitter            float64  `yaml:"jitter"`
}

// TLSConfig holds TLS-related settings for the notification endpoint.
type TLSConfig struct {
	InsecureSkipVerify bool   `yaml:"insecureSkipVerify"`
	CAFile             string `yaml:"caFile"`
}

// WorkerConfig controls the notification worker pool.
type WorkerConfig struct {
	PollInterval Duration `yaml:"pollInterval"`
	BatchSize    int      `yaml:"batchSize"`
	Concurrency  int      `yaml:"concurrency"`
}

// ReconciliationConfig controls the periodic reconciliation loop.
type ReconciliationConfig struct {
	Enabled   bool     `yaml:"enabled"`
	Interval  Duration `yaml:"interval"`
	OnStartup bool     `yaml:"onStartup"`
	Timeout   Duration `yaml:"timeout"`
}

// RetentionConfig controls old-record cleanup.
type RetentionConfig struct {
	Enabled         bool     `yaml:"enabled"`
	CleanupInterval Duration `yaml:"cleanupInterval"`
	RetentionPeriod Duration `yaml:"retentionPeriod"`
}

// StorageConfig controls the SQLite database and volume monitoring.
type StorageConfig struct {
	MonitorInterval    Duration `yaml:"monitorInterval"`
	DBPath             string   `yaml:"dbPath"`
	VolumePath         string   `yaml:"volumePath"`
	WarningThreshold   int      `yaml:"warningThreshold"`
	CriticalThreshold  int      `yaml:"criticalThreshold"`
}

// MetricsConfig controls the Prometheus metrics endpoint.
type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Port    int    `yaml:"port"`
	Path    string `yaml:"path"`
}

// HealthConfig controls the health/readiness probe endpoints.
type HealthConfig struct {
	LivenessPath  string `yaml:"livenessPath"`
	ReadinessPath string `yaml:"readinessPath"`
	Port          int    `yaml:"port"`
}

// Load reads the YAML configuration file at path, applies defaults, applies
// environment-variable overrides, and validates the result.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	cfg.applyDefaults()
	cfg.applyEnvOverrides()

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return cfg, nil
}

// applyDefaults fills in zero-valued fields with sensible defaults.
func (c *Config) applyDefaults() {
	// App defaults
	if c.App.LogLevel == "" {
		c.App.LogLevel = "info"
	}
	if c.App.LogFormat == "" {
		c.App.LogFormat = "json"
	}

	// Annotation defaults
	if c.Annotation.Key == "" {
		c.Annotation.Key = "bakerapps.net.maas"
	}

	// CloudEvents defaults
	if c.CloudEvents.Source == "" {
		c.CloudEvents.Source = "/beacon"
	}
	if c.CloudEvents.TypePrefix == "" {
		c.CloudEvents.TypePrefix = "net.bakerapps.beacon.resource"
	}

	// Endpoint defaults
	if c.Endpoint.Method == "" {
		c.Endpoint.Method = "POST"
	}
	if c.Endpoint.Timeout.Duration == 0 {
		c.Endpoint.Timeout.Duration = 30 * time.Second
	}

	// Retry defaults
	if c.Endpoint.Retry.MaxAttempts == 0 {
		c.Endpoint.Retry.MaxAttempts = 10
	}
	if c.Endpoint.Retry.InitialBackoff.Duration == 0 {
		c.Endpoint.Retry.InitialBackoff.Duration = 1 * time.Second
	}
	if c.Endpoint.Retry.MaxBackoff.Duration == 0 {
		c.Endpoint.Retry.MaxBackoff.Duration = 5 * time.Minute
	}
	if c.Endpoint.Retry.BackoffMultiplier == 0 {
		c.Endpoint.Retry.BackoffMultiplier = 2.0
	}
	if c.Endpoint.Retry.Jitter == 0 {
		c.Endpoint.Retry.Jitter = 0.1
	}

	// Worker defaults
	if c.Worker.PollInterval.Duration == 0 {
		c.Worker.PollInterval.Duration = 5 * time.Second
	}
	if c.Worker.BatchSize == 0 {
		c.Worker.BatchSize = 10
	}
	if c.Worker.Concurrency == 0 {
		c.Worker.Concurrency = 5
	}

	// Reconciliation defaults - use a pointer-like approach for booleans
	// Since Go zero-value for bool is false, we always default Enabled and
	// OnStartup to true when the YAML did not explicitly set them. Callers
	// who want to disable these must set them to false in the YAML.
	// We use a simple heuristic: if the entire Reconciliation section was
	// omitted (Interval is zero), we apply all defaults including Enabled.
	if c.Reconciliation.Interval.Duration == 0 {
		c.Reconciliation.Enabled = true
		c.Reconciliation.OnStartup = true
		c.Reconciliation.Interval.Duration = 15 * time.Minute
		c.Reconciliation.Timeout.Duration = 10 * time.Minute
	} else {
		// Section was provided but some fields may be missing.
		if c.Reconciliation.Timeout.Duration == 0 {
			c.Reconciliation.Timeout.Duration = 10 * time.Minute
		}
	}

	// Retention defaults
	if c.Retention.CleanupInterval.Duration == 0 {
		c.Retention.Enabled = true
		c.Retention.CleanupInterval.Duration = 1 * time.Hour
		c.Retention.RetentionPeriod.Duration = 48 * time.Hour
	} else {
		if c.Retention.RetentionPeriod.Duration == 0 {
			c.Retention.RetentionPeriod.Duration = 48 * time.Hour
		}
	}

	// Storage defaults
	if c.Storage.MonitorInterval.Duration == 0 {
		c.Storage.MonitorInterval.Duration = 1 * time.Minute
	}
	if c.Storage.DBPath == "" {
		c.Storage.DBPath = "/data/events.db"
	}
	if c.Storage.VolumePath == "" {
		c.Storage.VolumePath = "/data"
	}
	if c.Storage.WarningThreshold == 0 {
		c.Storage.WarningThreshold = 80
	}
	if c.Storage.CriticalThreshold == 0 {
		c.Storage.CriticalThreshold = 90
	}

	// Metrics defaults
	if c.Metrics.Port == 0 {
		c.Metrics.Enabled = true
		c.Metrics.Port = 8080
		c.Metrics.Path = "/metrics"
	} else {
		if c.Metrics.Path == "" {
			c.Metrics.Path = "/metrics"
		}
	}

	// Health defaults
	if c.Health.LivenessPath == "" {
		c.Health.LivenessPath = "/healthz"
	}
	if c.Health.ReadinessPath == "" {
		c.Health.ReadinessPath = "/ready"
	}
	if c.Health.Port == 0 {
		c.Health.Port = 8080
	}
}

// applyEnvOverrides applies environment variable overrides to the configuration.
func (c *Config) applyEnvOverrides() {
	if v := os.Getenv("DB_PATH"); v != "" {
		c.Storage.DBPath = v
	}
	if v := os.Getenv("ENDPOINT_AUTH_TOKEN"); v != "" {
		c.AuthToken = v
	}
	if v := os.Getenv("ENDPOINT_URL"); v != "" {
		c.Endpoint.URL = v
	}
}

// validate checks that all required fields are populated and that enum values
// are within the allowed set.
func (c *Config) validate() error {
	if c.Endpoint.URL == "" {
		return fmt.Errorf("endpoint.url is required")
	}
	if len(c.Resources) == 0 {
		return fmt.Errorf("at least one resource must be configured")
	}

	// Validate log level
	switch c.App.LogLevel {
	case "debug", "info", "warn", "error":
		// valid
	default:
		return fmt.Errorf("app.logLevel must be one of: debug, info, warn, error; got %q", c.App.LogLevel)
	}

	// Validate log format
	switch c.App.LogFormat {
	case "json", "text":
		// valid
	default:
		return fmt.Errorf("app.logFormat must be one of: json, text; got %q", c.App.LogFormat)
	}

	// Validate endpoint method
	switch c.Endpoint.Method {
	case "POST", "PUT", "PATCH":
		// valid
	default:
		return fmt.Errorf("endpoint.method must be one of: POST, PUT, PATCH; got %q", c.Endpoint.Method)
	}

	return nil
}
