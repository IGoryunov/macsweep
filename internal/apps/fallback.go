package apps

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"howett.net/plist"
)

func bundleID(appPath string) string {
	data, err := os.ReadFile(filepath.Join(appPath, "Contents", "Info.plist"))
	if err != nil {
		return ""
	}
	var info struct {
		CFBundleIdentifier string `plist:"CFBundleIdentifier"`
	}
	if _, err := plist.Unmarshal(data, &info); err != nil {
		return ""
	}
	return info.CFBundleIdentifier
}

// fromWalk lists *.app bundles at depth 1 and 2 under each root.
func fromWalk(_ context.Context, roots []string) (*Index, error) {
	idx := newIndex("walk")
	for _, root := range roots {
		l1, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range l1 {
			p := filepath.Join(root, e.Name())
			if strings.HasSuffix(e.Name(), ".app") {
				idx.add(p, bundleID(p))
				continue
			}
			if !e.IsDir() {
				continue
			}
			l2, err := os.ReadDir(p)
			if err != nil {
				continue
			}
			for _, f := range l2 {
				if strings.HasSuffix(f.Name(), ".app") {
					q := filepath.Join(p, f.Name())
					idx.add(q, bundleID(q))
				}
			}
		}
	}
	if idx.Count() == 0 {
		return nil, fmt.Errorf("walk: no application bundles under %v", roots)
	}
	return idx, nil
}

func fromMDFind(ctx context.Context, roots []string) (*Index, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "mdfind", "kMDItemContentType == 'com.apple.application-bundle'").Output()
	if err != nil {
		return nil, fmt.Errorf("mdfind: %w", err)
	}
	idx := newIndex("mdfind")
	for _, p := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if p != "" && underAny(p, roots) {
			idx.add(p, bundleID(p))
		}
	}
	if idx.Count() == 0 {
		return nil, fmt.Errorf("mdfind: no application bundles")
	}
	return idx, nil
}
