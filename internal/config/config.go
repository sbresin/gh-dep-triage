// Package config loads the optional gh-dep-triage config file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

type Repos struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

type Policy struct {
	AllowMajor bool  `yaml:"allowMajor"`
	Repos      Repos `yaml:"repos"`
}

type Defaults struct {
	Team  string `yaml:"team"`
	Limit int    `yaml:"limit"`
}

type Config struct {
	Policy   Policy   `yaml:"policy"`
	Bots     []string `yaml:"bots"`
	Defaults Defaults `yaml:"defaults"`
}

// DefaultPath is $XDG_CONFIG_HOME/gh-dep-triage/config.yml, falling back to
// ~/.config/gh-dep-triage/config.yml.
func DefaultPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "gh-dep-triage", "config.yml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "gh-dep-triage", "config.yml")
}

// Load reads and validates the config at p. A missing file yields the zero
// Config unless required is set.
func Load(p string, required bool) (Config, error) {
	var cfg Config
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) && !required {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read config %s: %w", p, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("parse config %s: %w", p, err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config %s: %w", p, err)
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.Bots != nil && len(c.Bots) == 0 {
		return errors.New("bots must list at least one bot login")
	}
	for _, p := range append(append([]string{}, c.Policy.Repos.Allow...), c.Policy.Repos.Deny...) {
		if _, err := path.Match(p, ""); err != nil {
			return fmt.Errorf("bad repo pattern %q: %w", p, err)
		}
	}
	if c.Defaults.Limit < 0 {
		return errors.New("defaults.limit must not be negative")
	}
	return nil
}
