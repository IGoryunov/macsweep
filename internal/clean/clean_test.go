package clean

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/IGoryunov/macsweep/internal/policy"
	"github.com/IGoryunov/macsweep/internal/report"
	"github.com/IGoryunov/macsweep/internal/testutil/fixture"
)

// fakeTrash renames into a temp directory; tests may rename.
type fakeTrash struct {
	dir   string
	calls []string
	fail  map[string]bool
}

func (f *fakeTrash) Trash(p string) (string, error) {
	f.calls = append(f.calls, p)
	if f.fail[p] {
		return "", os.ErrPermission
	}
	dest := filepath.Join(f.dir, filepath.Base(p))
	return dest, os.Rename(p, dest)
}

func cand(home, rel, rule, verdict, blocked, nested string) report.Candidate {
	p := filepath.Join(home, rel)
	c := report.Candidate{RuleID: rule, Path: p, Verdict: verdict, Bytes: 4096, Blocked: blocked}
	if nested != "" {
		c.NestedIn = filepath.Join(home, nested)
	}
	if fi, err := os.Lstat(p); err == nil {
		st := fi.Sys().(*syscall.Stat_t)
		c.Dev, c.Ino, c.RootModTime = uint64(st.Dev), st.Ino, fi.ModTime()
	}
	return c
}

func TestPlanAndExecute(t *testing.T) {
	home := fixture.Build(t, fixture.Tree{
		"Library/Caches/A/f":        fixture.File{Size: 10},
		"Library/Caches/A/inner/f":  fixture.File{Size: 10},
		"Library/Caches/B/f":        fixture.File{Size: 10},
		"Library/Caches/Changed/f":  fixture.File{Size: 10},
		"Library/Caches/Blocked/f":  fixture.File{Size: 10},
		"Library/Caches/Failing/f":  fixture.File{Size: 10},
		".m2/repository/f":          fixture.File{Size: 10},
		"Downloads/f":               fixture.File{Size: 10},
		"Library/Caches/Replaced/f": fixture.File{Size: 10},
	})
	rep := &report.Report{Groups: []report.Group{{Name: "G", Candidates: []report.Candidate{
		cand(home, "Library/Caches/A", "a", "safe", "", ""),
		cand(home, "Library/Caches/A/inner", "inner", "safe", "", "Library/Caches/A"),
		cand(home, "Library/Caches/B", "b", "safe", "", ""),
		cand(home, "Library/Caches/Changed", "ch", "safe", "", ""),
		cand(home, "Library/Caches/Blocked", "bl", "safe", "app Foo is running", ""),
		cand(home, "Library/Caches/Failing", "fl", "safe", "", ""),
		cand(home, "Library/Caches/Replaced", "rp", "safe", "", ""),
		cand(home, ".m2/repository", "m2", "review", "", ""),
		cand(home, "Downloads", "dl", "keep", "", ""),
	}}}}

	plan, err := BuildPlan(rep, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 5 || len(plan.Contained) != 1 || len(plan.Blocked) != 1 || plan.Apps[0] != "Foo" {
		t.Fatalf("plan: items=%d contained=%d blocked=%d apps=%v", len(plan.Items), len(plan.Contained), len(plan.Blocked), plan.Apps)
	}
	if plan.TotalBytes != 5*4096 {
		t.Errorf("total %d", plan.TotalBytes)
	}
	if _, err := BuildPlan(rep, []string{filepath.Join(home, "Downloads")}); err == nil {
		t.Error("keep must be rejected")
	}
	if _, err := BuildPlan(rep, []string{filepath.Join(home, "nope")}); err == nil {
		t.Error("unknown path must be rejected")
	}
	sel, err := BuildPlan(rep, []string{filepath.Join(home, ".m2/repository")})
	if err != nil || len(sel.Items) != 1 || sel.Items[0].Verdict != "review" {
		t.Fatalf("explicit selection of review item: %v %+v", err, sel)
	}

	// Mutate after the "scan": Changed gets a new file (mtime bump), Replaced is swapped for a new inode.
	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(home, "Library/Caches/Changed/new"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rp := filepath.Join(home, "Library/Caches/Replaced")
	if err := os.Rename(rp, rp+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rp, 0o755); err != nil {
		t.Fatal(err)
	}

	ft := &fakeTrash{dir: t.TempDir(), fail: map[string]bool{filepath.Join(home, "Library/Caches/Failing"): true}}
	j := Execute(context.Background(), plan, ft, policy.New(home), home)
	status := map[string]string{}
	for _, o := range j.Items {
		status[filepath.Base(o.Path)] = o.Status + ":" + o.Reason
	}
	want := map[string]string{"A": "trashed:", "B": "trashed:", "inner": "contained:inside " + filepath.Join(home, "Library/Caches/A"), "Blocked": "skipped:app Foo is running"}
	for k, v := range want {
		if status[k] != v {
			t.Errorf("%s = %q, want %q", k, status[k], v)
		}
	}
	for k, sub := range map[string]string{"Changed": "modified since the scan", "Replaced": "different inode", "Failing": "permission"} {
		if !strings.Contains(status[k], sub) {
			t.Errorf("%s = %q, want containing %q", k, status[k], sub)
		}
	}
	if j.Trashed != 2 || j.Skipped != 3 || j.Failed != 1 || j.TrashedBytes != 2*4096 {
		t.Errorf("counts: trashed=%d skipped=%d failed=%d bytes=%d", j.Trashed, j.Skipped, j.Failed, j.TrashedBytes)
	}
	if _, err := os.Lstat(filepath.Join(home, "Library/Caches/A")); !os.IsNotExist(err) {
		t.Error("A should be gone")
	}
	if _, err := os.Lstat(filepath.Join(home, "Library/Caches/Changed")); err != nil {
		t.Error("Changed must still exist")
	}
	p, err := SaveJournal(RunsDir(home), j)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(p); err != nil || st.Size() == 0 {
		t.Fatalf("journal not written: %v", err)
	}
	if p2, err := SaveJournal(RunsDir(home), j); err != nil || p2 == p {
		t.Fatalf("second journal in the same second must get a new name: %v %s", err, p2)
	}

	// Cancellation before the loop skips everything.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	j2 := Execute(ctx, sel, ft, policy.New(home), home)
	if j2.Trashed != 0 || j2.Skipped != 1 || j2.Items[0].Reason != "cancelled" {
		t.Errorf("cancel: %+v", j2)
	}
}
