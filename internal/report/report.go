// Package report defines the JSON output schema and text renderers.
package report

import (
	"fmt"
	"time"
)

// SchemaVersion of the JSON report.
const SchemaVersion = 1

// Candidate is one directory proposed by a rule.
type Candidate struct {
	RuleID      string            `json:"rule_id"`
	Path        string            `json:"path"`
	DisplayPath string            `json:"display_path"`
	Bytes       int64             `json:"bytes"`
	Files       int64             `json:"files"`
	ModTime     time.Time         `json:"mod_time"`      // newest change inside
	RootModTime time.Time         `json:"root_mod_time"` // of the directory entry itself, for the pre-trash check
	Dev         uint64            `json:"dev"`
	Ino         uint64            `json:"ino"`
	Verdict     string            `json:"verdict"`
	Reasons     []string          `json:"reasons"`
	Blocked     string            `json:"blocked"`
	Note        string            `json:"note"`
	Recovery    string            `json:"recovery"`
	OwnerApp    string            `json:"owner_app"`
	NestedIn    string            `json:"nested_in"`
	Errors      []string          `json:"errors,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"` // structured facts from analyzers (age_bucket, source, last_used, command…)
}

// Group is a named collection of candidates.
type Group struct {
	Name        string      `json:"name"`
	SafeBytes   int64       `json:"safe_bytes"`
	ReviewBytes int64       `json:"review_bytes"`
	KeepBytes   int64       `json:"keep_bytes"`
	Candidates  []Candidate `json:"candidates"`
}

// Totals are deduplicated sums over all groups.
type Totals struct {
	SafeBytes   int64 `json:"safe_bytes"`
	ReviewBytes int64 `json:"review_bytes"`
	KeepBytes   int64 `json:"keep_bytes"`
	Candidates  int   `json:"candidates"`
}

// Report is the top-level JSON document.
type Report struct {
	SchemaVersion int       `json:"schema_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	Home          string    `json:"home"`
	ProjectRoots  []string  `json:"project_roots"`
	FromCache     bool      `json:"from_cache"`
	Groups        []Group   `json:"groups"`
	Totals        Totals    `json:"totals"`
	Warnings      []string  `json:"warnings"`
}

// PredResult is one evaluated predicate, for --explain.
type PredResult struct {
	Expr   string `json:"expr"`
	Result *bool  `json:"result"`
	Err    string `json:"error,omitempty"`
}

// RuleMatch is one rule that applies to an explained path.
type RuleMatch struct {
	RuleID     string       `json:"rule_id"`
	Group      string       `json:"group"`
	By         string       `json:"matched_by"` // "paths" | "glob"
	Predicates []PredResult `json:"predicates"`
	Verdict    string       `json:"verdict"`
	Reasons    []string     `json:"reasons"`
	Blocked    string       `json:"blocked"`
	Note       string       `json:"note"`
	Recovery   string       `json:"recovery"`
}

// Explanation is the --explain output.
type Explanation struct {
	Path        string      `json:"path"`
	Resolved    string      `json:"resolved"`
	Allowed     bool        `json:"allowed"`
	Policy      string      `json:"policy"`
	Bytes       int64       `json:"bytes"`
	Files       int64       `json:"files"`
	MaxModTime  time.Time   `json:"max_mod_time"`
	Matches     []RuleMatch `json:"matches"`
	Verdict     string      `json:"verdict"` // most conservative across matches, "" if none
	Measurement string      `json:"measurement_note,omitempty"`
}

// HumanSize formats bytes with binary units.
func HumanSize(b int64) string {
	const unit = 1024
	switch {
	case b >= unit*unit*unit:
		return fmt.Sprintf("%.1f GB", float64(b)/(unit*unit*unit))
	case b >= unit*unit:
		return fmt.Sprintf("%.0f MB", float64(b)/(unit*unit))
	case b >= unit:
		return fmt.Sprintf("%.0f KB", float64(b)/unit)
	}
	return fmt.Sprintf("%d B", b)
}
