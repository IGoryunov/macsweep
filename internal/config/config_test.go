package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolve(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "Work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(home, ".config", "macsweep", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("project_roots: ['~/src']\nmin_size: 10MB\nworkers: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Missing file: defaults + autodetect.
	c, err := Load(filepath.Join(home, "nope.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Resolve(c, Overrides{}, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ProjectRoots) != 2 || r.MinSize != DefaultMinSize || r.Workers != DefaultWorkers() {
		t.Fatalf("defaults: %+v", r)
	}

	// Config file wins over defaults.
	c, err = Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	r, err = Resolve(c, Overrides{}, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ProjectRoots) != 1 || r.ProjectRoots[0] != filepath.Join(home, "src") || r.MinSize != 10<<20 || r.Workers != 3 {
		t.Fatalf("config: %+v", r)
	}

	// Flags win over config.
	r, err = Resolve(c, Overrides{ProjectRoots: []string{"~/Work"}, MinSize: "1GB", Workers: 7}, home)
	if err != nil {
		t.Fatal(err)
	}
	if r.ProjectRoots[0] != filepath.Join(home, "Work") || r.MinSize != 1<<30 || r.Workers != 7 {
		t.Fatalf("flags: %+v", r)
	}
	if _, err := Resolve(c, Overrides{MinSize: "lots"}, home); err == nil {
		t.Fatal("bad min_size should fail")
	}
}
