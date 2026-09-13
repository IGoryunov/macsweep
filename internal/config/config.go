// Package config reads ~/.config/macsweep/config.yaml and merges it with
// command-line overrides and autodetected defaults.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/IGoryunov/macsweep/internal/analyze"
	"github.com/IGoryunov/macsweep/internal/rules"
)

// Config mirrors the YAML file.
type Config struct {
	ProjectRoots  []string       `yaml:"project_roots"`
	MinSize       string         `yaml:"min_size"`
	Workers       int            `yaml:"workers"`
	UnusedAppDays int            `yaml:"unused_app_days"`
	Analyzers     analyze.Config `yaml:"analyzers"`
}

// Overrides come from command-line flags; zero values mean "not set".
type Overrides struct {
	ProjectRoots []string
	MinSize      string
	Workers      int
}

// Resolved is the effective configuration.
type Resolved struct {
	ProjectRoots  []string
	MinSize       int64
	Workers       int
	UnusedAppDays int
	Analyzers     analyze.Config
}

// DefaultMinSize is used when neither flag nor config sets a threshold.
const DefaultMinSize = 50 << 20

// DefaultPath returns ~/.config/macsweep/config.yaml.
func DefaultPath(home string) string {
	return filepath.Join(home, ".config", "macsweep", "config.yaml")
}

// Load reads the config file. A missing file yields a zero Config and nil error.
func Load(path string) (Config, error) {
	var c Config
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("config: %w", err)
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("config %s: %w", path, err)
	}
	return c, nil
}

// Autodetect returns the conventional project directories that exist under home.
func Autodetect(home string) []string {
	var out []string
	for _, rel := range []string{"Work", "Projects", "projects", "src", "dev", "code", "repos", "go/src"} {
		p := filepath.Join(home, rel)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// DefaultWorkers is min(NumCPU*2, 16).
func DefaultWorkers() int {
	n := runtime.NumCPU() * 2
	if n > 16 {
		n = 16
	}
	if n < 1 {
		n = 1
	}
	return n
}

// Resolve merges flags over config over defaults.
func Resolve(cfg Config, ov Overrides, home string) (Resolved, error) {
	var r Resolved
	switch {
	case len(ov.ProjectRoots) > 0:
		r.ProjectRoots = expandAll(ov.ProjectRoots, home)
	case len(cfg.ProjectRoots) > 0:
		r.ProjectRoots = expandAll(cfg.ProjectRoots, home)
	default:
		r.ProjectRoots = Autodetect(home)
	}
	sizeStr := ov.MinSize
	if sizeStr == "" {
		sizeStr = cfg.MinSize
	}
	if sizeStr == "" {
		r.MinSize = DefaultMinSize
	} else {
		n, err := rules.ParseSize(sizeStr)
		if err != nil {
			return r, fmt.Errorf("min_size: %w", err)
		}
		r.MinSize = n
	}
	r.UnusedAppDays = cfg.UnusedAppDays
	if r.UnusedAppDays <= 0 {
		r.UnusedAppDays = analyze.DefaultUnusedAppDays
	}
	r.Analyzers = cfg.Analyzers
	r.Workers = ov.Workers
	if r.Workers <= 0 {
		r.Workers = cfg.Workers
	}
	if r.Workers <= 0 {
		r.Workers = DefaultWorkers()
	}
	return r, nil
}

func expandAll(ps []string, home string) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		if p == "~" || strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
		abs, err := filepath.Abs(p)
		if err == nil {
			p = abs
		}
		out = append(out, filepath.Clean(p))
	}
	return out
}
