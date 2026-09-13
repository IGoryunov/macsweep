package engine

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/IGoryunov/macsweep/internal/policy"
	"github.com/IGoryunov/macsweep/internal/procs"
	"github.com/IGoryunov/macsweep/internal/report"
	"github.com/IGoryunov/macsweep/internal/rules"
	"github.com/IGoryunov/macsweep/internal/scan"
	"github.com/IGoryunov/macsweep/internal/testutil/fixture"
	rulesfs "github.com/IGoryunov/macsweep/rules"
)

var update = flag.Bool("update", false, "rewrite golden files")

type fakeApps struct {
	installed map[string]bool
	err       error
}

func (f fakeApps) InstalledByName(n string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.installed[strings.ToLower(n)], nil
}
func (f fakeApps) BundleRegistered(id string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.installed[strings.ToLower(id)], nil
}

const day = fixture.Day

// standardHome is the shared fixture for most scenarios.
func standardHome(t *testing.T) string {
	tree := fixture.Tree{
		"Library/Application Support/JetBrains/PyCharm2024.1/options/a.xml": fixture.File{Size: 4096},
		"Library/Application Support/JetBrains/GoLand2023.3/options/a.xml":  fixture.File{Size: 4096},
		"Library/Application Support/JetBrains/Unknown2024.1/x":             fixture.File{Size: 4096},
		"Library/Application Support/JetBrains/Toolbox/x":                   fixture.File{Size: 4096},
		"Library/Caches/JetBrains/PyCharm2024.1/index.bin":                  fixture.File{Size: 8192},
		"Work/old/package.json":                                             fixture.File{Size: 10},
		"Work/old/node_modules/a/index.js":                                  fixture.File{Size: 4096, Age: 200 * day},
		"Work/old/node_modules/b/big.js":                                    fixture.Hardlink{To: "Library/pnpm/store/v3/files/ab/cd"},
		"Work/fresh/package.json":                                           fixture.File{Size: 10},
		"Work/fresh/node_modules/a/index.js":                                fixture.File{Size: 4096, Age: 3 * day},
		"Work/lib/.git/node_modules/f":                                      fixture.File{Size: 4096},
		"Work/lib/target/out.bin":                                           fixture.File{Size: 4096, Age: 100 * day},
		"Library/pnpm/store/v3/files/ab/cd":                                 fixture.File{Size: 8192, Age: 200 * day},
		"Documents/proj/node_modules/x":                                     fixture.File{Size: 4096, Age: 300 * day},
		".cache/huggingface/model.bin":                                      fixture.File{Size: 8192},
		".cache/other.bin":                                                  fixture.File{Size: 4096},
		".ssh/id_x":                                                         fixture.File{Size: 4096},
		"Library/Caches/escape":                                             fixture.Symlink{To: "/"},
	}
	for _, w := range fixture.Weird {
		tree[filepath.Join("Library/Caches/JetBrains", w, "f")] = fixture.File{Size: 4096}
	}
	return fixture.Build(t, tree)
}

func env(home string, a AppIndex, p ProcessList) Env {
	return Env{Home: home, ProjectRoots: []string{filepath.Join(home, "Work"), filepath.Join(home, "Documents")},
		Now: time.Now, Apps: a, Procs: p, Policy: policy.New(home), MinSize: 0}
}

