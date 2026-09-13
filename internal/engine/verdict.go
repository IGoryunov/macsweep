package engine

import (
	"context"
	"fmt"

	"github.com/IGoryunov/macsweep/internal/report"
	"github.com/IGoryunov/macsweep/internal/rules"
)

// Evaluation is the engine's decision about one candidate.
type Evaluation struct {
	Verdict rules.Verdict
	Reasons []string
	Blocked string
	Preds   []report.PredResult
}

// Evaluate applies a rule's when-blocks to a target (spec §7.3):
//
//  1. start from the rule's base verdict;
//  2. a block fires when all its predicates are true; a predicate error means
//     the block does not fire and marks the evaluation as uncertain;
//  3. if any block fired, the most conservative fired verdict wins, otherwise
//     the base verdict stays;
//  4. an uncertain evaluation is never below review.
func Evaluate(ctx context.Context, env Env, r rules.Rule, t Target) Evaluation {
	ev := Evaluation{Verdict: r.Verdict}
	if r.Note != "" {
		ev.Reasons = append(ev.Reasons, "rule default: "+r.Note)
	} else {
		ev.Reasons = append(ev.Reasons, "rule default")
	}
	fired, uncertain := false, false
	firedMax := rules.Safe
	var firedReasons []string
	for _, w := range r.When {
		conds := append([]rules.Cond{{If: w.If, Args: w.Args}}, w.And...)
		blockOK := true
		for _, c := range conds {
			v, err := evalExpr(ctx, env, t, c.If, c.Args)
			pr := report.PredResult{Expr: exprLabel(c)}
			if err != nil {
				pr.Err = err.Error()
				ev.Preds = append(ev.Preds, pr)
				name, _ := rules.SplitPredicate(c.If)
				ev.Reasons = append(ev.Reasons, fmt.Sprintf("predicate %s unavailable: %v", name, err))
				uncertain = true
				blockOK = false
				continue
			}
			vv := v
			pr.Result = &vv
			ev.Preds = append(ev.Preds, pr)
			if !v {
				blockOK = false
			}
		}
		if blockOK {
			fired = true
			firedMax = rules.Max(firedMax, w.Then)
			firedReasons = append(firedReasons, w.Reason)
		}
	}
	if fired {
		ev.Verdict = firedMax
		ev.Reasons = append(ev.Reasons, firedReasons...)
	}
	if uncertain && ev.Verdict < rules.Review {
		ev.Verdict = rules.Review
		ev.Reasons = append(ev.Reasons, "raised to review because a predicate was unavailable")
	}
	if r.OwnerApp != "" {
		if env.Procs == nil {
			ev.Reasons = append(ev.Reasons, "process check unavailable: "+errNoProcs.Error())
		} else if running, err := env.Procs.Running(r.OwnerApp); err != nil {
			ev.Reasons = append(ev.Reasons, "process check unavailable: "+err.Error())
		} else if running {
			ev.Blocked = fmt.Sprintf("app %s is running", r.OwnerApp)
		}
	}
	return ev
}

func exprLabel(c rules.Cond) string {
	if len(c.Args) == 0 {
		return c.If
	}
	return fmt.Sprintf("%s %v", c.If, c.Args)
}
