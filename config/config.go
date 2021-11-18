package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type APIVersion string

const (
	APIVersionV1 APIVersion = "v1"
)

type Config struct {
	// APIVersion specifies the version of the configuration file used when it was persisted. This field doesn't
	// have any impact at the moment but provides forward-compatibility when a backwards-incompatible change has
	// to be applied to the configuration in which case this version is increased.
	APIVersion APIVersion `yaml:"apiVersion"`
	Frontends  []Frontend `yaml:"frontends"`
}

type Frontend struct {
	Bind           string    `yaml:"bind"`
	Backends       []Backend `yaml:"backends"`
	HealthInterval int       `yaml:"healthInterval"`
}

type Backend struct {
	Address string `yaml:"address"`
}

func Read(cfgPath string) (*Config, error) {
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
