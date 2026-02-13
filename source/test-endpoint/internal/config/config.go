// Package config handles loading and validation of the test-endpoint
// configuration from a YAML file with sensible defaults.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration for the test-endpoint.
type Config struct {
	Server      ServerConfig      `yaml:"server"`
	Behavior    BehaviorConfig    `yaml:"behavior"`
	Logging     LoggingConfig     `yaml:"logging"`
	Idempotency IdempotencyConfig `yaml:"idempotency"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port         int           `yaml:"port"`
	Path         string        `yaml:"path"`
	ReadTimeout  time.Duration `yaml:"readTimeout"`
	WriteTimeout time.Duration `yaml:"writeTimeout"`
}

// BehaviorConfig controls how the test endpoint responds to incoming events.
type BehaviorConfig struct {
	// Mode determines response behavior: "success", "failure", "delay", or "random".
	Mode string `yaml:"mode"`
	// FailureRate is the probability of failure when Mode is "random" (0.0-1.0).
	FailureRate float64 `yaml:"failureRate"`
	// DelayMs is the response delay in milliseconds when Mode is "delay".
	DelayMs int `yaml:"delayMs"`
	// StatusCode is the HTTP status code returned on failure.
	StatusCode int `yaml:"statusCode"`
}

// LoggingConfig controls log output.
type LoggingConfig struct {
	// Format is either "json" or "pretty".
	Format string `yaml:"format"`
	// Level is the minimum log level (e.g. "debug", "info", "warn", "error").
	Level string `yaml:"level"`
	// IncludeHeaders logs incoming HTTP headers when true.
	IncludeHeaders bool `yaml:"includeHeaders"`
	// IncludeBody logs the full request body when true.
	IncludeBody bool `yaml:"includeBody"`
}

// IdempotencyConfig controls duplicate event detection.
type IdempotencyConfig struct {
	// Enabled turns on idempotency checking via X-Event-ID.
	Enabled bool `yaml:"enabled"`
	// MaxTracked is the maximum number of event IDs held in memory.
	MaxTracked int `yaml:"maxTracked"`
}

// Defaults returns a Config populated with default values.
func Defaults() Config {
	return Config{
		Server: ServerConfig{
			Port:         8090,
			Path:         "/events",
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		Behavior: BehaviorConfig{
			Mode:        "success",
			FailureRate: 0.0,
			DelayMs:     0,
			StatusCode:  500,
		},
		Logging: LoggingConfig{
			Format:         "json",
			Level:          "info",
			IncludeHeaders: true,
			IncludeBody:    true,
		},
		Idempotency: IdempotencyConfig{
			Enabled:    true,
			MaxTracked: 10000,
		},
	}
}

// Load reads a YAML configuration file at the given path and returns a Config
// with any unset fields filled in from Defaults(). If path is empty the
// defaults are returned as-is.
func Load(path string) (Config, error) {
	cfg := Defaults()
	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("reading config file: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing config file: %w", err)
	}

	if err := validate(cfg); err != nil {
		return cfg, fmt.Errorf("validating config: %w", err)
	}

	return cfg, nil
}

func validate(cfg Config) error {
	switch cfg.Behavior.Mode {
	case "success", "failure", "delay", "random":
		// valid
	default:
		return fmt.Errorf("invalid behavior mode %q: must be success, failure, delay, or random", cfg.Behavior.Mode)
	}

	if cfg.Behavior.FailureRate < 0.0 || cfg.Behavior.FailureRate > 1.0 {
		return fmt.Errorf("failureRate must be between 0.0 and 1.0, got %f", cfg.Behavior.FailureRate)
	}

	switch cfg.Logging.Format {
	case "json", "pretty":
		// valid
	default:
		return fmt.Errorf("invalid logging format %q: must be json or pretty", cfg.Logging.Format)
	}

	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return fmt.Errorf("server port must be between 1 and 65535, got %d", cfg.Server.Port)
	}

	return nil
}
