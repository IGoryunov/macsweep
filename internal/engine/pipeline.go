package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/IGoryunov/macsweep/internal/analyze"
	"github.com/IGoryunov/macsweep/internal/policy"
	"github.com/IGoryunov/macsweep/internal/report"
	"github.com/IGoryunov/macsweep/internal/rules"
	"github.com/IGoryunov/macsweep/internal/scan"
)

// Options tune a Run.
type Options struct {
	Groups    []string               // restrict to these groups (case-insensitive); empty = all
	Scanner   *scan.Scanner          // required
	Cached    map[string]scan.Result // measurements from the manifest, keyed by path
	Analyzers []analyze.Analyzer     // code-driven candidate producers, run after rules
}

// candidate is a (rule, resolved path) pair before evaluation.
type candidate struct {
	rule    rules.Rule
	path    string
	by      string
	reasons []string          // extra reasons from an analyzer
	tags    map[string]string // structured facts from an analyzer
}

// advisory is a non-filesystem finding (docker://…) that is reported as is.
type advisory struct {
	f analyze.Finding
}

type groupStats struct {
	pathsOnDisk int
	denied      int
	belowMin    int
	failed      int
	emitted     int
}

// Run executes the pipeline of spec §7.4 and returns the report plus the raw
// measurements (for the manifest cache).
func Run(ctx context.Context, env Env, set *rules.Set, o Options) (*report.Report, []scan.Result, error) {
	if o.Scanner == nil {
		return nil, nil, fmt.Errorf("engine: scanner is required")
	}
	rep := &report.Report{SchemaVersion: report.SchemaVersion, GeneratedAt: env.now(), Home: env.Home, ProjectRoots: env.ProjectRoots, Warnings: []string{}}
	active := selectRules(set, o.Groups)
	stats := map[string]*groupStats{}
	for _, r := range active {
		if stats[r.Group] == nil {
			stats[r.Group] = &groupStats{}
		}
	}

	cands, warnings := discover(ctx, env, active, o.Scanner, stats)
	rep.Warnings = append(rep.Warnings, warnings...)

	// Analyzers: same policy and dedupe path as rules; rules win on collisions.
	var advisories []advisory
	var analyzerGroups []string
	if len(o.Analyzers) > 0 {
		aenv := analyze.Env{Home: env.Home, Now: env.Now, MinSize: env.MinSize, UnusedAppDays: env.UnusedAppDays, Exec: env.Exec}
		if env.Apps != nil {
			if al, ok := env.Apps.(analyze.AppLister); ok {
				aenv.Apps = al
			}
		}
		want := map[string]bool{}
		for _, g := range o.Groups {
			want[strings.ToLower(g)] = true
		}
		for _, an := range o.Analyzers {
			if len(want) > 0 && !want[strings.ToLower(an.Name())] {
				continue
			}
			analyzerGroups = append(analyzerGroups, an.Name())
			if stats[an.Name()] == nil {
				stats[an.Name()] = &groupStats{}
			}
			findings, warns := an.Analyze(ctx, aenv)
			rep.Warnings = append(rep.Warnings, warns...)
			for _, f := range findings {
				if stats[f.Rule.Group] == nil {
					stats[f.Rule.Group] = &groupStats{}
					analyzerGroups = append(analyzerGroups, f.Rule.Group)
				}
				if strings.Contains(f.Path, "://") {
					advisories = append(advisories, advisory{f})
					continue
				}
				cands = considerFinding(env, cands, f, stats)
			}
		}
	}

	// Measure in parallel; the scanner's pool bounds the real work.
	results := make([]scan.Result, len(cands))
	errs := make([]error, len(cands))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i, c := range cands {
		if cached, ok := o.Cached[c.path]; ok {
			results[i] = cached
			rep.FromCache = true
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, path string) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i], errs[i] = o.Scanner.Measure(ctx, path)
		}(i, c.path)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}

	// Evaluate.
	type evaluated struct {
		c   candidate
		res scan.Result
		ev  Evaluation
	}
	var evs []evaluated
	var measured []scan.Result
	for i, c := range cands {
		if errs[i] != nil {
			stats[c.rule.Group].failed++
			env.log().Debug("measure failed", "path", c.path, "err", errs[i])
			continue
		}
		measured = append(measured, results[i])
		t := Target{Path: c.path, Base: filepath.Base(c.path), Result: results[i]}
		ev := Evaluate(ctx, env, c.rule, t)
		ev.Reasons = append(ev.Reasons, c.reasons...)
		if env.Policy != nil && env.Policy.InfoOnly(c.path) && ev.Verdict != rules.Keep {
			env.log().Debug("dropping non-keep candidate under info root", "path", c.path)
			continue
		}
		evs = append(evs, evaluated{c: c, res: results[i], ev: ev})
	}

	// Nesting and cross-candidate dedup (§6.3), deterministic by path order.
	sort.Slice(evs, func(i, j int) bool { return evs[i].c.path < evs[j].c.path })
	nestedIn := make([]string, len(evs))
	effective := make([]int64, len(evs))
	seen := map[scan.InodeKey]bool{}
	var outer []string
	for i, e := range evs {
		for j := len(outer) - 1; j >= 0; j-- {
			if strings.HasPrefix(e.c.path, outer[j]+"/") {
				nestedIn[i] = outer[j]
				break
			}
		}
		if nestedIn[i] != "" {
			continue
		}
		outer = append(outer, e.c.path)
		eff := e.res.Bytes
		for k, b := range e.res.Links {
			if seen[k] {
				eff -= b
			} else {
				seen[k] = true
			}
		}
		effective[i] = eff
	}

	// Build groups.
	byGroup := map[string]*report.Group{}
	for i, e := range evs {
		min := e.c.rule.MinSize
		if min == 0 {
			min = env.MinSize
		}
		if e.res.Bytes < min {
			stats[e.c.rule.Group].belowMin++
			continue
		}
		stats[e.c.rule.Group].emitted++
		g := byGroup[e.c.rule.Group]
		if g == nil {
			g = &report.Group{Name: e.c.rule.Group}
			byGroup[e.c.rule.Group] = g
		}
		cand := report.Candidate{
			RuleID: e.c.rule.ID, Path: e.c.path, DisplayPath: report.DisplayPath(e.c.path, env.Home),
			Bytes: e.res.Bytes, Files: e.res.Files, ModTime: e.res.MaxModTime,
			RootModTime: e.res.ModTime, Dev: e.res.Dev, Ino: e.res.Ino,
			Verdict: e.ev.Verdict.String(), Reasons: e.ev.Reasons, Blocked: e.ev.Blocked,
			Note: e.c.rule.Note, Recovery: e.c.rule.Recovery, OwnerApp: e.c.rule.OwnerApp,
			NestedIn: nestedIn[i], Errors: e.res.Errors, Tags: e.c.tags,
		}
		g.Candidates = append(g.Candidates, cand)
		if nestedIn[i] == "" {
			switch e.ev.Verdict {
			case rules.Safe:
				g.SafeBytes += effective[i]
				rep.Totals.SafeBytes += effective[i]
			case rules.Review:
				g.ReviewBytes += effective[i]
				rep.Totals.ReviewBytes += effective[i]
			case rules.Keep:
				g.KeepBytes += effective[i]
				rep.Totals.KeepBytes += effective[i]
			}
		}
		rep.Totals.Candidates++
	}
	// Advisories (docker://…) are reported as keep candidates without measurement or policy.
	for _, a := range advisories {
		f := a.f
		g := byGroup[f.Rule.Group]
		if g == nil {
			g = &report.Group{Name: f.Rule.Group}
			byGroup[f.Rule.Group] = g
		}
		reasons := append([]string{"rule default: " + f.Rule.Note}, f.Reasons...)
		g.Candidates = append(g.Candidates, report.Candidate{RuleID: f.Rule.ID, Path: f.Path, DisplayPath: f.Display, Bytes: f.Bytes, Files: f.Files,
			Verdict: rules.Keep.String(), Reasons: reasons, Note: f.Rule.Note, Recovery: f.Rule.Recovery, Tags: f.Tags})
		g.KeepBytes += f.Bytes
		rep.Totals.KeepBytes += f.Bytes
		rep.Totals.Candidates++
		stats[f.Rule.Group].emitted++
	}
	order := append(append([]string{}, set.Groups...), analyzerGroups...)
	for _, name := range order {
		g := byGroup[name]
		if g == nil {
			continue
		}
		sort.SliceStable(g.Candidates, func(i, j int) bool { return g.Candidates[i].Bytes > g.Candidates[j].Bytes })
		rep.Groups = append(rep.Groups, *g)
	}

	// Empty-group diagnostics (§7.5).
	for _, name := range order {
		st := stats[name]
		if st == nil || st.emitted > 0 || st.pathsOnDisk == 0 {
			continue
		}
		if st.denied == 0 && st.failed == 0 {
			// Only small paths: normal, not worth a warning.
			env.log().Debug("group produced no candidates", "group", name, "below_min_size", st.belowMin)
			continue
		}
		var parts []string
		if st.denied > 0 {
			parts = append(parts, fmt.Sprintf("denied by policy: %d", st.denied))
		}
		if st.belowMin > 0 {
			parts = append(parts, fmt.Sprintf("below min-size: %d", st.belowMin))
		}
		if st.failed > 0 {
			parts = append(parts, fmt.Sprintf("measurement failed: %d", st.failed))
		}
		reason := strings.Join(parts, ", ")
		if reason == "" {
			reason = "no candidates survived"
		}
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("group %s: %d path(s) exist on disk but produced no candidates (%s)", name, st.pathsOnDisk, reason))
	}
	return rep, measured, nil
}

