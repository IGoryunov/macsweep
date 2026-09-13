package rules

import (
	"strings"
	"testing"
	"testing/fstest"
)

func loadOne(t *testing.T, yaml string) (*Set, error) {
	t.Helper()
	return Load(fstest.MapFS{"t.yaml": {Data: []byte(yaml)}})
}

const validYAML = `
version: 1
group: G
rules:
  - id: a
    paths: ["$CA/X"]
    verdict: safe
    note: n
    recovery: r
  - id: b
    glob: "$AS/JetBrains/*"
    exclude: ["Toolbox"]
    prune: true
    verdict: review
    min_size: 10MB
    when:
      - if: not app_installed
        args: { strip_version: '[0-9]+$', map: { PyCharm: PyCharm } }
        then: safe
        reason: orphan
      - if: mtime_older_than
        args: { days: 60 }
        and:
          - if: larger_than
            args: { bytes: 1GB }
        then: keep
        reason: big and old
`

func TestLoadValid(t *testing.T) {
	set, err := loadOne(t, validYAML)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Rules) != 2 || set.Groups[0] != "G" {
		t.Fatalf("unexpected set %+v", set)
	}
	b, ok := set.ByID("b")
	if !ok || b.MinSize != 10<<20 || b.When[1].Then != Keep || len(b.When[1].And) != 1 || !b.Prune {
		t.Fatalf("rule b parsed wrong: %+v", b)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []struct{ name, yaml, want string }{
		{"version", "version: 2\ngroup: G\nrules: [{id: a, paths: ['~/x'], verdict: safe}]", "version must be 1"},
		{"no group", "version: 1\nrules: [{id: a, paths: ['~/x'], verdict: safe}]", "group is required"},
		{"empty id", "version: 1\ngroup: G\nrules: [{paths: ['~/x'], verdict: safe}]", "empty id"},
		{"dup id", "version: 1\ngroup: G\nrules: [{id: a, paths: ['~/x'], verdict: safe}, {id: a, paths: ['~/y'], verdict: safe}]", "duplicate rule id"},
		{"paths and glob", "version: 1\ngroup: G\nrules: [{id: a, paths: ['~/x'], glob: '~/y/*', verdict: safe}]", "mutually exclusive"},
		{"neither", "version: 1\ngroup: G\nrules: [{id: a, verdict: safe}]", "either paths or glob"},
		{"abs path", "version: 1\ngroup: G\nrules: [{id: a, paths: ['/Users/x/y'], verdict: safe}]", "must start with a placeholder"},
		{"two doublestar", "version: 1\ngroup: G\nrules: [{id: a, glob: '~/**/a/**', verdict: safe}]", "at most once"},
		{"prune no glob", "version: 1\ngroup: G\nrules: [{id: a, paths: ['~/x'], prune: true, verdict: safe}]", "prune requires glob"},
		{"exclude no glob", "version: 1\ngroup: G\nrules: [{id: a, paths: ['~/x'], exclude: [b], verdict: safe}]", "exclude requires glob"},
		{"bad verdict", "version: 1\ngroup: G\nrules: [{id: a, paths: ['~/x'], verdict: maybe}]", "unknown verdict"},
		{"no verdict", "version: 1\ngroup: G\nrules: [{id: a, paths: ['~/x']}]", "verdict is required"},
		{"bad size", "version: 1\ngroup: G\nrules: [{id: a, paths: ['~/x'], verdict: safe, min_size: big}]", "min_size"},
		{"unknown pred", "version: 1\ngroup: G\nrules: [{id: a, paths: ['~/x'], verdict: safe, when: [{if: is_cool, then: safe, reason: r}]}]", "unknown predicate"},
		{"missing arg", "version: 1\ngroup: G\nrules: [{id: a, paths: ['~/x'], verdict: safe, when: [{if: mtime_older_than, then: safe, reason: r}]}]", "missing required argument \"days\""},
		{"bad arg type", "version: 1\ngroup: G\nrules: [{id: a, paths: ['~/x'], verdict: safe, when: [{if: mtime_older_than, args: {days: many}, then: safe, reason: r}]}]", "positive integer"},
		{"unknown arg", "version: 1\ngroup: G\nrules: [{id: a, paths: ['~/x'], verdict: safe, when: [{if: mtime_older_than, args: {days: 1, weeks: 2}, then: safe, reason: r}]}]", "unknown argument \"weeks\""},
		{"bad regexp", "version: 1\ngroup: G\nrules: [{id: a, paths: ['~/x'], verdict: safe, when: [{if: app_installed, args: {strip_version: '('}, then: safe, reason: r}]}]", "strip_version"},
		{"no then", "version: 1\ngroup: G\nrules: [{id: a, paths: ['~/x'], verdict: safe, when: [{if: bundle_id_registered, reason: r}]}]", "then is required"},
		{"no reason", "version: 1\ngroup: G\nrules: [{id: a, paths: ['~/x'], verdict: safe, when: [{if: bundle_id_registered, then: safe}]}]", "reason is required"},
		{"unknown field", "version: 1\ngroup: G\nrules: [{id: a, paths: ['~/x'], verdict: safe, colour: red}]", "field colour not found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadOne(t, c.yaml)
			if err == nil {
				t.Fatalf("expected error containing %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not contain %q", err, c.want)
			}
		})
	}
}

