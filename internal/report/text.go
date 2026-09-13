package report

import (
	"fmt"
	"io"
	"strings"
)

type palette struct{ bold, dim, green, yellow, cyan, reset string }

func colors(on bool) palette {
	if !on {
		return palette{}
	}
	return palette{"\033[1m", "\033[2m", "\033[32m", "\033[33m", "\033[36m", "\033[0m"}
}

// Mark returns the verdict glyph: ✓ safe, ? review, - keep.
func Mark(verdict string) string {
	switch verdict {
	case "safe":
		return "✓"
	case "review":
		return "?"
	}
	return "-"
}

// WriteText renders the report in the prototype's style.
func WriteText(w io.Writer, r *Report, color bool) {
	c := colors(color)
	for _, g := range r.Groups {
		fmt.Fprintf(w, "\n%s%s%s%s  %s%s safe%s", c.bold, c.cyan, g.Name, c.reset, c.green, HumanSize(g.SafeBytes), c.reset)
		if g.ReviewBytes > 0 {
			fmt.Fprintf(w, "  %s/ %s to review%s", c.dim, HumanSize(g.ReviewBytes), c.reset)
		}
		if g.KeepBytes > 0 {
			fmt.Fprintf(w, "  %s/ %s keep%s", c.dim, HumanSize(g.KeepBytes), c.reset)
		}
		fmt.Fprintln(w)
		for _, cand := range g.Candidates {
			mark := Mark(cand.Verdict)
			mc := c.green
			if cand.Verdict != "safe" {
				mc = c.yellow
			}
			nested := ""
			if cand.NestedIn != "" {
				nested = c.dim + " (inside " + DisplayPath(cand.NestedIn, r.Home) + ")" + c.reset
			}
			fmt.Fprintf(w, "  %s%s%s %9s  %s%s\n", mc, mark, c.reset, HumanSize(cand.Bytes), cand.DisplayPath, nested)
			detail := cand.Note
			if len(cand.Reasons) > 1 {
				detail += " — " + strings.Join(cand.Reasons[1:], "; ")
			}
			if cand.Blocked != "" {
				detail += " — BLOCKED: " + cand.Blocked
			}
			if detail != "" {
				fmt.Fprintf(w, "    %9s  %s%s%s\n", "", c.dim, detail, c.reset)
			}
		}
	}
	fmt.Fprintf(w, "\n%s════════════════════════════════════════════%s\n", c.bold, c.reset)
	fmt.Fprintf(w, "  %s%s%12s%s  safe to trash (%s✓%s)\n", c.green, c.bold, HumanSize(r.Totals.SafeBytes), c.reset, c.green, c.reset)
	fmt.Fprintf(w, "  %s%12s%s  needs your decision (%s?%s)\n", c.yellow, HumanSize(r.Totals.ReviewBytes), c.reset, c.yellow, c.reset)
	if r.Totals.KeepBytes > 0 {
		fmt.Fprintf(w, "  %s%12s%s  shown for information only (-)\n", c.dim, HumanSize(r.Totals.KeepBytes), c.reset)
	}
	fmt.Fprintf(w, "%s════════════════════════════════════════════%s\n", c.bold, c.reset)
	for _, wmsg := range r.Warnings {
		fmt.Fprintf(w, "%swarning: %s%s\n", c.yellow, wmsg, c.reset)
	}
	if r.FromCache {
		fmt.Fprintf(w, "%s(sizes from cache; use --rescan to measure again)%s\n", c.dim, c.reset)
	}
	fmt.Fprintf(w, "%sNothing was changed.%s\n", c.dim, c.reset)
}

// WriteExplanation renders --explain output.
func WriteExplanation(w io.Writer, e *Explanation, home string) {
	fmt.Fprintf(w, "path:      %s\n", DisplayPath(e.Path, home))
	if e.Resolved != e.Path {
		fmt.Fprintf(w, "resolved:  %s\n", DisplayPath(e.Resolved, home))
	}
	if e.Allowed {
		fmt.Fprintf(w, "policy:    allowed\n")
	} else {
		fmt.Fprintf(w, "policy:    DENIED — %s\n", e.Policy)
	}
	if e.Measurement != "" {
		fmt.Fprintf(w, "size:      %s\n", e.Measurement)
	} else {
		fmt.Fprintf(w, "size:      %s in %d files, newest change %s\n", HumanSize(e.Bytes), e.Files, e.MaxModTime.Format("2006-01-02"))
	}
	if len(e.Matches) == 0 {
		fmt.Fprintln(w, "rules:     no rule matches this path")
		return
	}
	for _, m := range e.Matches {
		fmt.Fprintf(w, "\nrule %s (%s), matched by %s\n", m.RuleID, m.Group, m.By)
		fmt.Fprintf(w, "  note:     %s\n  recovery: %s\n", m.Note, m.Recovery)
		for _, p := range m.Predicates {
			switch {
			case p.Err != "":
				fmt.Fprintf(w, "  %-40s error: %s\n", p.Expr, p.Err)
			case p.Result != nil:
				fmt.Fprintf(w, "  %-40s %v\n", p.Expr, *p.Result)
			}
		}
		fmt.Fprintf(w, "  verdict:  %s %s\n", Mark(m.Verdict), m.Verdict)
		for _, r := range m.Reasons {
			fmt.Fprintf(w, "    - %s\n", r)
		}
		if m.Blocked != "" {
			fmt.Fprintf(w, "  blocked:  %s\n", m.Blocked)
		}
	}
	if e.Verdict != "" {
		fmt.Fprintf(w, "\nfinal verdict: %s %s\n", Mark(e.Verdict), e.Verdict)
	}
}

// WriteDiscovery renders --discover output.
func WriteDiscovery(w io.Writer, home string, homeBytes, homeFiles, threshold int64, rows []DiscoveryRow, color bool) {
	c := colors(color)
	fmt.Fprintf(w, "\n%s%s%s  %s in %d files\n", c.bold, "~", c.reset, HumanSize(homeBytes), homeFiles)
	fmt.Fprintf(w, "%sLargest directories above %s. unknown = no rule covers it yet, covered = already in the report, protected = never a candidate.%s\n\n", c.dim, HumanSize(threshold), c.reset)
	for _, r := range rows {
		tag, tc := r.Status, c.yellow
		switch r.Status {
		case "covered":
			tc = c.green
			tag = "covered by " + r.Detail
		case "protected":
			tc = c.dim
			tag = "protected: " + r.Detail
		case "application":
			tc = c.dim
			tag = "application bundle " + r.Detail + " (macsweep never removes applications)"
		case "remainder":
			tc = c.dim
			tag = "files directly here plus subdirectories below the threshold"
		}
		fmt.Fprintf(w, "  %9s  %s\n  %9s  %s%s%s\n", HumanSize(r.Bytes), r.DisplayPath, "", tc, tag, c.reset)
	}
	fmt.Fprintf(w, "\n%sNothing was changed.%s\n", c.dim, c.reset)
}

// DiscoveryRow is the renderer's view of a hotspot (kept here so report does
// not import engine).
type DiscoveryRow struct {
	DisplayPath string
	Bytes       int64
	Status      string
	Detail      string
	Remainder   bool
}
