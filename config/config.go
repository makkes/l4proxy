// Package config provides types for configuring l4proxy and for reading the configuration from a file.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// APIVersion is the type used to represent different configuration API versions.
type APIVersion string

const (
	APIVersionV1 APIVersion = "v1"
)

// Config represents a full l4proxy configuration.
type Config struct {
	// APIVersion specifies the version of the configuration file used when it was persisted. This field doesn't
	// have any impact at the moment but provides forward-compatibility when a backwards-incompatible change has
	// to be applied to the configuration in which case this version is increased.
	APIVersion APIVersion `json:"api_version" yaml:"apiVersion"`
	Frontends  []Frontend `json:"frontends"   yaml:"frontends"`
}

// Frontend represents the configuration of a frontend and one or more backends.
type Frontend struct {
	Bind           string        `json:"bind"            yaml:"bind"`
	Backends       []Backend     `json:"backends"        yaml:"backends"`
	HealthInterval int           `json:"health_interval" yaml:"healthInterval"`
	Timeout        time.Duration `json:"timeout"         yaml:"timeout"`
}

// Backend represents the configuration of a single backend.
type Backend struct {
	Address string `json:"address" yaml:"address"`
}

// Read reads a [Config] from the given file. A non-nil error is returned when the file can't be opened or its format is unrecognized.
func Read(cfgPath string) (*Config, error) {
	//gosec:disable G304 -- cfgPath is provided by the caller and is expected to be a trusted configuration file path
	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read configuration file %q: %w", cfgPath, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(cfgBytes, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse configuration file %q: %w", cfgPath, err)
	}

	return &cfg, nil
}
