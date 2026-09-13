package analyze

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/IGoryunov/macsweep/internal/rules"
	"github.com/IGoryunov/macsweep/internal/testutil/fixture"
)

type fakeApps struct {
	apps []AppInfo
	err  error
}

func (f fakeApps) All() []AppInfo { return f.apps }
func (f fakeApps) InstalledByName(n string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	for _, a := range f.apps {
		if strings.EqualFold(a.Name, n) {
			return true, nil
		}
	}
	return false, nil
}
func (f fakeApps) BundleRegistered(id string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	for _, a := range f.apps {
		if strings.EqualFold(a.BundleID, id) {
			return true, nil
		}
	}
	return false, nil
}

func byPath(fs []Finding) map[string]Finding {
	m := map[string]Finding{}
	for _, f := range fs {
		m[f.Path] = f
	}
	return m
}

func TestCaches(t *testing.T) {
	home := fixture.Build(t, fixture.Tree{
		"Library/Application Support/App/Cache/f":       fixture.File{Size: 10},
		"Library/Application Support/App/GPUCache/f":    fixture.File{Size: 10},
		"Library/Application Support/App/Cache/inner/f": fixture.File{Size: 10}, // pruned
		"Library/Application Support/App/Data/f":        fixture.File{Size: 10},
		"Library/Containers/com.x/Data/Library/tmp/f":   fixture.File{Size: 10},
		"Library/Group Containers/G/Library/.cache/f":   fixture.File{Size: 10},
		".tool/cache/f":      fixture.File{Size: 10},
		".repo/.git/Cache/f": fixture.File{Size: 10}, // denied name inside a source tree
		".ssh/Cache/f":       fixture.File{Size: 10}, // denylisted root
		"Library/Application Support/a/b/c/d/e/Cache/f": fixture.File{Size: 10}, // too deep
	})
	fs, warns := Caches{}.Analyze(context.Background(), Env{Home: home})
	if len(warns) != 0 {
		t.Fatal(warns)
	}
	got := byPath(fs)
	for _, want := range []string{"Library/Application Support/App/Cache", "Library/Application Support/App/GPUCache", "Library/Containers/com.x/Data/Library/tmp", "Library/Group Containers/G/Library/.cache", ".tool/cache"} {
		f, ok := got[filepath.Join(home, want)]
		if !ok || f.Rule.Verdict != rules.Safe {
			t.Errorf("missing or wrong: %s (%+v)", want, f)
		}
	}
	for _, no := range []string{"Library/Application Support/App/Cache/inner", ".repo/.git/Cache", ".ssh/Cache", "Library/Application Support/a/b/c/d/e/Cache", "Library/Application Support/App/Data"} {
		if _, ok := got[filepath.Join(home, no)]; ok {
			t.Errorf("must not find %s", no)
		}
	}
}

