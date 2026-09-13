// Package fixture builds fake home directories for tests from a declarative
// tree description. Files are small real files, so st_blocks is deterministic
// on APFS (4 KiB allocation units).
package fixture

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Tree maps home-relative paths to entries. Parent directories are created
// automatically.
type Tree map[string]Entry

// Entry is one fixture node.
type Entry interface{ isEntry() }

// File is a regular file. Content wins over Size; Age sets mtime = now - Age.
type File struct {
	Size    int64
	Age     time.Duration
	Content []byte
}

// Dir is an explicit (possibly empty) directory with an optional Age.
type Dir struct{ Age time.Duration }

// Hardlink links to another home-relative file created in the same tree.
type Hardlink struct{ To string }

// Symlink creates a symbolic link with the given target, verbatim.
type Symlink struct{ To string }

func (File) isEntry()     {}
func (Dir) isEntry()      {}
func (Hardlink) isEntry() {}
func (Symlink) isEntry()  {}

// Day is a convenient unit for Age.
const Day = 24 * time.Hour

// Weird is the standard set of awkward directory names every scan test should include.
var Weird = []string{"with space", "кириллица", "emoji 🚀", "line\nbreak"}

// Build materialises tree under a fresh temp dir and returns its
// symlink-resolved absolute path.
func Build(t testing.TB, tree Tree) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(tree))
	for k := range tree {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	now := time.Now()

	// Pass 1: files and dirs.
	for _, k := range keys {
		p := filepath.Join(home, k)
		switch e := tree[k].(type) {
		case File:
			must(t, os.MkdirAll(filepath.Dir(p), 0o755))
			content := e.Content
			if content == nil {
				content = make([]byte, e.Size)
			}
			must(t, os.WriteFile(p, content, 0o644))
		case Dir:
			must(t, os.MkdirAll(p, 0o755))
		default:
			must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		}
	}
	// Pass 2: links.
	for _, k := range keys {
		p := filepath.Join(home, k)
		switch e := tree[k].(type) {
		case Hardlink:
			must(t, os.Link(filepath.Join(home, e.To), p))
		case Symlink:
			must(t, os.Symlink(e.To, p))
		}
	}
	// Pass 3: mtimes, files first, then dirs deepest-first so children don't disturb them.
	for _, k := range keys {
		if e, ok := tree[k].(File); ok && e.Age > 0 {
			ts := now.Add(-e.Age)
			must(t, os.Chtimes(filepath.Join(home, k), ts, ts))
		}
	}
	dirs := make([]string, 0)
	for _, k := range keys {
		if e, ok := tree[k].(Dir); ok && e.Age > 0 {
			dirs = append(dirs, k)
		}
	}
	sort.Slice(dirs, func(i, j int) bool {
		return strings.Count(dirs[i], "/") > strings.Count(dirs[j], "/")
	})
	explicit := map[string]bool{}
	for _, k := range dirs {
		ts := now.Add(-tree[k].(Dir).Age)
		must(t, os.Chtimes(filepath.Join(home, k), ts, ts))
		explicit[filepath.Join(home, k)] = true
	}
	// Pass 4: every other directory gets the newest mtime among its children,
	// bottom-up, so "age" of a tree is governed by its files as in real life.
	var all []string
	must(t, filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			all = append(all, p)
		}
		return err
	}))
	sort.Slice(all, func(i, j int) bool {
		return strings.Count(all[i], "/") > strings.Count(all[j], "/")
	})
	for _, d := range all {
		if explicit[d] {
			continue
		}
		ents, err := os.ReadDir(d)
		must(t, err)
		var newest time.Time
		for _, e := range ents {
			fi, err := os.Lstat(filepath.Join(d, e.Name()))
			must(t, err)
			if fi.ModTime().After(newest) {
				newest = fi.ModTime()
			}
		}
		if !newest.IsZero() {
			must(t, os.Chtimes(d, newest, newest))
		}
	}
	return home
}

// Many returns a tree with n small files spread over dirs of width files each, under prefix.
func Many(prefix string, n, width int) Tree {
	tr := Tree{}
	for i := 0; i < n; i++ {
		tr[filepath.Join(prefix, "d"+itoa(i/width), "f"+itoa(i)+".txt")] = File{Size: 100}
	}
	return tr
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func must(t testing.TB, err error) {
	if err != nil {
		t.Helper()
		t.Fatal(err)
	}
}
