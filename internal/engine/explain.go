package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/IGoryunov/macsweep/internal/report"
	"github.com/IGoryunov/macsweep/internal/rules"
	"github.com/IGoryunov/macsweep/internal/scan"
)

// Explain runs the pipeline for one path and reports every step (spec §7.4).
// Min-size is ignored.
func Explain(ctx context.Context, env Env, set *rules.Set, sc *scan.Scanner, path string) (*report.Explanation, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(path, "~/") || path == "~" {
		abs = filepath.Join(env.Home, strings.TrimPrefix(path, "~"))
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		resolved = abs
	}
	ex := &report.Explanation{Path: abs, Resolved: resolved}
	d := env.Policy.Check(resolved)
	ex.Allowed, ex.Policy = d.Allowed, d.Reason

	var res scan.Result
	measured := false
	if _, err := os.Lstat(resolved); err != nil {
		ex.Measurement = "path does not exist"
	} else if !d.Allowed {
		ex.Measurement = "not measured (denied by policy)"
	} else if r, err := sc.Measure(ctx, resolved); err != nil {
		ex.Measurement = "measurement failed: " + err.Error()
	} else {
		res, measured = r, true
		ex.Bytes, ex.Files, ex.MaxModTime = r.Bytes, r.Files, r.MaxModTime
	}

	renv := env.rulesEnv()
	base := filepath.Base(resolved)
	var final *rules.Verdict
	for _, r := range set.Rules {
		by := matchRule(r, renv, abs, resolved, base)
		if by == "" {
			continue
		}
		m := report.RuleMatch{RuleID: r.ID, Group: r.Group, By: by, Note: r.Note, Recovery: r.Recovery}
		if measured {
			ev := Evaluate(ctx, env, r, Target{Path: resolved, Base: base, Result: res})
			m.Predicates, m.Verdict, m.Reasons, m.Blocked = ev.Preds, ev.Verdict.String(), ev.Reasons, ev.Blocked
			v := ev.Verdict
			if final == nil || v > *final {
				final = &v
			}
		} else {
			m.Verdict = r.Verdict.String()
			m.Reasons = []string{"rule default (not measured)"}
		}
		ex.Matches = append(ex.Matches, m)
	}
	if final != nil {
		ex.Verdict = final.String()
	}
	if len(ex.Matches) == 0 && ex.Measurement == "" {
		ex.Measurement = fmt.Sprintf("%s in %d files (no rule applies)", report.HumanSize(res.Bytes), res.Files)
	}
	return ex, nil
}

// matchRule reports how (if at all) a rule targets the path: "paths", "glob" or "".
func matchRule(r rules.Rule, renv rules.Env, abs, resolved, base string) string {
	if len(r.Paths) > 0 {
		paths, _ := rules.ExpandPaths(r, renv)
		for _, p := range paths {
			if p == abs || p == resolved {
				return "paths"
			}
			if rp, err := filepath.EvalSymlinks(p); err == nil && rp == resolved {
				return "paths"
			}
		}
		return ""
	}
	for _, e := range r.Exclude {
		if e == base {
			return ""
		}
	}
	pats, _ := rules.ExpandGlobs(r, renv)
	for _, p := range pats {
		for _, cand := range []string{abs, resolved} {
			if cand == p.Root && p.Literal() {
				return "glob"
			}
			if !strings.HasPrefix(cand, p.Root+"/") {
				continue
			}
			rel := strings.Split(strings.TrimPrefix(cand, p.Root+"/"), "/")
			if p.Match(rel) {
				return "glob"
			}
		}
	}
	return ""
}
