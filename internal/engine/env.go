// Package engine turns rules and measurements into explained verdicts.
package engine

import (
	"log/slog"
	"time"

	"github.com/IGoryunov/macsweep/internal/analyze"
	"github.com/IGoryunov/macsweep/internal/policy"
	"github.com/IGoryunov/macsweep/internal/rules"
	"github.com/IGoryunov/macsweep/internal/scan"
)

// AppIndex answers installed-application queries. An error means "unknown".
type AppIndex interface {
	InstalledByName(name string) (bool, error)
	BundleRegistered(id string) (bool, error)
}

// ProcessList answers running-process queries. An error means "unknown".
type ProcessList interface {
	Running(name string) (bool, error)
}

// Env is everything the engine needs to know about the machine. All of it is
// injected so tests can substitute fakes.
type Env struct {
	Home         string
	ProjectRoots []string
	Now          func() time.Time
	Apps         AppIndex
	Procs        ProcessList
	Policy       *policy.Policy
	MinSize      int64
	Log          *slog.Logger

	UnusedAppDays int              // analyzers: threshold for "unused application"
	Exec          analyze.ExecFunc // analyzers: external command runner (nil = real)
}

func (e Env) rulesEnv() rules.Env { return rules.Env{Home: e.Home, ProjectRoots: e.ProjectRoots} }

func (e Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e Env) log() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.Default()
}

// Target is the candidate a predicate looks at.
type Target struct {
	Path   string
	Base   string
	Result scan.Result
}
