package scan

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/IGoryunov/macsweep/internal/testutil/fixture"
)

func TestTree(t *testing.T) {
	home := fixture.Build(t, fixture.Tree{
		"a/x/f1":     fixture.File{Size: 4096},
		"a/x/f2":     fixture.File{Size: 4096},
		"a/y/f3":     fixture.File{Size: 4096},
		"a/y/link":   fixture.Hardlink{To: "a/x/f1"},
		"a/esc":      fixture.Symlink{To: "/"},
		"b/f4":       fixture.File{Size: 4096},
		"a/z/deep/f": fixture.File{Size: 4096},
	})
	root, err := New(Options{}).Tree(context.Background(), home, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, wantFiles := duLike(t, home)
	if root.Bytes != want || root.Files != wantFiles {
		t.Fatalf("root = %d bytes %d files, want %d / %d", root.Bytes, root.Files, want, wantFiles)
	}
	a := root.Find(filepath.Join(home, "a"))
	if a == nil || len(a.Children) != 3 {
		t.Fatalf("a: %+v", a)
	}
	x := root.Find(filepath.Join(home, "a/x"))
	y := root.Find(filepath.Join(home, "a/y"))
	// The hardlinked inode is attributed to whichever directory saw it first,
	// but it is counted exactly once: x has f1,f2 (8192) and y has f3 (4096)
	// plus the link, which adds 4096 to exactly one of them.
	if x == nil || y == nil || x.Files != 2 || y.Files != 2 || x.Bytes+y.Bytes != 3*4096 {
		t.Fatalf("a/x: %+v a/y: %+v", x, y)
	}
	// Children sorted by size descending.
	if a.Children[0].Bytes < a.Children[1].Bytes || a.Children[1].Bytes < a.Children[2].Bytes {
		t.Errorf("children not sorted: %+v", a.Children)
	}
	if root.Find(filepath.Join(home, "a/esc/usr")) != nil {
		t.Error("symlink was followed")
	}
	skipped, _ := New(Options{}).Tree(context.Background(), home, func(p string) bool { return filepath.Base(p) == "a" })
	if skipped.Bytes != 4096 || !skipped.Find(filepath.Join(home, "a")).Skipped {
		t.Errorf("skip did not prevent descent or flag the node: %+v", skipped)
	}
	if !DefaultSkip(home)(filepath.Join(home, "Library/CloudStorage")) || DefaultSkip(home)(filepath.Join(home, "Library")) {
		t.Error("DefaultSkip must skip exactly ~/Library/CloudStorage")
	}
}
