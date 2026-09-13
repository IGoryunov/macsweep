// Package manifest caches scan measurements between runs.
package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/IGoryunov/macsweep/internal/scan"
)

// Version is the manifest schema version. Other versions are ignored, never migrated.
const Version = 1

// Manifest is the on-disk cache.
type Manifest struct {
	Version   int           `json:"version"`
	CreatedAt time.Time     `json:"created_at"`
	Home      string        `json:"home"`
	Roots     []string      `json:"roots"`
	RulesHash string        `json:"rules_hash"`
	Results   []scan.Result `json:"results"`
}

// DefaultPath is ~/Library/Caches/macsweep/manifest.json.
func DefaultPath(home string) string {
	return filepath.Join(CacheDir(home), "manifest.json")
}

// CacheDir is ~/Library/Caches/macsweep.
func CacheDir(home string) string { return filepath.Join(home, "Library", "Caches", "macsweep") }

// Load returns the cached manifest when it exists, has the right version, is
// younger than ttl and matches home, roots and rules hash. Otherwise ok=false.
func Load(path string, want Manifest, ttl time.Duration) (*Manifest, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false
	}
	if m.Version != Version || time.Since(m.CreatedAt) > ttl || m.Home != want.Home || m.RulesHash != want.RulesHash || !sameSet(m.Roots, want.Roots) {
		return nil, false
	}
	for i := range m.Results {
		m.Results[i].ThawLinks()
	}
	return &m, true
}

// Save writes the manifest, creating the directory as needed.
func Save(path string, m Manifest) error {
	m.Version = Version
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	for i := range m.Results {
		m.Results[i].FreezeLinks()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ByPath indexes results by path.
func (m *Manifest) ByPath() map[string]scan.Result {
	out := make(map[string]scan.Result, len(m.Results))
	for _, r := range m.Results {
		out[r.Path] = r
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a, b = append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