func TestOrphans(t *testing.T) {
	home := fixture.Build(t, fixture.Tree{
		"Library/Application Support/Installed/f":                   fixture.File{Size: 10},
		"Library/Application Support/Gone/f":                        fixture.File{Size: 10},
		"Library/Application Support/Code/f":                        fixture.File{Size: 10}, // alias → Visual Studio Code
		"Library/Application Support/Google/f":                      fixture.File{Size: 10}, // alias → com.google.*
		"Library/Application Support/PyCharm2024.1/f":               fixture.File{Size: 10}, // version stripped
		"Library/Containers/com.gone.app/f":                         fixture.File{Size: 10},
		"Library/Containers/com.apple.thing/f":                      fixture.File{Size: 10},
		"Library/Containers/com.installed.suite.helper/f":           fixture.File{Size: 10}, // shares 3 labels
		"Library/Group Containers/ABCDE12345.com.installed.suite/f": fixture.File{Size: 10},
		"Library/Group Containers/TEAM123456.Office/f":              fixture.File{Size: 10}, // team id of an installed app
		"Library/Application Support/NVIDIA/f":                      fixture.File{Size: 10}, // vendor label of com.nvidia.gfn
		"Library/Caches/GeoServices/f":                              fixture.File{Size: 10}, // system
		"Library/Caches/gone/f":                                     fixture.File{Size: 10},
		"Library/Saved Application State/com.gone.app.savedState/f": fixture.File{Size: 10},
	})
	apps := fakeApps{apps: []AppInfo{
		{Name: "Installed", Path: "/Applications/Installed.app", BundleID: "com.installed.suite.main"},
		{Name: "Visual Studio Code", Path: "/Applications/Visual Studio Code.app", BundleID: "com.microsoft.VSCode"},
		{Name: "Google Chrome", Path: "/Applications/Google Chrome.app", BundleID: "com.google.Chrome"},
		{Name: "PyCharm", Path: "/Applications/PyCharm.app", BundleID: "com.jetbrains.pycharm"},
		{Name: "Microsoft Excel", Path: "/Applications/Microsoft Excel.app", BundleID: "com.microsoft.Excel", TeamID: "TEAM123456"},
		{Name: "GeForceNOW", Path: "/Applications/GeForceNOW.app", BundleID: "com.nvidia.gfn"},
	}}
	fs, warns := Orphans{}.Analyze(context.Background(), Env{Home: home, Apps: apps})
	if len(warns) != 0 {
		t.Fatal(warns)
	}
	got := byPath(fs)
	want := map[string]rules.Verdict{
		"Library/Application Support/Gone":                        rules.Review,
		"Library/Containers/com.gone.app":                         rules.Review,
		"Library/Caches/gone":                                     rules.Safe,
		"Library/Saved Application State/com.gone.app.savedState": rules.Safe,
	}
	if len(got) != len(want) {
		for p := range got {
			t.Logf("found %s", p)
		}
		t.Fatalf("found %d, want %d", len(got), len(want))
	}
	for rel, v := range want {
		f, ok := got[filepath.Join(home, rel)]
		if !ok || f.Rule.Verdict != v || !strings.Contains(f.Reasons[0], "no installed application") {
			t.Errorf("%s: %+v", rel, f)
		}
	}
	fs, warns = Orphans{}.Analyze(context.Background(), Env{Home: home, Apps: fakeApps{apps: apps.apps, err: errors.New("down")}})
	if len(fs) != 0 {
		t.Errorf("index error must yield no findings, got %d", len(fs))
	}
	if _, w := (Orphans{}).Analyze(context.Background(), Env{Home: home}); len(w) != 1 {
		t.Errorf("nil index must warn: %v", w)
	}
}

