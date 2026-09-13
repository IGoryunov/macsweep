package manifest

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/IGoryunov/macsweep/internal/scan"
)

func TestRoundTripAndMismatch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.json")
	want := Manifest{Home: "/h", Roots: []string{"/h/a", "/h/b"}, RulesHash: "x"}
	m := want
	m.Results = []scan.Result{{Path: "/h/a/c", Bytes: 10, Links: map[scan.InodeKey]int64{{Dev: 1, Ino: 2}: 5}}}
	if err := Save(p, m); err != nil {
		t.Fatal(err)
	}
	got, ok := Load(p, Manifest{Home: "/h", Roots: []string{"/h/b", "/h/a"}, RulesHash: "x"}, time.Hour)
	if !ok || len(got.Results) != 1 || got.Results[0].Links[scan.InodeKey{Dev: 1, Ino: 2}] != 5 {
		t.Fatalf("round trip failed: %v %+v", ok, got)
	}
	if _, ok := Load(p, Manifest{Home: "/h", Roots: want.Roots, RulesHash: "y"}, time.Hour); ok {
		t.Error("rules hash mismatch should miss")
	}
	if _, ok := Load(p, Manifest{Home: "/other", Roots: want.Roots, RulesHash: "x"}, time.Hour); ok {
		t.Error("home mismatch should miss")
	}
	if _, ok := Load(p, want, 0); ok {
		t.Error("expired manifest should miss")
	}
	m.Version = 99
	m.CreatedAt = time.Now()
	if err := Save(p, m); err != nil {
		t.Fatal(err)
	}
	// Save forces Version; simulate a foreign version by hand.
	if _, ok := Load(filepath.Join(t.TempDir(), "missing.json"), want, time.Hour); ok {
		t.Error("missing file should miss")
	}
}
