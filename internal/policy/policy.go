// Package policy implements the hard safety denylist and root containment.
// It has no dependency on rules or the engine and is consulted before any
// path is measured.
package policy

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Policy decides whether a resolved absolute path may be a candidate.
type Policy struct {
	Home      string
	Roots     []string // Home plus configured extra roots
	InfoRoots []string // allowed for information-only (keep) candidates, never for the Trash
}

// Decision is the result of Check.
type Decision struct {
	Allowed bool
	Reason  string
}

// New builds a policy for home with optional extra allowed roots.
func New(home string, extraRoots ...string) *Policy {
	roots := []string{filepath.Clean(home)}
	for _, r := range extraRoots {
		roots = append(roots, filepath.Clean(r))
	}
	return &Policy{Home: filepath.Clean(home), Roots: roots, InfoRoots: []string{"/Applications", "/System/Applications"}}
}

// InfoOnly reports whether abs lies under an info root: it may be shown but
// never selected for the Trash, whatever its verdict.
func (p *Policy) InfoOnly(abs string) bool {
	abs = filepath.Clean(abs)
	for _, r := range p.InfoRoots {
		if under(abs, r) {
			return true
		}
	}
	return false
}

// Directories relative to home that may never be candidates, themselves or via ancestors.
var deniedPrefixes = []string{
	"Documents", "Desktop", "Pictures", "Movies", "Music",
	".ssh", ".gnupg", ".aws", ".config", ".Trash",
	"Library/Keychains", "Library/Caches/macsweep",
}

// Basename patterns that deny the path and everything beneath it.
var deniedNames = []string{
	".git", "*.photoslibrary", "*.sparsebundle", "*.keychain", "*.keychain-db", "*.pem", "*.p12",
}

// Files whose presence marks a directory as a source tree.
var sourceMarkers = []string{
	".git", ".hg", ".svn", "go.mod", "package.json", "Cargo.toml", "pyproject.toml",
	"setup.py", "pom.xml", "build.gradle", "build.gradle.kts", "Package.swift", "*.xcodeproj",
}

// StructuralDirs are containers that hold candidates but can never be one.
func (p *Policy) StructuralDirs() []string {
	lib := filepath.Join(p.Home, "Library")
	return []string{
		p.Home, lib,
		filepath.Join(lib, "Application Support"),
		filepath.Join(lib, "Caches"),
		filepath.Join(lib, "Containers"),
		filepath.Join(lib, "Group Containers"),
		filepath.Join(lib, "Developer"),
	}
}

// Check evaluates abs, which must already be absolute and symlink-resolved.
func (p *Policy) Check(abs string) Decision {
	abs = filepath.Clean(abs)
	inRoot := p.InfoOnly(abs)
	for _, r := range p.Roots {
		if under(abs, r) {
			inRoot = true
			break
		}
	}
	if !inRoot {
		return Decision{Reason: "outside allowed roots"}
	}
	for _, d := range p.StructuralDirs() {
		if abs == d {
			return Decision{Reason: "structural directory " + display(d, p.Home)}
		}
	}
	for _, rel := range deniedPrefixes {
		d := filepath.Join(p.Home, rel)
		if under(abs, d) {
			return Decision{Reason: "denylisted location ~/" + rel}
		}
	}
	for cur := abs; ; cur = filepath.Dir(cur) {
		base := filepath.Base(cur)
		for _, pat := range deniedNames {
			if ok, _ := path.Match(pat, base); ok {
				if cur == abs {
					return Decision{Reason: "denylisted name " + pat}
				}
				return Decision{Reason: "inside denylisted " + pat + " (" + display(cur, p.Home) + ")"}
			}
		}
		if cur == "/" || filepath.Dir(cur) == cur {
			break
		}
	}
	if marker := sourceMarker(abs); marker != "" {
		return Decision{Reason: "looks like a source tree (contains " + marker + ")"}
	}
	return Decision{Allowed: true, Reason: "allowed"}
}

// sourceMarker returns the first project marker found directly inside dir, or "".
func sourceMarker(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		for _, m := range sourceMarkers {
			if ok, _ := path.Match(m, e.Name()); ok {
				return e.Name()
			}
		}
	}
	return ""
}

func under(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

func display(p, home string) string {
	if under(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// DeniedName reports whether a basename alone is enough to deny a path.
// It is a cheap pre-filter for directory walks; Check remains authoritative.
func DeniedName(base string) bool {
	for _, pat := range deniedNames {
		if ok, _ := path.Match(pat, base); ok {
			return true
		}
	}
	return false
}
