package tui

import (
	"testing"

	"github.com/IGoryunov/macsweep/internal/report"
)

func testReport() *report.Report {
	return &report.Report{Groups: []report.Group{
		{Name: "A", Candidates: []report.Candidate{
			{Path: "/h/a1", DisplayPath: "~/a1", Bytes: 100, Verdict: "safe"},
			{Path: "/h/a2", DisplayPath: "~/a2", Bytes: 300, Verdict: "review"},
			{Path: "/h/a3", DisplayPath: "~/a3", Bytes: 50, Verdict: "keep"},
			{Path: "/h/a1/x", DisplayPath: "~/a1/x", Bytes: 20, Verdict: "safe", NestedIn: "/h/a1"},
		}},
		{Name: "B", Candidates: []report.Candidate{
			{Path: "/h/b1", DisplayPath: "~/b1", Bytes: 10, Verdict: "safe", Blocked: "app Foo is running"},
		}},
	}}
}

func TestRowsAndSelection(t *testing.T) {
	rep := testReport()
	rows := buildRows(rep, nil, "")
	if len(rows) != 7 || !rows[0].header || rows[1].cand.Path != "/h/a2" || rows[0].bytes != 450 {
		t.Fatalf("rows: %+v", rows)
	}
	if got := buildRows(rep, map[string]bool{"A": true}, ""); len(got) != 3 {
		t.Errorf("collapsed: %d rows", len(got))
	}
	if got := buildRows(rep, nil, "b1"); len(got) != 2 || got[0].group != "B" {
		t.Errorf("filter: %+v", got)
	}
	sel := selection{}
	sel.toggle(&rep.Groups[0].Candidates[2]) // keep: ignored
	sel.toggle(&rep.Groups[1].Candidates[0]) // blocked: ignored
	if len(sel) != 0 {
		t.Fatal("keep/blocked must not be selectable")
	}
	sel.toggleGroup(rows, "A")
	if len(sel) != 3 || !sel["/h/a1"] || !sel["/h/a2"] || !sel["/h/a1/x"] {
		t.Fatalf("group toggle: %v", sel)
	}
	s := sel.summarize(rep)
	if s.count != 3 || s.review != 1 || s.bytes != 400 || s.byGroup["A"] != 400 || len(s.blockedApps) != 1 || s.blockedApps[0] != "Foo" {
		t.Fatalf("summary: %+v", s)
	}
	sel.toggleGroup(rows, "A")
	if len(sel) != 0 {
		t.Fatal("second group toggle must clear")
	}
}
