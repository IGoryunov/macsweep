package analyze

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/IGoryunov/macsweep/internal/rules"
)

// UnusedApps reports applications that have not been launched for a long time,
// together with the data they keep. Bundles are information only (keep).
type UnusedApps struct{}

// Name implements Analyzer.
func (UnusedApps) Name() string { return "Unused applications" }

// DefaultUnusedAppDays is the threshold when the config does not set one.
const DefaultUnusedAppDays = 180

// Analyze implements Analyzer.
func (u UnusedApps) Analyze(ctx context.Context, env Env) ([]Finding, []string) {
	if env.Apps == nil {
		return nil, []string{"unused-application check skipped: application index unavailable"}
	}
	days := env.UnusedAppDays
	if days <= 0 {
		days = DefaultUnusedAppDays
	}
	roots := []string{"/Applications/", filepath.Join(env.Home, "Applications") + "/"}
	var apps []AppInfo
	for _, a := range env.Apps.All() {
		for _, r := range roots {
			if strings.HasPrefix(a.Path, r) && !strings.Contains(strings.TrimPrefix(a.Path, r), "/") {
				apps = append(apps, a)
			}
		}
	}
	if len(apps) == 0 {
		return nil, nil
	}
	lastUsed, err := u.lastUsed(ctx, env, apps)
	if err != nil {
		return nil, []string{"unused-application check skipped: " + err.Error()}
	}
	dropBulkTimestamps(lastUsed)
	now := env.now()
	lib := filepath.Join(env.Home, "Library")
	var out []Finding
	for _, a := range apps {
		t, known := lastUsed[a.Path]
		// Activity traces the app leaves when it runs: saved window state is
		// rewritten on every quit, preferences and caches when it works.
		for _, trace := range activityTraces(lib, a) {
			if nt := newestMtime(trace, 6, 5000); !nt.IsZero() && nt.After(t) {
				t, known = nt, true
			}
		}
		if !known || t.IsZero() {
			continue // nothing tells us when it ran: unknown, not unused
		}
		age := int(now.Sub(t).Hours() / 24)
		if age < days {
			continue
		}
		reason := fmt.Sprintf("application %s not launched for %d days (last %s)", a.Name, age, t.Format("2006-01-02"))
		out = append(out, Finding{
			Path: a.Path,
			Rule: rules.Rule{ID: "analyze/apps/bundle", Group: u.Name(), Verdict: rules.Keep,
				Note:     "application not launched since " + t.Format("2006-01-02"),
				Recovery: "macsweep never removes applications; drag it to the Trash from Finder if you no longer need it"},
			Reasons: []string{reason},
			Tags:    map[string]string{"last_used": t.Format("2006-01-02"), "unused_days": strconv.Itoa(age), "app": a.Name},
		})
		type dataDir struct {
			path    string
			verdict rules.Verdict
			note    string
		}
		var dirs []dataDir
		if a.BundleID != "" {
			dirs = append(dirs,
				dataDir{filepath.Join(lib, "Caches", a.BundleID), rules.Safe, "cache of an application not launched for a long time"},
				dataDir{filepath.Join(lib, "Containers", a.BundleID), rules.Review, "sandbox data of an application not launched for a long time"},
				dataDir{filepath.Join(lib, "Saved Application State", a.BundleID+".savedState"), rules.Safe, "saved window state of an unused application"},
			)
		}
		dirs = append(dirs,
			dataDir{filepath.Join(lib, "Caches", a.Name), rules.Safe, "cache of an application not launched for a long time"},
			dataDir{filepath.Join(lib, "Application Support", a.Name), rules.Review, "settings and data of an application not launched for a long time"},
			dataDir{filepath.Join(lib, "Logs", a.Name), rules.Safe, "logs of an unused application"},
		)
		for _, d := range dirs {
			if _, err := os.Lstat(d.path); err != nil {
				continue
			}
			kind := "data"
			switch d.verdict {
			case rules.Safe:
				kind = "cache"
			}
			out = append(out, Finding{
				Path: d.path,
				Rule: rules.Rule{ID: "analyze/apps/" + kind, Group: u.Name(), Verdict: d.verdict, Note: d.note,
					Recovery: "recreated if you launch " + a.Name + " again"},
				Reasons: []string{reason},
				Tags:    map[string]string{"last_used": t.Format("2006-01-02"), "unused_days": strconv.Itoa(age), "app": a.Name},
			})
		}
	}
	return out, nil
}

