package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that (un)marshals from a Go duration string
// (e.g. "10s", "500ms", "1m30s") in YAML.
type Duration time.Duration

// UnmarshalYAML decodes a duration string such as "2s" into a Duration.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("duration: expected a string like \"2s\": %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("duration: invalid value %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// MarshalYAML renders the Duration back to its canonical string form.
func (d Duration) MarshalYAML() (any, error) {
	return d.String(), nil
}

// Std returns the value as a standard library time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// String implements fmt.Stringer.
func (d Duration) String() string { return time.Duration(d).String() }
