package analyze

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/IGoryunov/macsweep/internal/policy"
	"github.com/IGoryunov/macsweep/internal/rules"
)

// Caches finds directories that are caches by naming convention.
type Caches struct{}

// Name implements Analyzer.
func (Caches) Name() string { return "Caches by convention" }

var cacheNames = map[string]bool{"Cache": true, "Caches": true, "cache": true, ".cache": true, "CachedData": true, "tmp": true, "Temp": true}

func isCacheName(name string) bool {
	if cacheNames[name] {
		return true
	}
	l := strings.ToLower(name)
	return strings.HasSuffix(l, "cache") && len(l) > 5 || strings.HasSuffix(l, "-cache") || strings.HasSuffix(l, "_cache")
}

// Analyze implements Analyzer.
func (c Caches) Analyze(ctx context.Context, env Env) ([]Finding, []string) {
	lib := filepath.Join(env.Home, "Library")
	var roots []string
	roots = append(roots, filepath.Join(lib, "Application Support"))
	roots = append(roots, children(filepath.Join(lib, "Group Containers"))...)
	for _, ct := range children(filepath.Join(lib, "Containers")) {
		roots = append(roots, filepath.Join(ct, "Data", "Library"))
	}
	pol := policy.New(env.Home)
	for _, e := range children(env.Home) {
		base := filepath.Base(e)
		if strings.HasPrefix(base, ".") && !policy.DeniedName(base) && pol.Check(e).Allowed {
			roots = append(roots, e)
		}
	}
	var out []Finding
	seen := map[string]bool{}
	for _, root := range roots {
		if ctx.Err() != nil {
			break
		}
		c.walk(root, 0, 3, func(dir string) {
			if seen[dir] {
				return
			}
			seen[dir] = true
			parent := ownerName(dir)
			out = append(out, Finding{
				Path: dir,
				Rule: rules.Rule{
					ID: "analyze/caches/" + strings.ToLower(filepath.Base(dir)), Group: c.Name(), Verdict: rules.Safe,
					Note:     "directory named like a cache (" + filepath.Base(dir) + ") inside " + parent,
					Recovery: "recreated on demand by " + parent,
				},
				Reasons: []string{"cache by naming convention"},
			})
		})
	}
	return out, nil
}

// walk visits directories to maxDepth, calling hit on cache-named ones and not
// descending into them or into denylisted names.
func (c Caches) walk(dir string, depth, maxDepth int, hit func(string)) {
	if depth > maxDepth {
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if policy.DeniedName(name) {
			continue
		}
		child := filepath.Join(dir, name)
		if isCacheName(name) {
			hit(child)
			continue
		}
		c.walk(child, depth+1, maxDepth, hit)
	}
}

func children(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

var structural = map[string]bool{"Library": true, "Data": true, "Application Support": true, "Caches": true, "Containers": true, "Group Containers": true}

// ownerName returns the nearest ancestor name that identifies an application
// rather than a structural folder (Library, Data, Application Support…).
func ownerName(dir string) string {
	for cur := filepath.Dir(dir); cur != "/" && cur != "."; cur = filepath.Dir(cur) {
		if b := filepath.Base(cur); !structural[b] {
			return b
		}
	}
	return filepath.Base(filepath.Dir(dir))
}