func TestUnusedApps(t *testing.T) {
	home := fixture.Build(t, fixture.Tree{
		"Library/Caches/com.old.app/f":         fixture.File{Size: 10},
		"Library/Application Support/OldApp/f": fixture.File{Size: 10},
	})
	apps := fakeApps{apps: []AppInfo{
		{Name: "OldApp", Path: "/Applications/OldApp.app", BundleID: "com.old.app"},
		{Name: "Fresh", Path: "/Applications/Fresh.app", BundleID: "com.fresh"},
		{Name: "Unknown", Path: "/Applications/Unknown.app", BundleID: "com.unknown"},
		{Name: "Helper", Path: "/Applications/Fresh.app/Contents/Helper.app", BundleID: "com.fresh.helper"},
	}}
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	exec := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "mdls" {
			t.Fatalf("unexpected command %s", name)
		}
		// apps sorted by path: Fresh, OldApp, Unknown
		return []byte("2026-09-01 10:00:00 +0000\x002025-01-15 10:00:00 +0000\x00(null)\x00"), nil
	}
	// OldApp's data dirs must look old too, otherwise recent activity traces win.
	old := time.Now().Add(-500 * 24 * time.Hour)
	for _, rel := range []string{"Library/Caches/com.old.app", "Library/Caches/com.old.app/f", "Library/Application Support/OldApp", "Library/Application Support/OldApp/f"} {
		if err := os.Chtimes(filepath.Join(home, rel), old, old); err != nil {
			t.Fatal(err)
		}
	}
	fs, warns := UnusedApps{}.Analyze(context.Background(), Env{Home: home, Apps: apps, Now: func() time.Time { return now }, Exec: exec, UnusedAppDays: 180})
	if len(warns) != 0 {
		t.Fatal(warns)
	}
	got := byPath(fs)
	if b, ok := got["/Applications/OldApp.app"]; !ok || b.Rule.Verdict != rules.Keep {
		t.Fatalf("bundle finding: %+v (all %v)", b, fs)
	}
	if c, ok := got[filepath.Join(home, "Library/Caches/com.old.app")]; !ok || c.Rule.Verdict != rules.Safe {
		t.Errorf("cache finding: %+v", c)
	}
	if d, ok := got[filepath.Join(home, "Library/Application Support/OldApp")]; !ok || d.Rule.Verdict != rules.Review {
		t.Errorf("data finding: %+v", d)
	}
	if len(got) != 3 {
		t.Errorf("want 3 findings, got %d", len(got))
	}
	_, warns = UnusedApps{}.Analyze(context.Background(), Env{Home: home, Apps: apps, Exec: func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no spotlight") }})
	if len(warns) != 1 {
		t.Errorf("mdls failure must warn: %v", warns)
	}
	// Three apps sharing one Spotlight timestamp: a re-index artefact, ignored.
	bulk := map[string]time.Time{"a": time.Unix(100, 0), "b": time.Unix(100, 0), "c": time.Unix(100, 0), "d": time.Unix(200, 0)}
	dropBulkTimestamps(bulk)
	if len(bulk) != 1 || bulk["d"].IsZero() {
		t.Errorf("bulk timestamps not dropped: %v", bulk)
	}
}

func TestDownloads(t *testing.T) {
	home := fixture.Build(t, fixture.Tree{
		"Downloads/old.dmg":        fixture.File{Size: 10, Age: 100 * fixture.Day},
		"Downloads/fresh.dmg":      fixture.File{Size: 10, Age: 2 * fixture.Day},
		"Downloads/data.zip":       fixture.File{Size: 10, Age: 400 * fixture.Day},
		"Downloads/doc.pdf":        fixture.File{Size: 10, Age: 50 * fixture.Day},
		"Downloads/folder/f":       fixture.File{Size: 10, Age: 10 * fixture.Day},
		"Downloads/.DS_Store":      fixture.File{Size: 10},
		"Downloads/q.pkg":          fixture.File{Size: 10},
		"Downloads/movie.mkv.part": fixture.File{Size: 10, Age: 3 * fixture.Day},
	})
	// Quarantine timestamp says q.pkg was downloaded 200 days ago even though the file is fresh.
	ts := time.Now().Add(-200 * fixture.Day).Unix()
	q := "0083;" + strings.ToLower(strings.TrimSpace(hex(ts))) + ";Safari;UUID"
	if err := unix.Setxattr(filepath.Join(home, "Downloads/q.pkg"), "com.apple.quarantine", []byte(q), 0); err != nil {
		t.Fatal(err)
	}
	fs, _ := Downloads{}.Analyze(context.Background(), Env{Home: home})
	got := byPath(fs)
	check := func(name string, v rules.Verdict, reason string) {
		f, ok := got[filepath.Join(home, "Downloads", name)]
		if !ok {
			t.Errorf("%s missing", name)
			return
		}
		if f.Rule.Verdict != v || !strings.Contains(strings.Join(f.Reasons, "|"), reason) || f.Rule.MinSize != DownloadsMinSize {
			t.Errorf("%s: %+v", name, f)
		}
	}
	check("old.dmg", rules.Safe, "90–180 days")
	check("fresh.dmg", rules.Review, "< 30 days")
	check("data.zip", rules.Review, "> 1 year")
	check("doc.pdf", rules.Review, "30–90 days")
	check("folder", rules.Review, "< 30 days")
	check("q.pkg", rules.Safe, "180–365 days")
	check("movie.mkv.part", rules.Safe, "< 30 days")
	if _, ok := got[filepath.Join(home, "Downloads/.DS_Store")]; ok {
		t.Error("hidden files must be skipped")
	}
}

func hex(n int64) string {
	const digits = "0123456789abcdef"
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{digits[n%16]}, b...)
		n /= 16
	}
	return string(b)
}

