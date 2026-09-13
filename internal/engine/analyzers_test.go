package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IGoryunov/macsweep/internal/analyze"
	"github.com/IGoryunov/macsweep/internal/procs"
	"github.com/IGoryunov/macsweep/internal/rules"
	"github.com/IGoryunov/macsweep/internal/scan"
	"github.com/IGoryunov/macsweep/internal/testutil/fixture"
)

type stubAnalyzer struct {
	name     string
	findings []analyze.Finding
}

func (s stubAnalyzer) Name() string { return s.name }
func (s stubAnalyzer) Analyze(context.Context, analyze.Env) ([]analyze.Finding, []string) {
	return s.findings, []string{"stub ran"}
}

func TestAnalyzersInPipeline(t *testing.T) {
	home := fixture.Build(t, fixture.Tree{
		"Library/Caches/JetBrains/f": fixture.File{Size: 4096},
		"Library/Caches/Other/f":     fixture.File{Size: 4096},
		".ssh/x":                     fixture.File{Size: 4096},
	})
	set := loadRules(t, "version: 1\ngroup: Rules\nrules:\n  - id: jb\n    paths: ['$CA/JetBrains']\n    verdict: safe\n    note: from rule\n    recovery: r\n")
	mk := func(rel string, v rules.Verdict) analyze.Finding {
		return analyze.Finding{Path: filepath.Join(home, rel), Rule: rules.Rule{ID: "analyze/x", Group: "Stub", Verdict: v, Note: "from analyzer", Recovery: "r"}, Reasons: []string{"heuristic"}}
	}
	stub := stubAnalyzer{name: "Stub", findings: []analyze.Finding{
		mk("Library/Caches/JetBrains", rules.Review), // collides with the rule: rule wins
		mk("Library/Caches/Other", rules.Safe),
		mk(".ssh", rules.Safe), // policy denies
		{Path: "/Applications/Foo.app", Rule: rules.Rule{ID: "analyze/app", Group: "Stub", Verdict: rules.Safe, Note: "n", Recovery: "r"}}, // info root, non-keep → dropped (does not exist anyway)
		{Path: "docker://image/abc", Display: "image abc", Bytes: 5 << 20, Rule: rules.Rule{ID: "analyze/docker/image", Group: "Docker (advisor)", Verdict: rules.Keep, Note: "unused image", Recovery: "docker rmi abc"}},
	}}
	e := env(home, fakeApps{}, procs.FromList(nil))
	rep, _, err := Run(context.Background(), e, set, Options{Scanner: scan.New(scan.Options{}), Analyzers: []analyze.Analyzer{stub}})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, g := range rep.Groups {
		names = append(names, g.Name)
	}
	if strings.Join(names, ",") != "Rules,Stub,Docker (advisor)" {
		t.Fatalf("groups: %v", names)
	}
	jb := find(rep, "jb", "JetBrains")
	if jb == nil || jb.Note != "from rule" {
		t.Fatalf("rule must win over analyzer: %+v", jb)
	}
	if find(rep, "analyze/x", "JetBrains") != nil {
		t.Fatal("analyzer duplicate of a rule path must not appear")
	}
	other := find(rep, "analyze/x", "Other")
	if other == nil || other.Verdict != "safe" || !strings.Contains(strings.Join(other.Reasons, "|"), "heuristic") {
		t.Fatalf("analyzer candidate: %+v", other)
	}
	if find(rep, "analyze/x", ".ssh") != nil {
		t.Fatal("policy must still deny analyzer findings")
	}
	d := find(rep, "analyze/docker/image", "abc")
	if d == nil || d.Verdict != "keep" || d.Bytes != 5<<20 || d.DisplayPath != "image abc" {
		t.Fatalf("advisory: %+v", d)
	}
	if rep.Totals.KeepBytes != 5<<20 {
		t.Errorf("advisory bytes must count as keep: %d", rep.Totals.KeepBytes)
	}
	if !strings.Contains(strings.Join(rep.Warnings, "|"), "stub ran") {
		t.Errorf("analyzer warnings must surface: %v", rep.Warnings)
	}
	// --group filters analyzers too.
	rep, _, _ = Run(context.Background(), e, set, Options{Scanner: scan.New(scan.Options{}), Analyzers: []analyze.Analyzer{stub}, Groups: []string{"rules"}})
	if len(rep.Groups) != 1 {
		t.Errorf("group filter: %d groups", len(rep.Groups))
	}
}
