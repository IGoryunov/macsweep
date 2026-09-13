// Package apps answers "is application X installed?" from Launch Services,
// with a directory walk and mdfind as fallbacks. Any total failure is reported
// as an error so the engine can treat the answer as unknown.
package apps

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Index is the installed-application lookup.
type Index struct {
	Source   string            `json:"source"` // lsregister | walk | mdfind | cache
	ByName   map[string]string `json:"by_name"`
	ByBundle map[string]string `json:"by_bundle"`
	Teams    map[string]string `json:"teams,omitempty"` // bundle path → Apple Developer team id
	err      error
}

type cacheFile struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Index     Index     `json:"index"`
}

const cacheVersion = 2

// Failed returns a non-functional index that reports err for every query.
func Failed(err error) *Index { return &Index{err: err} }

// Err returns the load error, if any.
func (i *Index) Err() error { return i.err }

// InstalledByName reports whether an app bundle named name (without .app) exists.
func (i *Index) InstalledByName(name string) (bool, error) {
	if i.err != nil {
		return false, i.err
	}
	_, ok := i.ByName[strings.ToLower(strings.TrimSuffix(name, ".app"))]
	return ok, nil
}

// BundleRegistered reports whether a bundle identifier is known.
func (i *Index) BundleRegistered(id string) (bool, error) {
	if i.err != nil {
		return false, i.err
	}
	_, ok := i.ByBundle[strings.ToLower(id)]
	return ok, nil
}

// Info describes one installed application bundle.
type Info struct {
	Name     string
	Path     string
	BundleID string
	TeamID   string
}

// All lists every known application bundle, sorted by path.
func (i *Index) All() []Info {
	if i.err != nil {
		return nil
	}
	byPath := map[string]*Info{}
	for name, p := range i.ByName {
		byPath[p] = &Info{Name: name, Path: p}
	}
	for id, p := range i.ByBundle {
		if info, ok := byPath[p]; ok {
			info.BundleID = id
		} else {
			byPath[p] = &Info{Name: strings.ToLower(strings.TrimSuffix(filepath.Base(p), ".app")), Path: p, BundleID: id}
		}
	}
	out := make([]Info, 0, len(byPath))
	for _, info := range byPath {
		info.Name = strings.TrimSuffix(filepath.Base(info.Path), ".app")
		info.TeamID = i.Teams[info.Path]
		out = append(out, *info)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	return out
}

// Count returns the number of known applications.
func (i *Index) Count() int { return len(i.ByName) }

// Roots returns the directories whose bundles count as "installed".
func Roots(home string) []string {
	return []string{"/Applications", "/System/Applications", filepath.Join(home, "Applications")}
}

// Load builds the index, using cacheDir/apps.json when fresh.
func Load(ctx context.Context, home, cacheDir string, ttl time.Duration) *Index {
	cachePath := filepath.Join(cacheDir, "apps.json")
	if idx := loadCache(cachePath, ttl); idx != nil {
		return idx
	}
	roots := Roots(home)
	var errs []error
	for _, try := range []func(context.Context, []string) (*Index, error){fromLSRegister, fromWalk, fromMDFind} {
		idx, err := try(ctx, roots)
		if err == nil && idx.Count() > 0 {
			saveCache(cachePath, idx)
			return idx
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		errs = append(errs, errors.New("no applications found by any source"))
	}
	return Failed(errors.Join(errs...))
}

func loadCache(path string, ttl time.Duration) *Index {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var c cacheFile
	if json.Unmarshal(data, &c) != nil || c.Version != cacheVersion || time.Since(c.CreatedAt) > ttl || len(c.Index.ByName) == 0 {
		return nil
	}
	idx := c.Index
	idx.Source = "cache(" + idx.Source + ")"
	return &idx
}

func saveCache(path string, idx *Index) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(cacheFile{Version: cacheVersion, CreatedAt: time.Now(), Index: *idx})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

func newIndex(source string) *Index {
	return &Index{Source: source, ByName: map[string]string{}, ByBundle: map[string]string{}, Teams: map[string]string{}}
}

func (i *Index) add(path, bundleID string) {
	i.addWithTeam(path, bundleID, "")
}

func (i *Index) addWithTeam(path, bundleID, team string) {
	if team != "" {
		i.Teams[path] = team
	}
	name := strings.TrimSuffix(filepath.Base(path), ".app")
	if _, exists := i.ByName[strings.ToLower(name)]; !exists {
		i.ByName[strings.ToLower(name)] = path
	}
	if bundleID != "" {
		if _, exists := i.ByBundle[strings.ToLower(bundleID)]; !exists {
			i.ByBundle[strings.ToLower(bundleID)] = path
		}
	}
}

func underAny(p string, roots []string) bool {
	for _, r := range roots {
		if strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}