// lastUsed asks Spotlight for kMDItemLastUsedDate of every bundle in one call.
func (UnusedApps) lastUsed(ctx context.Context, env Env, apps []AppInfo) (map[string]time.Time, error) {
	sort.Slice(apps, func(i, j int) bool { return apps[i].Path < apps[j].Path })
	args := []string{"-raw", "-name", "kMDItemLastUsedDate"}
	for _, a := range apps {
		args = append(args, a.Path)
	}
	out, err := env.exec(ctx, "mdls", args...)
	if err != nil {
		return nil, fmt.Errorf("mdls: %w", err)
	}
	return parseMDLS(string(out), apps)
}

// parseMDLS decodes NUL-separated -raw output; "(null)" means unknown.
func parseMDLS(out string, apps []AppInfo) (map[string]time.Time, error) {
	parts := strings.Split(out, "\x00")
	if len(parts) < len(apps) {
		return nil, fmt.Errorf("mdls returned %d values for %d apps", len(parts), len(apps))
	}
	res := map[string]time.Time{}
	for i, a := range apps {
		v := strings.TrimSpace(parts[i])
		if v == "" || v == "(null)" {
			continue
		}
		t, err := time.Parse("2006-01-02 15:04:05 -0700", v)
		if err != nil {
			continue
		}
		res[a.Path] = t
	}
	return res, nil
}

// activityTraces lists files whose mtime moves when the application runs.
func activityTraces(lib string, a AppInfo) []string {
	var out []string
	if a.BundleID != "" {
		out = append(out,
			filepath.Join(lib, "Saved Application State", a.BundleID+".savedState"),
			filepath.Join(lib, "Preferences", a.BundleID+".plist"),
			filepath.Join(lib, "Caches", a.BundleID),
			filepath.Join(lib, "Containers", a.BundleID, "Data"),
			filepath.Join(lib, "Group Containers", "group."+a.BundleID),
			filepath.Join(lib, "HTTPStorages", a.BundleID),
		)
	}
	out = append(out, filepath.Join(lib, "Application Support", a.Name), filepath.Join(lib, "Caches", a.Name), filepath.Join(lib, "Logs", a.Name))
	return out
}

// dropBulkTimestamps removes Spotlight dates shared by three or more apps to
// the second: those come from re-indexing or system updates, not from use.
func dropBulkTimestamps(m map[string]time.Time) {
	count := map[int64]int{}
	for _, t := range m {
		count[t.Unix()]++
	}
	for p, t := range m {
		if count[t.Unix()] >= 3 {
			delete(m, p)
		}
	}
}

// newestMtime returns the newest mtime found in path and, for directories, in
// its contents down to maxDepth levels, looking at no more than maxEntries
// entries so a large data directory stays cheap to probe. Zero when missing.
func newestMtime(path string, maxDepth, maxEntries int) time.Time {
	fi, err := os.Lstat(path)
	if err != nil {
		return time.Time{}
	}
	newest := fi.ModTime()
	if !fi.IsDir() {
		return newest
	}
	budget := maxEntries
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > maxDepth || budget <= 0 {
			return
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			budget--
			if budget <= 0 {
				return
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			if info.ModTime().After(newest) {
				newest = info.ModTime()
			}
			if e.IsDir() {
				walk(filepath.Join(dir, e.Name()), depth+1)
			}
		}
	}
	walk(path, 1)
	return newest
}
