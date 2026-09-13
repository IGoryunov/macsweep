// Package procs snapshots running processes once so predicates can ask
// whether an application is running.
package procs

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Snapshot is a set of running process names.
type Snapshot struct {
	names map[string]bool
	err   error
}

// Snap runs `ps -axo comm=` once.
func Snap() *Snapshot {
	out, err := exec.Command("ps", "-axo", "comm=").Output()
	if err != nil {
		return &Snapshot{err: fmt.Errorf("ps: %w", err)}
	}
	return FromList(strings.Split(string(out), "\n"))
}

// FromList builds a snapshot from process command paths (for tests).
func FromList(comms []string) *Snapshot {
	s := &Snapshot{names: map[string]bool{}}
	for _, c := range comms {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		s.names[strings.ToLower(c)] = true
		s.names[strings.ToLower(filepath.Base(c))] = true
	}
	return s
}

// Failed returns a snapshot that reports err for every query.
func Failed(err error) *Snapshot { return &Snapshot{err: err} }

// Running reports whether a process with this name (basename or full path) exists.
func (s *Snapshot) Running(name string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	n := strings.ToLower(strings.TrimSpace(name))
	return s.names[n] || s.names[strings.ToLower(filepath.Base(n))], nil
}
