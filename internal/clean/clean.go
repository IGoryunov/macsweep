// Package clean turns a report into a trash plan, verifies every item right
// before it is moved, and writes a journal of what happened.
package clean

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/IGoryunov/macsweep/internal/policy"
	"github.com/IGoryunov/macsweep/internal/report"
	"github.com/IGoryunov/macsweep/internal/trash"
)

// Item is one candidate scheduled for the Trash.
type Item struct {
	Path        string    `json:"path"`
	RuleID      string    `json:"rule_id"`
	Group       string    `json:"group"`
	Verdict     string    `json:"verdict"`
	Bytes       int64     `json:"bytes"`
	Dev         uint64    `json:"dev"`
	Ino         uint64    `json:"ino"`
	RootModTime time.Time `json:"root_mod_time"`
	Blocked     string    `json:"blocked,omitempty"`
	NestedIn    string    `json:"nested_in,omitempty"`
}

// Plan is what --clean intends to do.
type Plan struct {
	Items      []Item   // to trash, sorted by path (outer before inner)
	Contained  []Item   // inside another planned item; go with it
	Blocked    []Item   // owner app running; never trashed
	Apps       []string // running owner apps, sorted
	TotalBytes int64    // bytes of Items (Contained excluded)
}

// BuildPlan selects candidates. With no selection every safe, unblocked
// candidate is planned. With a selection, exactly those paths are planned;
// keep candidates and unknown paths are errors.
func BuildPlan(rep *report.Report, selected []string) (Plan, error) {
	var plan Plan
	byPath := map[string]Item{}
	for _, g := range rep.Groups {
		for _, c := range g.Candidates {
			byPath[c.Path] = Item{Path: c.Path, RuleID: c.RuleID, Group: g.Name, Verdict: c.Verdict, Bytes: c.Bytes,
				Dev: c.Dev, Ino: c.Ino, RootModTime: c.RootModTime, Blocked: c.Blocked, NestedIn: c.NestedIn}
		}
	}
	var chosen []Item
	if len(selected) == 0 {
		for _, it := range byPath {
			if it.Verdict == "safe" {
				chosen = append(chosen, it)
			}
		}
	} else {
		for _, sel := range selected {
			it, ok := byPath[filepath.Clean(sel)]
			if !ok {
				return plan, fmt.Errorf("--select %s: not a candidate in this report (paths must match exactly)", sel)
			}
			if it.Verdict == "keep" {
				return plan, fmt.Errorf("--select %s: verdict is keep, macsweep never trashes it", sel)
			}
			chosen = append(chosen, it)
		}
	}
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].Path < chosen[j].Path })
	apps := map[string]bool{}
	var outer []string
	for _, it := range chosen {
		if it.Blocked != "" {
			plan.Blocked = append(plan.Blocked, it)
			if app := strings.TrimSuffix(strings.TrimPrefix(it.Blocked, "app "), " is running"); app != "" {
				apps[app] = true
			}
			continue
		}
		contained := false
		for _, o := range outer {
			if strings.HasPrefix(it.Path, o+"/") {
				contained = true
				break
			}
		}
		if contained {
			plan.Contained = append(plan.Contained, it)
			continue
		}
		outer = append(outer, it.Path)
		plan.Items = append(plan.Items, it)
		plan.TotalBytes += it.Bytes
	}
	for a := range apps {
		plan.Apps = append(plan.Apps, a)
	}
	sort.Strings(plan.Apps)
	return plan, nil
}

// Outcome records what happened to one item.
type Outcome struct {
	Path      string `json:"path"`
	RuleID    string `json:"rule_id"`
	Verdict   string `json:"verdict"`
	Bytes     int64  `json:"bytes"`
	Status    string `json:"status"` // trashed | contained | skipped | failed
	Reason    string `json:"reason,omitempty"`
	TrashedTo string `json:"trashed_to,omitempty"`
}

