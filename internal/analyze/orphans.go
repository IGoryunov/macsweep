package analyze

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/IGoryunov/macsweep/internal/rules"
)

// Orphans finds application data whose application is no longer installed.
type Orphans struct{}

// Name implements Analyzer.
func (Orphans) Name() string { return "Orphaned app data" }

type orphanSource struct {
	rel     string
	verdict rules.Verdict
	note    string
}

var orphanSources = []orphanSource{
	{"Library/Application Support", rules.Review, "application data"},
	{"Library/Containers", rules.Review, "sandbox container"},
	{"Library/Group Containers", rules.Review, "app group container"},
	{"Library/Caches", rules.Safe, "cache"},
	{"Library/Saved Application State", rules.Safe, "saved window state"},
	{"Library/HTTPStorages", rules.Safe, "HTTP storage"},
	{"Library/WebKit", rules.Safe, "WebKit storage"},
}

var neverOrphan = regexp.MustCompile(`^(com\.apple\.|group\.com\.apple\.|Apple|CloudDocs$|AddressBook$|CallHistory|Knowledge$|FileProvider$|Dock$|iCloud|SyncServices$|CrashReporter$|MobileSync$|GeoServices$|com\.microsoft\.autoupdate|macsweep$)`)

var teamPrefix = regexp.MustCompile(`^[A-Z0-9]{10}\.`)

// aliases maps folder names to installed application names or bundle prefixes.
var aliases = map[string][]string{
	"Code":            {"Visual Studio Code"},
	"Code - Insiders": {"Visual Studio Code - Insiders"},
	"discord":         {"Discord"},
	"Google":          {"com.google."},
	"Microsoft":       {"com.microsoft."},
	"JetBrains":       {"com.jetbrains."},
	"Mozilla":         {"org.mozilla."},
	"Firefox":         {"org.mozilla."},
	"Adobe":           {"com.adobe."},
	"Slack":           {"com.tinyspeck."},
	"Zoom":            {"us.zoom."},
	"zoom.us":         {"us.zoom."},
	"Postman":         {"com.postmanlabs."},
	"Notion":          {"notion.id"},
}

// Analyze implements Analyzer.
func (o Orphans) Analyze(ctx context.Context, env Env) ([]Finding, []string) {
	if env.Apps == nil {
		return nil, []string{"orphan detection skipped: application index unavailable"}
	}
	apps := env.Apps.All()
	if len(apps) == 0 {
		return nil, []string{"orphan detection skipped: application index is empty"}
	}
	bundles := make([]string, 0, len(apps))
	teams := map[string]bool{}
	labels := map[string]bool{}
	for _, a := range apps {
		if a.BundleID != "" {
			bundles = append(bundles, strings.ToLower(a.BundleID))
			for _, l := range strings.Split(strings.ToLower(a.BundleID), ".") {
				if len(l) > 2 && l != "com" && l != "org" && l != "net" && l != "io" && l != "app" && l != "mac" {
					labels[l] = true
				}
			}
		}
		if a.TeamID != "" {
			teams[a.TeamID] = true
		}
	}
	resolver := ownerResolver{apps: env.Apps, bundles: bundles, teams: teams, labels: labels}
	var out []Finding
	for _, src := range orphanSources {
		dir := filepath.Join(env.Home, src.rel)
		for _, entry := range children(dir) {
			if ctx.Err() != nil {
				return out, nil
			}
			name := strings.TrimSuffix(filepath.Base(entry), ".savedState")
			if neverOrphan.MatchString(name) {
				continue
			}
			owner, unknown := resolver.owner(name)
			if owner || unknown {
				continue
			}
			out = append(out, Finding{
				Path: entry,
				Rule: rules.Rule{
					ID: "analyze/orphans/" + strings.ToLower(strings.ReplaceAll(filepath.Base(src.rel), " ", "-")), Group: o.Name(), Verdict: src.verdict,
					Note:     src.note + " of an application that is not installed",
					Recovery: "reinstalling the application recreates what it needs; settings inside may be worth exporting first",
				},
				Reasons: []string{fmt.Sprintf("no installed application matches %q", name)},
				Tags:    map[string]string{"owner": name, "location": filepath.Base(src.rel)},
			})
		}
	}
	return out, nil
}

type ownerResolver struct {
	apps    AppLister
	bundles []string        // lower-case bundle ids
	teams   map[string]bool // Apple Developer team ids of installed apps
	labels  map[string]bool // bundle-id labels (vendor and product names) of installed apps
}

// owner reports whether an installed application owns name. unknown is true
// when the lookup itself failed, so the caller stays silent.
func (r ownerResolver) owner(name string) (owned, unknown bool) {
	if m := teamPrefix.FindString(name); m != "" {
		if r.teams[strings.TrimSuffix(m, ".")] {
			return true, false // a container of an installed vendor's team
		}
		name = name[len(m):]
	}
	name = strings.TrimPrefix(name, "group.")
	if looksLikeBundleID(name) {
		ok, err := r.apps.BundleRegistered(name)
		if err != nil {
			return false, true
		}
		if ok {
			return true, false
		}
		labels := strings.Split(strings.ToLower(name), ".")
		if len(labels) >= 3 {
			prefix := strings.Join(labels[:3], ".")
			for _, b := range r.bundles {
				if b == prefix || strings.HasPrefix(b, prefix+".") {
					return true, false
				}
			}
		}
		return false, false
	}
	ok, err := r.apps.InstalledByName(name)
	if err != nil {
		return false, true
	}
	if ok {
		return true, false
	}
	for _, alt := range aliases[name] {
		if strings.HasSuffix(alt, ".") || strings.Contains(alt, ".") && !strings.Contains(alt, " ") {
			p := strings.ToLower(alt)
			for _, b := range r.bundles {
				if strings.HasPrefix(b, p) {
					return true, false
				}
			}
			continue
		}
		if ok, err := r.apps.InstalledByName(alt); err == nil && ok {
			return true, false
		}
	}
	// Vendor or product folders named after a bundle-id label: Google, NVIDIA, Adobe…
	if !strings.Contains(name, " ") && r.labels[strings.ToLower(name)] {
		return true, false
	}
	// "PyCharm2024.1" style: strip a trailing version.
	if stripped := regexp.MustCompile(`[ -]?[0-9]+(\.[0-9]+)*$`).ReplaceAllString(name, ""); stripped != name && stripped != "" {
		if ok, err := r.apps.InstalledByName(stripped); err == nil && ok {
			return true, false
		}
	}
	return false, false
}

func looksLikeBundleID(name string) bool {
	return strings.Count(name, ".") >= 2 && !strings.Contains(name, " ")
}
