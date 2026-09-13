package engine

import (
	"context"
	"sort"
	"strings"

	"github.com/IGoryunov/macsweep/internal/report"
	"github.com/IGoryunov/macsweep/internal/scan"
)

// Hotspot is a large directory found by Discover.
type Hotspot struct {
	Path        string `json:"path"`
	DisplayPath string `json:"display_path"`
	Bytes       int64  `json:"bytes"`
	Files       int64  `json:"files"`
	Status      string `json:"status"` // unknown | covered | protected | application | remainder
	Detail      string `json:"detail"` // rule id, policy reason, or "" ; "remainder" marks own files
	Remainder   bool   `json:"remainder"`
}

// Discovery is the --discover output.
type Discovery struct {
	Home      string    `json:"home"`
	HomeBytes int64     `json:"home_bytes"`
	HomeFiles int64     `json:"home_files"`
	Threshold int64     `json:"threshold"`
	Hotspots  []Hotspot `json:"hotspots"`
}

// Discover walks all of env.Home and returns the largest directories that are
// not already explained by a rule candidate. rep may be nil.
func Discover(ctx context.Context, env Env, sc *scan.Scanner, rep *report.Report, threshold int64, top int) (*Discovery, error) {
	root, err := sc.Tree(ctx, env.Home, scan.DefaultSkip(env.Home))
	if err != nil {
		return nil, err
	}
	d := &Discovery{Home: env.Home, HomeBytes: root.Bytes, HomeFiles: root.Files, Threshold: threshold}

	covered := map[string]string{}
	if rep != nil {
		for _, g := range rep.Groups {
			for _, c := range g.Candidates {
				covered[c.Path] = c.RuleID
			}
		}
	}
	var raw []Hotspot
	hotspots(root, threshold, &raw)
	for i := range raw {
		h := &raw[i]
		h.DisplayPath = report.DisplayPath(h.Path, env.Home)
		if h.Remainder {
			h.Status = "remainder"
			continue
		}
		if app := appBundle(h.Path); app != "" {
			h.Status, h.Detail = "application", report.DisplayPath(app, env.Home)
			continue
		}
		if rule, ok := coveredBy(h.Path, covered); ok {
			h.Status, h.Detail = "covered", rule
			continue
		}
		if dec := env.Policy.Check(h.Path); !dec.Allowed {
			h.Status, h.Detail = "protected", dec.Reason
			continue
		}
		h.Status = "unknown"
	}
	sort.SliceStable(raw, func(i, j int) bool { return raw[i].Bytes > raw[j].Bytes })
	if top > 0 && len(raw) > top {
		raw = raw[:top]
	}
	d.Hotspots = raw
	return d, nil
}

// hotspots implements the descent rule: a directory above the threshold is
// reported itself unless its large children explain at least half of it, in
// which case we descend into them and report the remainder separately when
// it is still above the threshold.
func hotspots(n *scan.Node, threshold int64, out *[]Hotspot) {
	if n.Bytes < threshold {
		return
	}
	var big []*scan.Node
	var bigSum int64
	for _, c := range n.Children {
		if c.Bytes >= threshold {
			big = append(big, c)
			bigSum += c.Bytes
		}
	}
	// Stop here when the big children do not explain half of this directory,
	// or when a single child holds almost everything: then this directory is
	// the shortest name for the same space (e.g. ~/.nvm/.cache rather than a
	// seven-level chain beneath it).
	if len(big) == 0 || bigSum*2 < n.Bytes || (len(big) == 1 && big[0].Bytes*10 >= n.Bytes*9) {
		*out = append(*out, Hotspot{Path: n.Path, Bytes: n.Bytes, Files: n.Files})
		return
	}
	for _, c := range big {
		hotspots(c, threshold, out)
	}
	if rest := n.Bytes - bigSum; rest >= threshold {
		var restFiles int64 = n.Files
		for _, c := range big {
			restFiles -= c.Files
		}
		*out = append(*out, Hotspot{Path: n.Path, Bytes: rest, Files: restFiles, Remainder: true})
	}
}

func coveredBy(path string, covered map[string]string) (string, bool) {
	for p, rule := range covered {
		if path == p || strings.HasPrefix(path, p+"/") {
			return rule, true
		}
	}
	return "", false
}

// appBundle returns the enclosing *.app bundle path, or "".
func appBundle(path string) string {
	for cur := path; cur != "/" && cur != "."; cur = cur[:strings.LastIndex(cur, "/")] {
		if strings.HasSuffix(cur, ".app") {
			return cur
		}
		if !strings.Contains(cur, "/") {
			break
		}
	}
	return ""
}