func loadRules(t *testing.T, yaml string) *rules.Set {
	t.Helper()
	set, err := rules.Load(fstest.MapFS{"t.yaml": {Data: []byte(yaml)}})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

const testRules = `
version: 1
group: Test
rules:
  - id: jb-caches
    paths: ["$CA/JetBrains"]
    verdict: safe
    note: caches
    recovery: rebuilt
    owner_app: pycharm
  - id: jb-orphan
    glob: "$AS/JetBrains/*"
    exclude: ["Toolbox"]
    verdict: review
    note: config
    recovery: reinstall
    when:
      - if: not app_installed
        args:
          strip_version: '[0-9]{4}\.[0-9]+$'
          map: { PyCharm: PyCharm, GoLand: GoLand }
        then: safe
        reason: IDE not installed
  - id: node-modules
    glob: "$PROJECTS/**/node_modules"
    prune: true
    verdict: review
    note: deps
    recovery: npm install
    when:
      - if: mtime_older_than
        args: { days: 60 }
        then: safe
        reason: stale
  - id: pnpm-store
    paths: ["~/Library/pnpm/store"]
    verdict: safe
    note: store
    recovery: pnpm install
  - id: xdg-cache
    paths: ["~/.cache"]
    verdict: safe
    note: cache
    recovery: recreated
  - id: hf-cache
    paths: ["~/.cache/huggingface"]
    verdict: safe
    note: hf
    recovery: redownload
  - id: ssh
    paths: ["~/.ssh"]
    verdict: safe
    note: never
    recovery: never
  - id: target
    glob: "$PROJECTS/**/target"
    prune: true
    verdict: review
    note: build
    recovery: rebuild
`

func find(rep *report.Report, ruleID, suffix string) *report.Candidate {
	for gi := range rep.Groups {
		for ci := range rep.Groups[gi].Candidates {
			c := &rep.Groups[gi].Candidates[ci]
			if c.RuleID == ruleID && strings.HasSuffix(c.Path, suffix) {
				return c
			}
		}
	}
	return nil
}

func TestScenarios(t *testing.T) {
	home := standardHome(t)
	set := loadRules(t, testRules)
	sc := scan.New(scan.Options{})
	apps := fakeApps{installed: map[string]bool{"goland": true}}
	running := procs.FromList([]string{"/Applications/PyCharm.app/Contents/MacOS/pycharm", "Finder"})

	rep, measured, err := Run(context.Background(), env(home, apps, running), set, Options{Scanner: sc})
	if err != nil {
		t.Fatal(err)
	}
	if len(measured) == 0 {
		t.Fatal("no measurements returned")
	}

	check := func(rule, suffix, verdict, reason string) *report.Candidate {
		t.Helper()
		c := find(rep, rule, suffix)
		if c == nil {
			t.Errorf("%s %s: candidate missing", rule, suffix)
			return nil
		}
		if c.Verdict != verdict {
			t.Errorf("%s %s: verdict %s, want %s (reasons %v)", rule, suffix, c.Verdict, verdict, c.Reasons)
		}
		if reason != "" && !strings.Contains(strings.Join(c.Reasons, "|"), reason) {
			t.Errorf("%s %s: reasons %v lack %q", rule, suffix, c.Reasons, reason)
		}
		return c
	}
	check("jb-orphan", "PyCharm2024.1", "safe", "IDE not installed")
	check("jb-orphan", "GoLand2023.3", "review", "rule default")
	check("jb-orphan", "Unknown2024.1", "review", "not in map")
	if find(rep, "jb-orphan", "Toolbox") != nil {
		t.Error("excluded Toolbox must not be a candidate")
	}
	check("node-modules", "Work/old/node_modules", "safe", "stale")
	check("node-modules", "Work/fresh/node_modules", "review", "")
	if find(rep, "node-modules", ".git/node_modules") != nil {
		t.Error("walk must stop at .git")
	}
	if find(rep, "node-modules", "Documents/proj/node_modules") != nil {
		t.Error("denylisted ~/Documents must not appear even though it is a project root")
	}
	if find(rep, "ssh", ".ssh") != nil {
		t.Error("~/.ssh must be denied even when a rule targets it")
	}
	check("target", "Work/lib/target", "review", "rule default") // no when block: base verdict stays
	if c := check("jb-caches", "Caches/JetBrains", "safe", ""); c != nil {
		if c.Blocked == "" || !strings.Contains(c.Blocked, "pycharm") {
			t.Errorf("running owner app must block: %+v", c.Blocked)
		}
		// 1 index file + 4 weird files
		if c.Files != 5 {
			t.Errorf("JetBrains caches files = %d, want 5", c.Files)
		}
	}
	// Nesting: ~/.cache/huggingface inside ~/.cache.
	hf := check("hf-cache", ".cache/huggingface", "safe", "")
	if hf != nil && !strings.HasSuffix(hf.NestedIn, "/.cache") {
		t.Errorf("huggingface should be nested in ~/.cache, got %q", hf.NestedIn)
	}
	// Totals dedup: hardlinked 8 KiB file in pnpm store and node_modules counted once;
	// nested huggingface not double counted.
	var sum int64
	for _, g := range rep.Groups {
		for _, c := range g.Candidates {
			if c.NestedIn == "" {
				sum += c.Bytes
			}
		}
	}
	total := rep.Totals.SafeBytes + rep.Totals.ReviewBytes + rep.Totals.KeepBytes
	if total != sum-8192 {
		t.Errorf("totals %d should equal outer sum %d minus one 8 KiB hardlink", total, sum)
	}
	if len(rep.Warnings) != 0 {
		t.Errorf("unexpected warnings %v", rep.Warnings)
	}
}

func TestAppIndexFailureNeverSafe(t *testing.T) {
	home := standardHome(t)
	set := loadRules(t, testRules)
	rep, _, err := Run(context.Background(), env(home, fakeApps{err: errors.New("lsregister exploded")}, procs.FromList(nil)), set, Options{Scanner: scan.New(scan.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"PyCharm2024.1", "GoLand2023.3"} {
		c := find(rep, "jb-orphan", suffix)
		if c == nil || c.Verdict != "review" || !strings.Contains(strings.Join(c.Reasons, "|"), "unavailable") {
			t.Errorf("%s: %+v", suffix, c)
		}
	}
}

func TestVerdictCombination(t *testing.T) {
	yes := fakeApps{installed: map[string]bool{"x": true}}
	tgt := Target{Path: "/h/X", Base: "X", Result: scan.Result{Bytes: 10 << 20, MaxModTime: time.Now().Add(-100 * day)}}
	e := Env{Home: "/h", Now: time.Now, Apps: yes}
	mk := func(base rules.Verdict, when ...rules.When) rules.Rule {
		return rules.Rule{ID: "r", Verdict: base, When: when}
	}
	old := rules.When{If: "mtime_older_than", Args: map[string]any{"days": 60}, Then: rules.Safe, Reason: "old"}
	big := rules.When{If: "larger_than", Args: map[string]any{"bytes": "1MB"}, Then: rules.Keep, Reason: "big"}
	fresh := rules.When{If: "mtime_older_than", Args: map[string]any{"days": 365}, Then: rules.Safe, Reason: "very old"}
	broken := rules.When{If: "app_installed", Args: map[string]any{"map": map[string]any{"Y": "Y"}}, Then: rules.Safe, Reason: "n/a"}
	both := rules.When{If: "mtime_older_than", Args: map[string]any{"days": 60}, And: []rules.Cond{{If: "not larger_than", Args: map[string]any{"bytes": "1MB"}}}, Then: rules.Safe, Reason: "old and small"}

	cases := []struct {
		name string
		r    rules.Rule
		want rules.Verdict
	}{
		{"none fired keeps base", mk(rules.Review, fresh), rules.Review},
		{"one fired lowers", mk(rules.Review, old), rules.Safe},
		{"safe and keep → keep", mk(rules.Review, old, big), rules.Keep},
		{"error on safe base → review", mk(rules.Safe, broken), rules.Review},
		{"error plus fired safe → review", mk(rules.Review, old, broken), rules.Review},
		{"and: second cond false", mk(rules.Review, both), rules.Review},
	}
	for _, c := range cases {
		got := Evaluate(context.Background(), e, c.r, tgt)
		if got.Verdict != c.want {
			t.Errorf("%s: got %s, want %s (%v)", c.name, got.Verdict, c.want, got.Reasons)
		}
	}
}

func TestEmptyGroupWarning(t *testing.T) {
	home := standardHome(t)
	set := loadRules(t, "version: 1\ngroup: Only\nrules:\n  - id: ssh\n    paths: ['~/.ssh']\n    verdict: safe\n    note: n\n    recovery: r\n")
	rep, _, err := Run(context.Background(), env(home, fakeApps{}, procs.FromList(nil)), set, Options{Scanner: scan.New(scan.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "denied by policy: 1") {
		t.Fatalf("warnings = %v", rep.Warnings)
	}
	e := env(home, fakeApps{}, procs.FromList(nil))
	e.MinSize = 1 << 40
	rep, _, err = Run(context.Background(), e, loadRules(t, testRules), Options{Scanner: scan.New(scan.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	// Below-threshold-only groups are normal and must not warn; the ~/.ssh and
	// ~/Documents denials in the test rules still produce one warning.
	if len(rep.Groups) != 0 || len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "denied by policy") {
		t.Fatalf("min-size: groups=%d warnings=%v", len(rep.Groups), rep.Warnings)
	}
}

func TestExplain(t *testing.T) {
	home := standardHome(t)
	set := loadRules(t, testRules)
	sc := scan.New(scan.Options{})
	e := env(home, fakeApps{installed: map[string]bool{}}, procs.FromList(nil))

	ex, err := Explain(context.Background(), e, set, sc, filepath.Join(home, "Library/Application Support/JetBrains/PyCharm2024.1"))
	if err != nil {
		t.Fatal(err)
	}
	if !ex.Allowed || len(ex.Matches) != 1 || ex.Matches[0].RuleID != "jb-orphan" || ex.Verdict != "safe" || len(ex.Matches[0].Predicates) != 1 {
		t.Fatalf("explain orphan: %+v", ex)
	}
	ex, _ = Explain(context.Background(), e, set, sc, filepath.Join(home, "Documents"))
	if ex.Allowed || !strings.Contains(ex.Policy, "~/Documents") {
		t.Fatalf("explain Documents: %+v", ex)
	}
	ex, _ = Explain(context.Background(), e, set, sc, filepath.Join(home, "Work/old/node_modules"))
	if len(ex.Matches) != 1 || ex.Matches[0].By != "glob" || ex.Verdict != "safe" {
		t.Fatalf("explain node_modules: %+v", ex)
	}
	ex, _ = Explain(context.Background(), e, set, sc, filepath.Join(home, "Work/old"))
	if len(ex.Matches) != 0 || ex.Allowed {
		t.Fatalf("explain source dir: %+v", ex)
	}
}

func TestGoldenEmbeddedRules(t *testing.T) {
	home := standardHome(t)
	set, err := rules.Load(rulesfs.FS())
	if err != nil {
		t.Fatal(err)
	}
	e := env(home, fakeApps{installed: map[string]bool{"goland": true}}, procs.FromList(nil))
	e.Now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	// Fixture ages are relative to the real clock; use the real clock for predicates
	// but a fixed GeneratedAt via normalisation below.
	e.Now = time.Now
	rep, _, err := Run(context.Background(), e, set, Options{Scanner: scan.New(scan.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	normalise(rep, home)
	got, _ := json.MarshalIndent(rep, "", "  ")
	golden := filepath.Join("..", "..", "testdata", "golden", "embedded-standard-home.json")
	if *update {
		if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	if strings.TrimSpace(string(want)) != string(got) {
		t.Errorf("golden %s differs from output; run `go test ./internal/engine -update` and inspect `git diff testdata/`", golden)
	}
}

func normalise(rep *report.Report, home string) {
	rep.GeneratedAt = time.Time{}
	rep.Home = "~"
	for i := range rep.ProjectRoots {
		rep.ProjectRoots[i] = report.DisplayPath(rep.ProjectRoots[i], home)
	}
	for gi := range rep.Groups {
		for ci := range rep.Groups[gi].Candidates {
			c := &rep.Groups[gi].Candidates[ci]
			c.Path = report.DisplayPath(c.Path, home)
			c.ModTime = time.Time{}
			c.RootModTime = time.Time{}
			c.Dev, c.Ino = 0, 0
			if c.NestedIn != "" {
				c.NestedIn = report.DisplayPath(c.NestedIn, home)
			}
		}
	}
}
