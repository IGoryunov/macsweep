// Package rules loads, validates and expands the declarative YAML rules that
// describe candidate directories for cleanup.
package rules

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Verdict is the engine's opinion about a candidate. Higher is more conservative.
type Verdict int

const (
	// Safe: the owning application recreates the data on its own.
	Safe Verdict = iota
	// Review: meaningful user data, a human decides.
	Review
	// Keep: never propose for removal; shown for information only.
	Keep
)

func (v Verdict) String() string {
	switch v {
	case Safe:
		return "safe"
	case Review:
		return "review"
	case Keep:
		return "keep"
	}
	return fmt.Sprintf("verdict(%d)", int(v))
}

// MarshalText implements encoding.TextMarshaler.
func (v Verdict) MarshalText() ([]byte, error) { return []byte(v.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (v *Verdict) UnmarshalText(b []byte) error {
	switch strings.ToLower(strings.TrimSpace(string(b))) {
	case "safe":
		*v = Safe
	case "review":
		*v = Review
	case "keep":
		*v = Keep
	default:
		return fmt.Errorf("unknown verdict %q (want safe, review or keep)", b)
	}
	return nil
}

// Max returns the more conservative of two verdicts.
func Max(a, b Verdict) Verdict {
	if a > b {
		return a
	}
	return b
}

// Rule is a declarative description of a cleanup candidate.
type Rule struct {
	ID       string
	Group    string
	Paths    []string // placeholder-prefixed literal paths
	Glob     string   // alternative to Paths
	Prune    bool     // glob only: do not descend into matches
	Exclude  []string // glob only: basenames to skip
	Verdict  Verdict
	When     []When
	Note     string
	Recovery string
	MinSize  int64 // bytes; 0 = inherit the global default
	OwnerApp string
}

// When is one conditional block. All of If and And must hold for it to fire.
type When struct {
	If     string
	Args   map[string]any
	And    []Cond
	Then   Verdict
	Reason string
}

// Cond is an extra predicate inside a When block.
type Cond struct {
	If   string
	Args map[string]any
}

// Set is the loaded, validated collection of rules.
type Set struct {
	Rules  []Rule
	Groups []string // in file order, unique
	byID   map[string]int
}

// ByID returns the rule with the given id.
func (s *Set) ByID(id string) (Rule, bool) {
	i, ok := s.byID[id]
	if !ok {
		return Rule{}, false
	}
	return s.Rules[i], true
}

var sizeRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([KMGT]?)I?B?$`)

// ParseSize parses "50MB", "1.5GB", "4096", "100k". Units are binary.
func ParseSize(s string) (int64, error) {
	m := sizeRe.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(s)))
	if m == nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", s, err)
	}
	mult := map[string]float64{"": 1, "K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}[m[2]]
	v := f * mult
	if v > math.MaxInt64 {
		return 0, fmt.Errorf("size %q too large", s)
	}
	return int64(v), nil
}