const (
	sampleImages = `{"Containers":"N/A","CreatedAt":"2026-08-01 10:00:00 +0000 UTC","CreatedSince":"5 weeks ago","Digest":"<none>","ID":"aaaa1111bbbb","Repository":"nginx","Tag":"latest","SharedSize":"N/A","Size":"187MB","UniqueSize":"N/A","VirtualSize":"187MB"}
{"Containers":"N/A","CreatedAt":"2026-07-01 10:00:00 +0000 UTC","CreatedSince":"2 months ago","Digest":"<none>","ID":"cccc2222dddd","Repository":"postgres","Tag":"16","SharedSize":"N/A","Size":"1.23GB","UniqueSize":"N/A","VirtualSize":"1.23GB"}
{"Containers":"N/A","CreatedAt":"2026-06-01 10:00:00 +0000 UTC","CreatedSince":"3 months ago","Digest":"<none>","ID":"eeee3333ffff","Repository":"<none>","Tag":"<none>","SharedSize":"N/A","Size":"512kB","UniqueSize":"N/A","VirtualSize":"512kB"}`
	samplePS = `{"Command":"\"docker-entrypoint.s…\"","CreatedAt":"2026-08-02 10:00:00 +0000 UTC","ID":"0123456789ab","Image":"nginx:latest","Labels":"","LocalVolumes":"0","Mounts":"","Names":"web","Networks":"bridge","Ports":"","RunningFor":"5 weeks ago","Size":"1.2kB (virtual 187MB)","State":"running","Status":"Up 3 hours"}
{"Command":"\"sh\"","CreatedAt":"2026-08-03 10:00:00 +0000 UTC","ID":"fedcba987654","Image":"alpine:3.19","Labels":"","LocalVolumes":"1","Mounts":"","Names":"scratch","Networks":"bridge","Ports":"","RunningFor":"4 weeks ago","Size":"45.2MB (virtual 52MB)","State":"exited","Status":"Exited (0) 3 weeks ago"}`
	sampleVolumes = `{"Availability":"N/A","Driver":"local","Group":"N/A","Labels":"","Links":"N/A","Mountpoint":"/var/lib/docker/volumes/orphanvol/_data","Name":"orphanvol","Scope":"local","Size":"N/A","Status":"N/A"}`
	sampleDF      = `{"Active":"1","Reclaimable":"1.23GB (86%)","Size":"1.42GB","TotalCount":"3","Type":"Images"}
{"Active":"1","Reclaimable":"45.2MB (100%)","Size":"45.2MB","TotalCount":"2","Type":"Containers"}
{"Active":"0","Reclaimable":"0B","Size":"0B","TotalCount":"1","Type":"Local Volumes"}
{"Active":"0","Reclaimable":"2.5GB","Size":"2.5GB","TotalCount":"14","Type":"Build Cache"}`
)

func fakeDocker(fail bool) ExecFunc {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		if fail {
			return nil, errors.New("Cannot connect to the Docker daemon")
		}
		switch strings.Join(args[:2], " ") {
		case "images --format":
			return []byte(sampleImages), nil
		case "ps -a":
			return []byte(samplePS), nil
		case "volume ls":
			return []byte(sampleVolumes), nil
		case "system df":
			return []byte(sampleDF), nil
		}
		return nil, errors.New("unexpected: " + strings.Join(args, " "))
	}
}

func TestDocker(t *testing.T) {
	items, warn := DockerItems(context.Background(), Env{Exec: fakeDocker(false)})
	if warn != "" {
		t.Fatal(warn)
	}
	kinds := map[string]DockerItem{}
	for _, it := range items {
		kinds[it.Kind+":"+it.Name] = it
	}
	if _, ok := kinds["image:nginx:latest"]; ok {
		t.Error("nginx is used by a running container and must not be listed")
	}
	if it, ok := kinds["image:postgres:16"]; !ok || it.Bytes != 1230000000 || it.Command != "docker rmi postgres:16" {
		t.Errorf("unused image: %+v", it)
	}
	if it, ok := kinds["image:<none> eeee3333ffff"]; !ok || it.Bytes != 512000 {
		t.Errorf("dangling image: %+v", it)
	}
	if it, ok := kinds["container:scratch"]; !ok || it.Bytes != 45200000 || it.Command != "docker rm fedcba987654" {
		t.Errorf("stopped container: %+v", it)
	}
	if it, ok := kinds["volume:orphanvol"]; !ok || it.Command != "docker volume rm orphanvol" {
		t.Errorf("volume: %+v", it)
	}
	if it, ok := kinds["build-cache:build cache"]; !ok || it.Bytes != 2500000000 {
		t.Errorf("build cache: %+v", it)
	}
	fs, warns := Docker{}.Analyze(context.Background(), Env{Exec: fakeDocker(false)})
	if len(fs) != 5 || fs[0].Rule.Verdict != rules.Keep || !strings.HasPrefix(fs[0].Path, "docker://") {
		t.Errorf("findings: %d %+v", len(fs), fs[0])
	}
	if _, warns = (Docker{}).Analyze(context.Background(), Env{Exec: fakeDocker(true)}); len(warns) != 1 || !strings.Contains(warns[0], "Docker Desktop") {
		t.Errorf("daemon down must warn: %v", warns)
	}
	_ = os.Getenv
}
