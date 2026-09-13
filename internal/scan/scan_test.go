package scan

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/IGoryunov/macsweep/internal/rules"
	"github.com/IGoryunov/macsweep/internal/testutil/fixture"
)

// duLike independently sums allocated bytes with hardlink dedup, following nothing.
func duLike(t *testing.T, root string) (bytes int64, files int64) {
	seen := map[InodeKey]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		st := fi.Sys().(*syscall.Stat_t)
		key := InodeKey{uint64(st.Dev), st.Ino}
		if !fi.IsDir() {
			files++
		}
		if st.Nlink > 1 && !fi.IsDir() {
			if seen[key] {
				return nil
			}
			seen[key] = true
		}
		bytes += st.Blocks * 512
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return bytes, files
}

func TestMeasure(t *testing.T) {
	tree := fixture.Tree{
		"a/one.bin":      fixture.File{Size: 10000, Age: 200 * fixture.Day},
		"a/sub/two.bin":  fixture.File{Size: 5000, Age: 3 * fixture.Day},
		"a/sub/link.bin": fixture.Hardlink{To: "a/one.bin"},
		"a/esc":          fixture.Symlink{To: "/"},
		"a/empty":        fixture.Dir{},
		"b/other.bin":    fixture.File{Size: 4096},
	}
	for _, w := range fixture.Weird {
		tree[filepath.Join("a", w, "f.txt")] = fixture.File{Size: 100}
	}
	home := fixture.Build(t, tree)
	root := filepath.Join(home, "a")

	s := New(Options{Workers: 4})
	res, err := s.Measure(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, wantFiles := duLike(t, root)
	if res.Bytes != wantBytes {
		t.Errorf("Bytes = %d, want %d", res.Bytes, wantBytes)
	}
	// one.bin, two.bin, link.bin, esc (symlink), 4 weird files
	if res.Files != wantFiles || res.Files != 8 {
		t.Errorf("Files = %d, want %d (8)", res.Files, wantFiles)
	}
	if res.Dirs != 1+1+1+4 { // a, sub, empty, 4 weird
		t.Errorf("Dirs = %d", res.Dirs)
	}
	if len(res.Links) != 1 {
		t.Errorf("Links = %v, want exactly one multi-link inode", res.Links)
	}
	// Symlink to / must not have been followed: otherwise Files would be huge.
	if res.Files > 100 {
		t.Fatal("symlink was followed")
	}
	// Newest mtime is "now" (the weird files and other.bin have no Age), so it must be recent.
	if time.Since(res.MaxModTime) > time.Minute {
		t.Errorf("MaxModTime too old: %v", res.MaxModTime)
	}
	if _, err := s.Measure(context.Background(), filepath.Join(root, "esc")); err != ErrSymlinkRoot {
		t.Errorf("symlink root: got %v", err)
	}
	if _, err := s.Measure(context.Background(), filepath.Join(root, "missing")); err == nil {
		t.Error("missing root should fail")
	}
}

func TestMaxModTimeOldTree(t *testing.T) {
	home := fixture.Build(t, fixture.Tree{
		"old/x/f1": fixture.File{Size: 10, Age: 200 * fixture.Day},
		"old/x/f2": fixture.File{Size: 10, Age: 300 * fixture.Day},
		"old/x":    fixture.Dir{Age: 200 * fixture.Day},
		"old":      fixture.Dir{Age: 200 * fixture.Day},
	})
	res, err := New(Options{}).Measure(context.Background(), filepath.Join(home, "old"))
	if err != nil {
		t.Fatal(err)
	}
	age := time.Since(res.MaxModTime)
	if age < 199*fixture.Day || age > 201*fixture.Day {
		t.Errorf("MaxModTime age = %v, want ~200d", age)
	}
}

func TestCancel(t *testing.T) {
	home := fixture.Build(t, fixture.Many("big", 6000, 50))
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(2 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := New(Options{}).Measure(ctx, filepath.Join(home, "big"))
	el := time.Since(start)
	if err != nil && err != context.Canceled {
		t.Fatalf("unexpected error %v", err)
	}
	if el > 300*time.Millisecond {
		t.Fatalf("cancellation took %v", el)
	}
}

func TestExpand(t *testing.T) {
	home := fixture.Build(t, fixture.Tree{
		"Work/app/package.json":                                 fixture.File{Size: 10},
		"Work/app/node_modules/x/node_modules/y/f":              fixture.File{Size: 10},
		"Work/app/.git/HEAD":                                    fixture.File{Size: 10},
		"Work/app/.git/node_modules/f":                          fixture.File{Size: 10},
		"Work/deep/a/b/lib/target/out":                          fixture.File{Size: 10},
		"Work/link":                                             fixture.Symlink{To: "/"},
		"Library/Application Support/JetBrains/PyCharm2024.1/x": fixture.File{Size: 10},
		"Library/Application Support/JetBrains/Toolbox/x":       fixture.File{Size: 10},
		"Library/Application Support/JetBrains/GoLand2023.3/x":  fixture.File{Size: 10},
		"Library/Application Support/JetBrains/file.json":       fixture.File{Size: 10},
	})
	s := New(Options{})
	root := filepath.Join(home, "Work")
	nm, _ := rules.CompilePattern(root + "/**/node_modules")
	tg, _ := rules.CompilePattern(root + "/**/target")
	stop := func(abs string) bool { return filepath.Base(abs) == ".git" }
	got, err := s.Expand(context.Background(), root, []Query{{Pattern: nm, Prune: true}, {Pattern: tg, Prune: true}}, stop)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(root, "app/node_modules")}; strings.Join(got[0], "|") != strings.Join(want, "|") {
		t.Errorf("node_modules = %v, want %v (pruned, .git stopped, symlink not followed)", got[0], want)
	}
	if want := filepath.Join(root, "deep/a/b/lib/target"); len(got[1]) != 1 || got[1][0] != want {
		t.Errorf("target = %v", got[1])
	}

	jb := filepath.Join(home, "Library/Application Support/JetBrains")
	star, _ := rules.CompilePattern(jb + "/*")
	got, _ = s.Expand(context.Background(), jb, []Query{{Pattern: star, Exclude: map[string]bool{"Toolbox": true}}}, nil)
	if want := []string{filepath.Join(jb, "GoLand2023.3"), filepath.Join(jb, "PyCharm2024.1")}; strings.Join(got[0], "|") != strings.Join(want, "|") {
		t.Errorf("jetbrains = %v", got[0])
	}

	lit, _ := rules.CompilePattern(jb)
	got, _ = s.Expand(context.Background(), jb, []Query{{Pattern: lit}}, nil)
	if len(got[0]) != 1 || got[0][0] != jb {
		t.Errorf("literal = %v", got[0])
	}
	got, _ = s.Expand(context.Background(), filepath.Join(home, "nope"), []Query{{Pattern: lit}}, nil)
	if len(got[0]) != 0 {
		t.Errorf("missing root should yield nothing, got %v", got[0])
	}
}

func TestProgress(t *testing.T) {
	home := fixture.Build(t, fixture.Many("p", 500, 25))
	ch := make(chan Progress, 64)
	s := New(Options{Progress: ch})
	s.WithProgress(func() { _, _ = s.Measure(context.Background(), filepath.Join(home, "p")) })
	close(ch)
	var last Progress
	for p := range ch {
		last = p
	}
	if last.Paths < 500 || last.Bytes == 0 {
		t.Fatalf("final progress %+v", last)
	}
}
