package rules

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Env is what placeholder expansion needs to know about the machine.
type Env struct {
	Home         string
	ProjectRoots []string
}

var placeholderNames = []string{"~", "$HOME", "$AS", "$CA", "$CT", "$GC", "$DEV", "$LOGS", "$PROJECTS"}

// Placeholders maps each placeholder to the concrete directories it stands for.
func Placeholders(env Env) map[string][]string {
	h := env.Home
	lib := filepath.Join(h, "Library")
	return map[string][]string{
		"~":         {h},
		"$HOME":     {h},
		"$AS":       {filepath.Join(lib, "Application Support")},
		"$CA":       {filepath.Join(lib, "Caches")},
		"$CT":       {filepath.Join(lib, "Containers")},
		"$GC":       {filepath.Join(lib, "Group Containers")},
		"$DEV":      {filepath.Join(lib, "Developer")},
		"$LOGS":     {filepath.Join(lib, "Logs")},
		"$PROJECTS": env.ProjectRoots,
	}
}

// HasAllowedPrefix reports whether p starts with a known placeholder segment.
func HasAllowedPrefix(p string) bool {
	first, _, _ := strings.Cut(p, "/")
	for _, n := range placeholderNames {
		if first == n {
			return true
		}
	}
	return false
}

// ExpandPath expands the leading placeholder of p into zero or more absolute
// paths. The returned warning is non-empty when the placeholder has no roots.
func ExpandPath(p string, env Env) (paths []string, warning string) {
	first, rest, _ := strings.Cut(p, "/")
	bases, ok := Placeholders(env)[first]
	if !ok {
		return nil, fmt.Sprintf("path %q has no placeholder prefix", p)
	}
	if len(bases) == 0 {
		return nil, fmt.Sprintf("placeholder %s has no roots configured", first)
	}
	for _, b := range bases {
		if rest == "" {
			paths = append(paths, filepath.Clean(b))
		} else {
			paths = append(paths, filepath.Join(b, rest))
		}
	}
	return paths, ""
}

// ExpandPaths expands every literal path of a rule.
func ExpandPaths(r Rule, env Env) (paths []string, warnings []string) {
	for _, p := range r.Paths {
		ps, w := ExpandPath(p, env)
		if w != "" {
			warnings = append(warnings, fmt.Sprintf("rule %s: %s", r.ID, w))
		}
		paths = append(paths, ps...)
	}
	return paths, warnings
}

// ExpandGlobs expands and compiles the glob of a rule, one pattern per root.
func ExpandGlobs(r Rule, env Env) (pats []Pattern, warnings []string) {
	if r.Glob == "" {
		return nil, nil
	}
	ps, w := ExpandPath(r.Glob, env)
	if w != "" {
		return nil, []string{fmt.Sprintf("rule %s: %s", r.ID, w)}
	}
	for _, p := range ps {
		pat, err := CompilePattern(p)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("rule %s: %v", r.ID, err))
			continue
		}
		pats = append(pats, pat)
	}
	return pats, warnings
}
