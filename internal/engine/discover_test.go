package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/IGoryunov/macsweep/internal/policy"
	"github.com/IGoryunov/macsweep/internal/report"
	"github.com/IGoryunov/macsweep/internal/scan"
	"github.com/IGoryunov/macsweep/internal/testutil/fixture"
)

func TestHotspots(t *testing.T) {
	mk := func(path string, bytes int64, kids ...*scan.Node) *scan.Node {
		return &scan.Node{Path: path, Bytes: bytes, Children: kids}
	}
	// ~ = 100: Library 70 (AS 40 (JB 40), Caches 20, rest 10), Movies 25, rest 5
	root := mk("/h", 100,
		mk("/h/Library", 70,
			mk("/h/Library/Application Support", 40, mk("/h/Library/Application Support/JetBrains", 40)),
			mk("/h/Library/Caches", 20, mk("/h/Library/Caches/a", 3), mk("/h/Library/Caches/b", 3)),
		),
		mk("/h/Movies", 25),
	)
	var out []Hotspot
	hotspots(root, 10, &out)
	got := map[string]int64{}
	for _, h := range out {
		key := h.Path
		if h.Remainder {
			key += " (rest)"
		}
		got[key] = h.Bytes
	}
	want := map[string]int64{
		"/h/Library/Application Support": 40, // single child holds ~100% → stop at the shorter name
		"/h/Library/Caches":              20, // children small → report itself
		"/h/Library (rest)":              10, // 70 - 40 - 20
		"/h/Movies":                      25,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
}

func TestDiscoverStatuses(t *testing.T) {
	tree := fixture.Tree{}
	for i := 0; i < 8; i++ {
		tree[filepath.Join("Movies", "m", itoa(i))] = fixture.File{Size: 4096}
		tree[filepath.Join("Library/Caches/Foo", itoa(i))] = fixture.File{Size: 4096}
		tree[filepath.Join("Stuff/big", itoa(i))] = fixture.File{Size: 4096}
	}
	home := fixture.Build(t, tree)
	e := Env{Home: home, Policy: policy.New(home)}
	rep := &report.Report{Groups: []report.Group{{Candidates: []report.Candidate{{RuleID: "foo", Path: filepath.Join(home, "Library/Caches/Foo")}}}}}
	d, err := Discover(context.Background(), e, scan.New(scan.Options{}), rep, 8*4096, 10)
	if err != nil {
		t.Fatal(err)
	}
	st := map[string]string{}
	for _, h := range d.Hotspots {
		st[h.DisplayPath] = h.Status + ":" + h.Detail
	}
	if st["~/Movies/m"] != "protected:denylisted location ~/Movies" && st["~/Movies"] != "protected:denylisted location ~/Movies" {
		t.Errorf("Movies: %v", st)
	}
	// ~/Library → Caches → Foo is a single chain, so the hotspot collapses to ~/Library,
	// which is inside no candidate; mark it covered only if the path is under a candidate.
	if st["~/Library"] != "protected:structural directory ~/Library" && st["~/Library/Caches/Foo"] != "covered:foo" {
		t.Errorf("Foo chain: %v", st)
	}
	if st["~/Stuff/big"] != "unknown:" && st["~/Stuff"] != "unknown:" {
		t.Errorf("Stuff: %v", st)
	}
}

func itoa(i int) string { return string(rune('a' + i)) }
