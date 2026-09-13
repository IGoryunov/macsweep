// Package guard holds tests that enforce project-wide invariants.
package guard

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

var deletionRe = regexp.MustCompile(`\bos\.(Remove|RemoveAll|Rename)\(|\bsyscall\.(Unlink|Rmdir|Rename)\(|\bunix\.(Unlink|Rmdir|Rename)\(`)

// TestNoDeletionAPIs enforces SPEC §6/§7: nothing in the codebase deletes
// files. internal/undo is the one place allowed to call os.Rename: it moves
// items out of the Trash back to their journaled origin and never overwrites.
func TestNoDeletionAPIs(t *testing.T) {
	root := repoRoot(t)
	allowRename := map[string]bool{"internal/undo/undo.go": true}
	removeRe := regexp.MustCompile(`\bos\.(Remove|RemoveAll)\(|\bsyscall\.(Unlink|Rmdir)\(|\bunix\.(Unlink|Rmdir)\(`)
	for _, dir := range []string{"cmd", "internal"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if m := removeRe.Find(data); m != nil {
				t.Errorf("%s uses forbidden deletion API %s", rel, m)
			}
			if m := deletionRe.Find(data); m != nil && !allowRename[rel] {
				t.Errorf("%s uses forbidden deletion/rename API %s", rel, m)
			}
			return nil
		})
	}
}

var nativeDeletionRe = regexp.MustCompile(`removeItem|\bunlink\s*\(|\brmdir\s*\(|\bremove\s*\(|\brename\s*\(`)

// TestNoNativeDeletion enforces that Objective-C/C sources only ever trash.
func TestNoNativeDeletion(t *testing.T) {
	root := repoRoot(t)
	_ = filepath.WalkDir(filepath.Join(root, "internal"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		switch filepath.Ext(p) {
		case ".m", ".c", ".h", ".mm":
		default:
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if m := nativeDeletionRe.Find(data); m != nil {
			rel, _ := filepath.Rel(root, p)
			t.Errorf("%s uses forbidden native deletion primitive %s", rel, m)
		}
		return nil
	})
}

// TestNoNetwork enforces SPEC §12: the binary makes no network calls. Package
// net/http is permitted only in internal/web (the loopback UI server); no
// non-test file may use an HTTP client or dial out, and every listener must
// bind to 127.0.0.1.
func TestNoNetwork(t *testing.T) {
	root := repoRoot(t)
	outbound := regexp.MustCompile(`http\.(Get|Post|Head|PostForm|NewRequest|NewRequestWithContext|DefaultClient|Client\{)|net\.Dial|net\.DialTimeout|tls\.Dial`)
	listen := regexp.MustCompile(`net\.Listen\(\s*"tcp[46]?"\s*,\s*"([^"]*)"`)
	for _, dir := range []string{"cmd", "internal"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			src := string(data)
			if strings.Contains(src, `"net/http"`) && !strings.HasPrefix(rel, "internal/web/") {
				t.Errorf("%s imports net/http; only internal/web may", rel)
			}
			if m := outbound.FindString(src); m != "" {
				t.Errorf("%s makes an outbound network call: %s", rel, m)
			}
			for _, m := range listen.FindAllStringSubmatch(src, -1) {
				if !strings.HasPrefix(m[1], "127.0.0.1:") {
					t.Errorf("%s listens on %q, only 127.0.0.1 is allowed", rel, m[1])
				}
			}
			return nil
		})
	}
}
