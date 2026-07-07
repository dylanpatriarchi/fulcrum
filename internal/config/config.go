// Package config loads and validates Fulcrum's YAML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dylanpatriarchi/fulcrum/internal/balancer"
)

// Default values applied when an optional field is omitted from the file.
const (
	defaultHealthPath          = "/healthz"
	defaultHealthyThreshold    = 2
	defaultUnhealthyThreshold  = 3
	defaultHealthCheckInterval = 10 * time.Second
	defaultHealthCheckTimeout  = 2 * time.Second
	defaultRequestTimeout      = 30 * time.Second
	defaultBackendWeight       = 1
)

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

// rawConfig mirrors Config but represents defaultable fields as pointers so we
// can distinguish "field omitted" (nil → apply default) from an explicit zero
// value (kept as-is → rejected by Validate). Required fields (listen, strategy)
// stay plain: an omitted value becomes the zero value and validation rejects it.
type rawConfig struct {
	Listen      string         `yaml:"listen"`
	Strategy    string         `yaml:"strategy"`
	Backends    []rawBackend   `yaml:"backends"`
	HealthCheck rawHealthCheck `yaml:"health_check"`
	Proxy       rawProxy       `yaml:"proxy"`
}

type rawBackend struct {
	URL    string `yaml:"url"`
	Weight *int   `yaml:"weight"`
}

type rawHealthCheck struct {
	Interval           *Duration `yaml:"interval"`
	Timeout            *Duration `yaml:"timeout"`
	Path               string    `yaml:"path"`
	HealthyThreshold   *int      `yaml:"healthy_threshold"`
	UnhealthyThreshold *int      `yaml:"unhealthy_threshold"`
}

type rawProxy struct {
	// max_retries and max_in_flight_per_backend legitimately default to 0, so a
	// plain value (omitted == explicit 0) is correct here.
	RequestTimeout        *Duration `yaml:"request_timeout"`
	MaxRetries            int       `yaml:"max_retries"`
	MaxInFlightPerBackend int       `yaml:"max_in_flight_per_backend"`
	RetryNonIdempotent    bool      `yaml:"retry_non_idempotent"`
}

// resolve converts the parsed raw document into a Config, applying defaults for
// omitted fields while preserving explicit values (including invalid zeros) so
// Validate can report them.
func (r *rawConfig) resolve() *Config {
	c := &Config{
		Listen:   r.Listen,
		Strategy: r.Strategy,
		HealthCheck: HealthCheck{
			Interval:           orDuration(r.HealthCheck.Interval, defaultHealthCheckInterval),
			Timeout:            orDuration(r.HealthCheck.Timeout, defaultHealthCheckTimeout),
			Path:               orString(r.HealthCheck.Path, defaultHealthPath),
			HealthyThreshold:   orInt(r.HealthCheck.HealthyThreshold, defaultHealthyThreshold),
			UnhealthyThreshold: orInt(r.HealthCheck.UnhealthyThreshold, defaultUnhealthyThreshold),
		},
		Proxy: Proxy{
			RequestTimeout:        orDuration(r.Proxy.RequestTimeout, defaultRequestTimeout),
			MaxRetries:            r.Proxy.MaxRetries,
			MaxInFlightPerBackend: r.Proxy.MaxInFlightPerBackend,
			RetryNonIdempotent:    r.Proxy.RetryNonIdempotent,
		},
	}
	for _, rb := range r.Backends {
		c.Backends = append(c.Backends, Backend{
			URL:    rb.URL,
			Weight: orInt(rb.Weight, defaultBackendWeight),
		})
	}
	return c
}

func orInt(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

func orDuration(p *Duration, def time.Duration) Duration {
	if p == nil {
		return Duration(def)
	}
	return *p
}

func orString(s, def string) string {
	if s == "" {
		return def
	}
	return s
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
	var rc rawConfig
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true) // reject unknown keys — catches typos early.
	if err := dec.Decode(&rc); err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}
	c := rc.resolve()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Validate reports the first configuration error found, if any.
func (c *Config) Validate() error {
	if c.Listen == "" {
		return errors.New("config: listen address must not be empty")
	}
	// Delegate strategy and backend validity to the balancer package so its
	// registry and constructor are the single source of truth (no drift between
	// "accepted in config" and "actually implemented").
	if _, err := balancer.New(c.Strategy); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if len(c.Backends) == 0 {
		return errors.New("config: at least one backend is required")
	}
	for i, b := range c.Backends {
		if _, err := balancer.NewBackend(b.URL, b.Weight); err != nil {
			return fmt.Errorf("config: backend[%d]: %w", i, err)
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
