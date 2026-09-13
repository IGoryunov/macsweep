package scan

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/IGoryunov/macsweep/internal/rules"
)

// Query is one glob to resolve under a common root.
type Query struct {
	Pattern rules.Pattern
	Prune   bool            // do not descend into directories this query matches
	Exclude map[string]bool // basenames that never match this query
}

// StopFunc tells Expand not to enter (or match) a directory at all.
type StopFunc func(abs string) bool

// Expand walks root once (directories only, symlinks never followed) and
// returns, for each query index, the sorted directories it matched. All
// queries must share the same Pattern.Root == root.
func (s *Scanner) Expand(ctx context.Context, root string, qs []Query, stop StopFunc) (map[int][]string, error) {
	out := map[int][]string{}
	fi, err := os.Lstat(root)
	if err != nil || !fi.IsDir() {
		return out, nil // missing root is not an error: the tool is simply not installed
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var walk func(dir string, rel []string)
	walk = func(dir string, rel []string) {
		if ctx.Err() != nil {
			return
		}
		s.current.Store(&dir)
		s.paths.Add(1)
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, d := range ents {
			if !d.IsDir() { // symlinks have ModeSymlink, so they are excluded here
				continue
			}
			name := d.Name()
			child := filepath.Join(dir, name)
			if stop != nil && stop(child) {
				continue
			}
			crel := append(append(make([]string, 0, len(rel)+1), rel...), name)
			descend, pruned := false, false
			for i, q := range qs {
				if q.Exclude[name] {
					continue
				}
				if q.Pattern.Match(crel) {
					mu.Lock()
					out[i] = append(out[i], child)
					mu.Unlock()
					if q.Prune {
						pruned = true
					}
				}
				if q.Pattern.CanDescend(crel) {
					descend = true
				}
			}
			if !descend || pruned {
				continue
			}
			wg.Add(1)
			select {
			case s.sem <- struct{}{}:
				go func() {
					defer wg.Done()
					defer func() { <-s.sem }()
					walk(child, crel)
				}()
			default:
				walk(child, crel)
				wg.Done()
			}
		}
	}
	for i, q := range qs {
		if q.Pattern.Literal() {
			if q.Pattern.Root == root {
				out[i] = append(out[i], root)
			}
		}
	}
	hasWild := false
	for _, q := range qs {
		if !q.Pattern.Literal() {
			hasWild = true
		}
	}
	if hasWild {
		walk(root, nil)
		wg.Wait()
	}
	for i := range out {
		sort.Strings(out[i])
	}
	return out, ctx.Err()
}