func TestAllErrorsCollected(t *testing.T) {
	_, err := loadOne(t, "version: 3\ngroup: G\nrules: [{id: a, verdict: safe}, {id: b, paths: ['/abs'], verdict: nope}]")
	ve, ok := err.(ValidationErrors)
	if !ok || len(ve) < 3 {
		t.Fatalf("want >=3 collected errors, got %v", err)
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{"4096": 4096, "50MB": 50 << 20, "50 mb": 50 << 20, "1.5GB": 1536 << 20, "100k": 100 << 10, "2GiB": 2 << 30, "7B": 7}
	for in, want := range cases {
		got, err := ParseSize(in)
		if err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "big", "-1", "1PB"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) should fail", bad)
		}
	}
}

func TestExpandPaths(t *testing.T) {
	env := Env{Home: "/h", ProjectRoots: []string{"/h/Work", "/h/src"}}
	r := Rule{ID: "x", Paths: []string{"$AS/JetBrains", "~/.cache", "$PROJECTS"}}
	paths, warns := ExpandPaths(r, env)
	want := []string{"/h/Library/Application Support/JetBrains", "/h/.cache", "/h/Work", "/h/src"}
	if strings.Join(paths, "|") != strings.Join(want, "|") || len(warns) != 0 {
		t.Fatalf("got %v %v", paths, warns)
	}
	_, warns = ExpandGlobs(Rule{ID: "y", Glob: "$PROJECTS/**/node_modules"}, Env{Home: "/h"})
	if len(warns) != 1 || !strings.Contains(warns[0], "no roots configured") {
		t.Fatalf("want no-roots warning, got %v", warns)
	}
}

func TestPattern(t *testing.T) {
	p, err := CompilePattern("/h/Work/**/node_modules")
	if err != nil || p.Root != "/h/Work" || len(p.Segs) != 2 {
		t.Fatalf("compile: %+v %v", p, err)
	}
	cases := []struct {
		rel        string
		match, can bool
	}{
		{"node_modules", true, true},
		{"app/node_modules", true, true},
		{"app/src", false, true},
		{"app/node_modules/x", false, true},
	}
	for _, c := range cases {
		rel := strings.Split(c.rel, "/")
		if got := p.Match(rel); got != c.match {
			t.Errorf("Match(%q) = %v", c.rel, got)
		}
		if got := p.CanDescend(rel); got != c.can {
			t.Errorf("CanDescend(%q) = %v", c.rel, got)
		}
	}
	q, _ := CompilePattern("/h/Library/Application Support/JetBrains/*")
	if q.Root != "/h/Library/Application Support/JetBrains" || !q.Match([]string{"PyCharm2024.1"}) || q.Match([]string{"a", "b"}) || q.CanDescend([]string{"a"}) {
		t.Fatalf("single-star pattern misbehaves: %+v", q)
	}
	lit, _ := CompilePattern("/h/.cache")
	if !lit.Literal() || lit.Root != "/h/.cache" {
		t.Fatalf("literal: %+v", lit)
	}
	if _, err := CompilePattern("/h/**/a/**"); err == nil {
		t.Fatal("double ** should fail")
	}
}
