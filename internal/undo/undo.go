// Package undo restores items from the Trash to their original location using
// the journal written by a --clean run. It is the only code in macsweep that
// moves anything out of the Trash, and it never overwrites: a target that
// exists again is skipped.
package undo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/IGoryunov/macsweep/internal/clean"
)

// Run summarises one journal file.
type Run struct {
	ID           string    `json:"id"` // file name without .json
	Kind         string    `json:"kind"`
	StartedAt    time.Time `json:"started_at"`
	Trashed      int       `json:"trashed"`
	TrashedBytes int64     `json:"trashed_bytes"`
	Restorable   int       `json:"restorable"` // items still present in the Trash
	Path         string    `json:"path"`
}

// ListRuns reads every journal in dir, newest first.
func ListRuns(dir string) ([]Run, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var runs []Run
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		j, err := Load(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // foreign or corrupt file: not ours to interpret
		}
		r := Run{ID: strings.TrimSuffix(e.Name(), ".json"), Kind: j.Kind, StartedAt: j.StartedAt, Trashed: j.Trashed, TrashedBytes: j.TrashedBytes, Path: filepath.Join(dir, e.Name())}
		if r.Kind == "" {
			r.Kind = "clean"
		}
		if r.Kind == "docker" {
			r.Trashed = j.Trashed
		}
		for _, it := range Restorable(j) {
			if _, err := os.Lstat(it.TrashedTo); err == nil {
				r.Restorable++
			}
		}
		runs = append(runs, r)
	}
	sort.Slice(runs, func(i, k int) bool { return runs[i].StartedAt.After(runs[k].StartedAt) })
	return runs, nil
}

// Load reads one journal file.
func Load(path string) (clean.Journal, error) {
	var j clean.Journal
	data, err := os.ReadFile(path)
	if err != nil {
		return j, err
	}
	if err := json.Unmarshal(data, &j); err != nil {
		return j, fmt.Errorf("%s: %w", path, err)
	}
	if j.Version != clean.JournalVersion {
		return j, fmt.Errorf("%s: unsupported journal version %d", path, j.Version)
	}
	return j, nil
}

// Resolve turns "last" or a run id into a journal path.
func Resolve(dir, id string) (string, error) {
	runs, err := ListRuns(dir)
	if err != nil {
		return "", err
	}
	if id == "last" || id == "" {
		for _, r := range runs {
			if r.Kind == "clean" {
				return r.Path, nil
			}
		}
		return "", fmt.Errorf("no clean runs recorded in %s", dir)
	}
	for _, r := range runs {
		if r.ID == id || strings.HasPrefix(r.ID, id) {
			return r.Path, nil
		}
	}
	return "", fmt.Errorf("run %q not found in %s", id, dir)
}

// Restorable returns the journal items that were actually moved to the Trash.
func Restorable(j clean.Journal) []clean.Outcome {
	var out []clean.Outcome
	for _, o := range j.Items {
		if o.Status == "trashed" && o.TrashedTo != "" {
			out = append(out, o)
		}
	}
	return out
}

// Plan selects items to restore: all restorable ones, or exactly the selected
// original paths.
func Plan(j clean.Journal, selected []string) ([]clean.Outcome, error) {
	all := Restorable(j)
	if len(selected) == 0 {
		return all, nil
	}
	var out []clean.Outcome
	for _, sel := range selected {
		sel = filepath.Clean(sel)
		found := false
		for _, o := range all {
			if o.Path == sel {
				out = append(out, o)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("--select %s: not a trashed item of this run", sel)
		}
	}
	return out, nil
}

// Execute moves each item back. Every move is guarded: the source must still
// be inside a Trash directory, the destination must be under home and must
// not exist, and parent directories are created as needed.
func Execute(ctx context.Context, items []clean.Outcome, home string) clean.Journal {
	j := clean.Journal{Version: clean.JournalVersion, Kind: "undo", StartedAt: time.Now(), Home: home}
	home = filepath.Clean(home)
	for _, it := range items {
		o := clean.Outcome{Path: it.Path, RuleID: it.RuleID, Verdict: it.Verdict, Bytes: it.Bytes, TrashedTo: it.TrashedTo}
		if ctx.Err() != nil {
			o.Status, o.Reason = "skipped", "cancelled"
			j.Skipped++
			j.Items = append(j.Items, o)
			continue
		}
		if reason := check(it, home); reason != "" {
			o.Status, o.Reason = "skipped", reason
			j.Skipped++
		} else if err := os.MkdirAll(filepath.Dir(it.Path), 0o755); err != nil {
			o.Status, o.Reason = "failed", err.Error()
			j.Failed++
		} else if err := os.Rename(it.TrashedTo, it.Path); err != nil {
			o.Status, o.Reason = "failed", err.Error()
			j.Failed++
		} else {
			o.Status = "restored"
			j.Trashed++ // reused counter: number of items moved
			j.TrashedBytes += it.Bytes
		}
		j.Items = append(j.Items, o)
	}
	j.FinishedAt = time.Now()
	return j
}

func check(it clean.Outcome, home string) string {
	if !strings.Contains(it.TrashedTo, "/.Trash") {
		return "journal source is not inside a Trash directory"
	}
	if _, err := os.Lstat(it.TrashedTo); err != nil {
		return "no longer in the Trash (emptied or moved by hand)"
	}
	dest := filepath.Clean(it.Path)
	if dest != home && !strings.HasPrefix(dest, home+string(filepath.Separator)) {
		return "destination is outside the home directory"
	}
	if _, err := os.Lstat(dest); err == nil {
		return "destination exists again (recreated by the application); not overwriting"
	}
	return ""
}