// Journal is the on-disk record of one --clean run.
type Journal struct {
	Version      int       `json:"version"`
	Kind         string    `json:"kind,omitempty"` // "" or "clean" for trash runs, "undo" for restores
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	Home         string    `json:"home"`
	Items        []Outcome `json:"items"`
	TrashedBytes int64     `json:"trashed_bytes"`
	Trashed      int       `json:"trashed"`
	Skipped      int       `json:"skipped"`
	Failed       int       `json:"failed"`
}

// JournalVersion is the current journal schema version.
const JournalVersion = 1

// Execute verifies and trashes every planned item, continuing past failures.
// Cancellation stops before the next item.
func Execute(ctx context.Context, plan Plan, t trash.Trasher, pol *policy.Policy, home string) Journal {
	j := Journal{Version: JournalVersion, Kind: "clean", StartedAt: time.Now(), Home: home}
	for _, it := range plan.Blocked {
		j.Items = append(j.Items, Outcome{Path: it.Path, RuleID: it.RuleID, Verdict: it.Verdict, Bytes: it.Bytes, Status: "skipped", Reason: it.Blocked})
		j.Skipped++
	}
	for _, it := range plan.Items {
		if ctx.Err() != nil {
			j.Items = append(j.Items, Outcome{Path: it.Path, RuleID: it.RuleID, Verdict: it.Verdict, Bytes: it.Bytes, Status: "skipped", Reason: "cancelled"})
			j.Skipped++
			continue
		}
		o := Outcome{Path: it.Path, RuleID: it.RuleID, Verdict: it.Verdict, Bytes: it.Bytes}
		if reason := verify(it, pol); reason != "" {
			o.Status, o.Reason = "skipped", reason
			j.Skipped++
		} else if dest, err := t.Trash(it.Path); err != nil {
			o.Status, o.Reason = "failed", err.Error()
			j.Failed++
		} else {
			o.Status, o.TrashedTo = "trashed", dest
			j.Trashed++
			j.TrashedBytes += it.Bytes
		}
		j.Items = append(j.Items, o)
	}
	for _, it := range plan.Contained {
		j.Items = append(j.Items, Outcome{Path: it.Path, RuleID: it.RuleID, Verdict: it.Verdict, Bytes: it.Bytes, Status: "contained", Reason: "inside " + it.NestedIn})
	}
	j.FinishedAt = time.Now()
	return j
}

// verify re-checks an item immediately before trashing (SPEC §7.7).
func verify(it Item, pol *policy.Policy) string {
	fi, err := os.Lstat(it.Path)
	if err != nil {
		return "no longer exists: " + err.Error()
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "cannot read inode information"
	}
	if uint64(st.Dev) != it.Dev || st.Ino != it.Ino {
		return "path was replaced since the scan (different inode)"
	}
	if fi.ModTime().After(it.RootModTime.Add(time.Second)) {
		return fmt.Sprintf("modified since the scan (%s > %s)", fi.ModTime().Format(time.RFC3339), it.RootModTime.Format(time.RFC3339))
	}
	resolved, err := filepath.EvalSymlinks(it.Path)
	if err != nil || resolved != it.Path {
		return "path no longer resolves to itself"
	}
	if d := pol.Check(resolved); !d.Allowed {
		return "denied by policy: " + d.Reason
	}
	if pol.InfoOnly(resolved) {
		return "application bundles are never trashed by macsweep"
	}
	return ""
}

// SaveJournal writes the journal into dir as <started_at>.json.
func SaveJournal(dir string, j Journal) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := j.StartedAt.UTC().Format("2006-01-02T15-04-05Z")
	if j.Kind == "undo" {
		name += "-undo"
	}
	path := filepath.Join(dir, name+".json")
	for i := 2; ; i++ { // two runs within one second must not overwrite each other
		if _, err := os.Lstat(path); err != nil {
			break
		}
		path = filepath.Join(dir, fmt.Sprintf("%s-%d.json", name, i))
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0o644)
}

// RunsDir is ~/Library/Caches/macsweep/runs.
func RunsDir(home string) string { return filepath.Join(home, "Library", "Caches", "macsweep", "runs") }
