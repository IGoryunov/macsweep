// Package tui is the interactive Bubble Tea front end. It only consumes the
// core packages (engine, clean, report, scan) and is never imported by them.
package tui

import (
	"sort"
	"strings"

	"github.com/IGoryunov/macsweep/internal/report"
)

// row is one visible line in the results list: a group header or a candidate.
type row struct {
	group  string
	header bool
	cand   *report.Candidate // nil for headers
	count  int               // header: candidates in group (after filter)
	bytes  int64             // header: group bytes shown (sum of non-nested)
}

// selectable reports whether a candidate may be marked for the Trash.
func selectable(c *report.Candidate) bool {
	return c != nil && c.Verdict != "keep" && c.Blocked == ""
}

// buildRows flattens the report into rows, honouring collapsed groups and the
// filter (case-insensitive substring on the display path, note or rule id).
func buildRows(rep *report.Report, collapsed map[string]bool, filter string) []row {
	f := strings.ToLower(strings.TrimSpace(filter))
	var rows []row
	for gi := range rep.Groups {
		g := &rep.Groups[gi]
		var cands []*report.Candidate
		for ci := range g.Candidates {
			c := &g.Candidates[ci]
			if f != "" && !strings.Contains(strings.ToLower(c.DisplayPath+" "+c.Note+" "+c.RuleID), f) {
				continue
			}
			cands = append(cands, c)
		}
		if len(cands) == 0 {
			continue
		}
		sort.SliceStable(cands, func(i, j int) bool { return cands[i].Bytes > cands[j].Bytes })
		var sum int64
		for _, c := range cands {
			if c.NestedIn == "" {
				sum += c.Bytes
			}
		}
		rows = append(rows, row{group: g.Name, header: true, count: len(cands), bytes: sum})
		if collapsed[g.Name] {
			continue
		}
		for _, c := range cands {
			rows = append(rows, row{group: g.Name, cand: c})
		}
	}
	return rows
}

// selection is the set of marked candidate paths.
type selection map[string]bool

func (s selection) toggle(c *report.Candidate) {
	if !selectable(c) {
		return
	}
	if s[c.Path] {
		delete(s, c.Path)
	} else {
		s[c.Path] = true
	}
}

// toggleGroup marks every selectable candidate of the group, or clears them
// all when every selectable one is already marked.
func (s selection) toggleGroup(rows []row, group string) {
	all := true
	var cands []*report.Candidate
	for _, r := range rows {
		if r.header || r.group != group || !selectable(r.cand) {
			continue
		}
		cands = append(cands, r.cand)
		if !s[r.cand.Path] {
			all = false
		}
	}
	for _, c := range cands {
		if all {
			delete(s, c.Path)
		} else {
			s[c.Path] = true
		}
	}
}

// summary totals the selection, excluding candidates nested inside another
// selected one so the number matches what will actually be freed.
type summary struct {
	count, review int
	bytes         int64
	byGroup       map[string]int64
	reviewItems   []*report.Candidate
	blockedApps   []string
}

func (s selection) summarize(rep *report.Report) summary {
	sum := summary{byGroup: map[string]int64{}}
	apps := map[string]bool{}
	for gi := range rep.Groups {
		g := &rep.Groups[gi]
		for ci := range g.Candidates {
			c := &g.Candidates[ci]
			if !s[c.Path] {
				continue
			}
			sum.count++
			if c.Verdict == "review" {
				sum.review++
				sum.reviewItems = append(sum.reviewItems, c)
			}
			if c.NestedIn != "" && s[c.NestedIn] {
				continue
			}
			sum.bytes += c.Bytes
			sum.byGroup[g.Name] += c.Bytes
		}
		for ci := range g.Candidates {
			c := &g.Candidates[ci]
			if c.Blocked != "" && strings.HasPrefix(c.Blocked, "app ") {
				apps[strings.TrimSuffix(strings.TrimPrefix(c.Blocked, "app "), " is running")] = true
			}
		}
	}
	for a := range apps {
		sum.blockedApps = append(sum.blockedApps, a)
	}
	sort.Strings(sum.blockedApps)
	return sum
}
