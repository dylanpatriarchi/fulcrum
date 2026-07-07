// Package config loads and validates Fulcrum's YAML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// knownStrategies is the set of balancing strategies accepted in config.
// The registry that instantiates them lives in the balancer package; this
// list keeps validation self-contained without importing it (avoids a cycle).
var knownStrategies = map[string]bool{
	"round-robin":       true,
	"weighted":          true,
	"least-connections": true,
	"random":            true,
	"ip-hash":           true,
}

// Config is the root configuration document.
type Config struct {
	Listen      string      `yaml:"listen"`
	Strategy    string      `yaml:"strategy"`
	Backends    []Backend   `yaml:"backends"`
	HealthCheck HealthCheck `yaml:"health_check"`
	Proxy       Proxy       `yaml:"proxy"`
}

// Backend is a single upstream target.
type Backend struct {
	URL    string `yaml:"url"`
	Weight int    `yaml:"weight"`
}

// HealthCheck configures active probing with hysteresis thresholds.
type HealthCheck struct {
	Interval           Duration `yaml:"interval"`
	Timeout            Duration `yaml:"timeout"`
	Path               string   `yaml:"path"`
	HealthyThreshold   int      `yaml:"healthy_threshold"`
	UnhealthyThreshold int      `yaml:"unhealthy_threshold"`
}

// Proxy configures request forwarding, timeouts and retry/failover behaviour.
type Proxy struct {
	RequestTimeout        Duration `yaml:"request_timeout"`
	MaxRetries            int      `yaml:"max_retries"`
	MaxInFlightPerBackend int      `yaml:"max_in_flight_per_backend"`
	RetryNonIdempotent    bool     `yaml:"retry_non_idempotent"`
}

// Load reads, parses and validates a configuration file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %q: %w", path, err)
	}
	return Parse(raw)
}

// Parse decodes and validates configuration from raw YAML bytes.
func Parse(raw []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true) // reject unknown keys — catches typos early.
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// applyDefaults fills unset optional fields with sensible values.
func (c *Config) applyDefaults() {
	for i := range c.Backends {
		if c.Backends[i].Weight == 0 {
			c.Backends[i].Weight = 1
		}
	}
	if c.HealthCheck.Path == "" {
		c.HealthCheck.Path = "/healthz"
	}
	if c.HealthCheck.HealthyThreshold == 0 {
		c.HealthCheck.HealthyThreshold = 2
	}
	if c.HealthCheck.UnhealthyThreshold == 0 {
		c.HealthCheck.UnhealthyThreshold = 3
	}
	if c.HealthCheck.Interval == 0 {
		c.HealthCheck.Interval = Duration(10 * time.Second)
	}
	if c.HealthCheck.Timeout == 0 {
		c.HealthCheck.Timeout = Duration(2 * time.Second)
	}
	if c.Proxy.RequestTimeout == 0 {
		c.Proxy.RequestTimeout = Duration(30 * time.Second)
	}
}

// Validate reports the first configuration error found, if any.
func (c *Config) Validate() error {
	if c.Listen == "" {
		return errors.New("config: listen address must not be empty")
	}
	if !knownStrategies[c.Strategy] {
		return fmt.Errorf("config: unknown strategy %q", c.Strategy)
	}
	if len(c.Backends) == 0 {
		return errors.New("config: at least one backend is required")
	}
	for i, b := range c.Backends {
		u, err := url.Parse(b.URL)
		if err != nil {
			return fmt.Errorf("config: backend[%d] url %q: %w", i, b.URL, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("config: backend[%d] url %q: scheme must be http or https", i, b.URL)
		}
		if u.Host == "" {
			return fmt.Errorf("config: backend[%d] url %q: missing host", i, b.URL)
		}
		if b.Weight < 1 {
			return fmt.Errorf("config: backend[%d] weight must be >= 1, got %d", i, b.Weight)
		}
	}
	if c.HealthCheck.Interval <= 0 {
		return errors.New("config: health_check.interval must be > 0")
	}
	if c.HealthCheck.Timeout <= 0 {
		return errors.New("config: health_check.timeout must be > 0")
	}
	if c.HealthCheck.HealthyThreshold < 1 {
		return errors.New("config: health_check.healthy_threshold must be >= 1")
	}
	if c.HealthCheck.UnhealthyThreshold < 1 {
		return errors.New("config: health_check.unhealthy_threshold must be >= 1")
	}
	if c.Proxy.RequestTimeout <= 0 {
		return errors.New("config: proxy.request_timeout must be > 0")
	}
	if c.Proxy.MaxRetries < 0 {
		return errors.New("config: proxy.max_retries must be >= 0")
	}
	if c.Proxy.MaxInFlightPerBackend < 0 {
		return errors.New("config: proxy.max_in_flight_per_backend must be >= 0")
	}
	return nil
}