func selectRules(set *rules.Set, groups []string) []rules.Rule {
	if len(groups) == 0 {
		return set.Rules
	}
	want := map[string]bool{}
	for _, g := range groups {
		want[strings.ToLower(g)] = true
	}
	var out []rules.Rule
	for _, r := range set.Rules {
		if want[strings.ToLower(r.Group)] {
			out = append(out, r)
		}
	}
	return out
}

// discover expands rules into concrete, policy-approved candidate paths.
func discover(ctx context.Context, env Env, active []rules.Rule, sc *scan.Scanner, stats map[string]*groupStats) ([]candidate, []string) {
	var warnings []string
	var cands []candidate
	seen := map[string]int{}
	renv := env.rulesEnv()

	consider := func(r rules.Rule, raw, by string) {
		resolved, err := filepath.EvalSymlinks(raw)
		if err != nil {
			return // does not exist or unreadable: the tool is not installed
		}
		stats[r.Group].pathsOnDisk++
		if d := env.Policy.Check(resolved); !d.Allowed {
			stats[r.Group].denied++
			env.log().Debug("denied by policy", "rule", r.ID, "path", resolved, "reason", d.Reason)
			return
		}
		if i, dup := seen[resolved]; dup {
			// Same directory reached by two rules: a literal path beats a glob;
			// otherwise the earlier rule wins. Never list the same path twice.
			if cands[i].by == "glob" && by == "paths" {
				cands[i] = candidate{rule: r, path: resolved, by: by}
			}
			return
		}
		seen[resolved] = len(cands)
		cands = append(cands, candidate{rule: r, path: resolved, by: by})
	}

	type rootQueries struct {
		queries []scan.Query
		rules   []rules.Rule
	}
	byRoot := map[string]*rootQueries{}
	var roots []string
	for _, r := range active {
		if len(r.Paths) > 0 {
			paths, w := rules.ExpandPaths(r, renv)
			warnings = append(warnings, w...)
			for _, p := range paths {
				if _, err := os.Lstat(p); err == nil {
					consider(r, p, "paths")
				}
			}
			continue
		}
		pats, w := rules.ExpandGlobs(r, renv)
		warnings = append(warnings, w...)
		for _, p := range pats {
			rq := byRoot[p.Root]
			if rq == nil {
				rq = &rootQueries{}
				byRoot[p.Root] = rq
				roots = append(roots, p.Root)
			}
			ex := map[string]bool{}
			for _, e := range r.Exclude {
				ex[e] = true
			}
			rq.queries = append(rq.queries, scan.Query{Pattern: p, Prune: r.Prune, Exclude: ex})
			rq.rules = append(rq.rules, r)
		}
	}
	stop := func(abs string) bool { return policy.DeniedName(filepath.Base(abs)) }
	sort.Strings(roots)
	for _, root := range roots {
		rq := byRoot[root]
		matches, err := sc.Expand(ctx, root, rq.queries, stop)
		if err != nil {
			return cands, warnings
		}
		for i, r := range rq.rules {
			for _, m := range matches[i] {
				consider(r, m, "glob")
			}
		}
	}
	return cands, warnings
}

// considerFinding adds an analyzer finding as a candidate unless the path is
// denied or already claimed by a rule (rules always win).
func considerFinding(env Env, cands []candidate, f analyze.Finding, stats map[string]*groupStats) []candidate {
	resolved, err := filepath.EvalSymlinks(f.Path)
	if err != nil {
		return cands
	}
	st := stats[f.Rule.Group]
	st.pathsOnDisk++
	if d := env.Policy.Check(resolved); !d.Allowed {
		st.denied++
		env.log().Debug("denied by policy", "rule", f.Rule.ID, "path", resolved, "reason", d.Reason)
		return cands
	}
	for _, c := range cands {
		if c.path == resolved {
			return cands
		}
	}
	return append(cands, candidate{rule: f.Rule, path: resolved, by: "analyzer", reasons: f.Reasons, tags: f.Tags})
}
