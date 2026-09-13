package undo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IGoryunov/macsweep/internal/clean"
	"github.com/IGoryunov/macsweep/internal/testutil/fixture"
)

func TestUndo(t *testing.T) {
	home := fixture.Build(t, fixture.Tree{
		".Trash/A/f":         fixture.File{Size: 10},
		".Trash/B/f":         fixture.File{Size: 10},
		".Trash/C/f":         fixture.File{Size: 10},
		"Library/Caches/B/x": fixture.File{Size: 10}, // recreated by its app
		".Trash/Outside/f":   fixture.File{Size: 10},
	})
	tr := func(rel string) string { return filepath.Join(home, ".Trash", rel) }
	j := clean.Journal{Version: clean.JournalVersion, Kind: "clean", StartedAt: time.Now().Add(-time.Hour), Home: home, Trashed: 4, TrashedBytes: 40, Items: []clean.Outcome{
		{Path: filepath.Join(home, "Library/Caches/A"), RuleID: "a", Verdict: "safe", Bytes: 10, Status: "trashed", TrashedTo: tr("A")},
		{Path: filepath.Join(home, "Library/Caches/B"), RuleID: "b", Verdict: "safe", Bytes: 10, Status: "trashed", TrashedTo: tr("B")},
		{Path: filepath.Join(home, ".cache/gone"), RuleID: "g", Verdict: "safe", Bytes: 10, Status: "trashed", TrashedTo: tr("Gone")},
		{Path: "/tmp/outside-" + filepath.Base(home), RuleID: "o", Verdict: "safe", Bytes: 10, Status: "trashed", TrashedTo: tr("Outside")},
		{Path: filepath.Join(home, "Library/Caches/S"), RuleID: "s", Verdict: "safe", Bytes: 10, Status: "skipped", Reason: "modified"},
	}}
	dir := clean.RunsDir(home)
	if _, err := clean.SaveJournal(dir, j); err != nil {
		t.Fatal(err)
	}
	// An older undo journal and a foreign file must not confuse the listing.
	if _, err := clean.SaveJournal(dir, clean.Journal{Version: clean.JournalVersion, Kind: "undo", StartedAt: time.Now().Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "junk.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	runs, err := ListRuns(dir)
	if err != nil || len(runs) != 2 || runs[0].Kind != "clean" || runs[0].Restorable != 3 || runs[1].Kind != "undo" {
		t.Fatalf("runs: %v %+v", err, runs)
	}
	path, err := Resolve(dir, "last")
	if err != nil || path != runs[0].Path {
		t.Fatalf("resolve last: %v %s", err, path)
	}
	if _, err := Resolve(dir, "nope"); err == nil {
		t.Fatal("unknown id must fail")
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Plan(loaded, []string{filepath.Join(home, "Library/Caches/S")}); err == nil {
		t.Fatal("selecting a non-trashed item must fail")
	}
	items, err := Plan(loaded, nil)
	if err != nil || len(items) != 4 {
		t.Fatalf("plan: %v %d", err, len(items))
	}

	res := Execute(context.Background(), items, home)
	st := map[string]string{}
	for _, o := range res.Items {
		st[o.RuleID] = o.Status + ":" + o.Reason
	}
	if st["a"] != "restored:" {
		t.Errorf("A: %s", st["a"])
	}
	if !strings.Contains(st["b"], "exists again") {
		t.Errorf("B: %s", st["b"])
	}
	if !strings.Contains(st["g"], "no longer in the Trash") {
		t.Errorf("gone: %s", st["g"])
	}
	if !strings.Contains(st["o"], "outside the home") {
		t.Errorf("outside: %s", st["o"])
	}
	if res.Kind != "undo" || res.Trashed != 1 || res.Skipped != 3 {
		t.Errorf("summary: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(home, "Library/Caches/A/f")); err != nil {
		t.Error("A not restored")
	}
	if _, err := os.Lstat(tr("A")); !os.IsNotExist(err) {
		t.Error("A still in Trash")
	}
	if _, err := os.Stat(filepath.Join(home, "Library/Caches/B/x")); err != nil {
		t.Error("B's recreated content must be untouched")
	}
	p, err := clean.SaveJournal(dir, res)
	if err != nil || !strings.HasSuffix(p, "-undo.json") {
		t.Fatalf("undo journal: %v %s", err, p)
	}
	runs, _ = ListRuns(dir)
	if runs[0].Kind != "undo" || runs[1].Restorable != 2 {
		t.Fatalf("after undo: %+v", runs)
	}
}
