package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mk(t *testing.T, home string, rel ...string) {
	t.Helper()
	for _, r := range rel {
		p := filepath.Join(home, r)
		if strings.HasSuffix(r, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCheck(t *testing.T) {
	home := t.TempDir()
	mk(t, home, "Work/app/package.json", "Work/app/node_modules/x/index.js", "Work/lib/.git/", "Work/lib/target/", "Library/Caches/Foo/",
		"Documents/proj/node_modules/", "Pictures/Lib.photoslibrary/inner/", "Library/Application Support/Bar/")
	p := New(home, "/opt/extra")
	cases := []struct {
		path   string
		allow  bool
		reason string
	}{
		{"/tmp/elsewhere", false, "outside allowed roots"},
		{"/opt/extra/x", true, "allowed"},
		{home, false, "structural"},
		{filepath.Join(home, "Library"), false, "structural"},
		{filepath.Join(home, "Library/Caches"), false, "structural"},
		{filepath.Join(home, "Library/Caches/Foo"), true, "allowed"},
		{filepath.Join(home, "Documents/proj/node_modules"), false, "~/Documents"},
		{filepath.Join(home, ".ssh"), false, "~/.ssh"},
		{filepath.Join(home, ".config/macsweep"), false, "~/.config"},
		{filepath.Join(home, "Library/Caches/macsweep"), false, "~/Library/Caches/macsweep"},
		{filepath.Join(home, "Work/lib/.git"), false, "denylisted name .git"},
		{filepath.Join(home, "Work/lib/.git/objects"), false, "inside denylisted .git"},
		{filepath.Join(home, "Pictures/Lib.photoslibrary/inner"), false, "~/Pictures"},
		{filepath.Join(home, "Work/app"), false, "source tree (contains package.json)"},
		{filepath.Join(home, "Work/lib"), false, "source tree (contains .git)"},
		{filepath.Join(home, "Work/app/node_modules"), true, "allowed"},
		{filepath.Join(home, "Work/lib/target"), true, "allowed"},
		{filepath.Join(home, "Library/Application Support/Bar"), true, "allowed"},
	}
	for _, c := range cases {
		d := p.Check(c.path)
		if d.Allowed != c.allow || !strings.Contains(d.Reason, c.reason) {
			t.Errorf("Check(%s) = %+v, want allow=%v reason~%q", c.path, d, c.allow, c.reason)
		}
	}
}

func TestInfoRoots(t *testing.T) {
	p := New(t.TempDir())
	if d := p.Check("/Applications/Foo.app"); !d.Allowed {
		t.Fatalf("app bundle should be allowed for information: %+v", d)
	}
	if !p.InfoOnly("/Applications/Foo.app") || p.InfoOnly(filepath.Join(p.Home, "Applications/Foo.app")) {
		t.Fatal("InfoOnly wrong")
	}
}

func TestDeniedName(t *testing.T) {
	for name, want := range map[string]bool{".git": true, "x.photoslibrary": true, "node_modules": false, "k.keychain-db": true} {
		if DeniedName(name) != want {
			t.Errorf("DeniedName(%q) = %v", name, !want)
		}
	}
}
