// Package analyze holds code-driven candidate producers that complement the
// YAML rules: heuristics that need to look at the machine (installed apps,
// Spotlight metadata, Docker) rather than at a fixed path list. Findings flow
// through the same policy, verdict, Trash and journal pipeline as rules.
package analyze

import (
	"context"
	"os/exec"
	"time"

	"github.com/IGoryunov/macsweep/internal/rules"
)

// AppInfo describes one installed application bundle.
type AppInfo struct {
	Name     string
	Path     string
	BundleID string
	TeamID   string
}

// AppLister is the application index as analyzers see it.
type AppLister interface {
	All() []AppInfo
	InstalledByName(name string) (bool, error)
	BundleRegistered(id string) (bool, error)
}

// ExecFunc runs an external command; injectable for tests.
type ExecFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

// DefaultExec runs the command with exec.CommandContext.
func DefaultExec(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// Env is what analyzers know about the machine.
type Env struct {
	Home          string
	Now           func() time.Time
	Apps          AppLister // nil when the index is unavailable
	MinSize       int64
	UnusedAppDays int
	Exec          ExecFunc
}

func (e Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e Env) exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	if e.Exec != nil {
		return e.Exec(ctx, name, args...)
	}
	return DefaultExec(ctx, name, args...)
}

// Finding is one proposed candidate.
type Finding struct {
	Path    string     // filesystem path, or docker://<kind>/<id> for advisories
	Rule    rules.Rule // synthetic rule: ID, Group, Verdict, Note, Recovery, MinSize
	Reasons []string   // appended after the rule default reason
	Display string     // display override for non-filesystem items
	Bytes   int64      // known size for non-filesystem items; 0 means "measure"
	Files   int64
	Tags    map[string]string // structured facts for UIs (age_bucket, source, last_used, command…)
}

// Analyzer produces findings.
type Analyzer interface {
	Name() string
	Analyze(ctx context.Context, env Env) (findings []Finding, warnings []string)
}

// Config switches analyzers on and off.
type Config struct {
	Caches    *bool `yaml:"caches"`
	Orphans   *bool `yaml:"orphans"`
	Apps      *bool `yaml:"apps"`
	Downloads *bool `yaml:"downloads"`
	Docker    *bool `yaml:"docker"`
}

func on(b *bool) bool { return b == nil || *b }

// Default returns the enabled analyzers in report order.
func Default(cfg Config) []Analyzer {
	var out []Analyzer
	if on(cfg.Caches) {
		out = append(out, Caches{})
	}
	if on(cfg.Orphans) {
		out = append(out, Orphans{})
	}
	if on(cfg.Apps) {
		out = append(out, UnusedApps{})
	}
	if on(cfg.Downloads) {
		out = append(out, Downloads{})
	}
	if on(cfg.Docker) {
		out = append(out, Docker{})
	}
	return out
}
