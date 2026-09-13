package rules

import (
	"fmt"
	"path"
	"strings"
)

// Pattern is a compiled directory glob. Root is the literal prefix before the
// first wildcard segment; Segs are the remaining pattern segments, where "**"
// matches zero or more segments.
type Pattern struct {
	Raw  string
	Root string
	Segs []string
}

// CompilePattern compiles an absolute glob. It fails when ** appears more than once.
func CompilePattern(abs string) (Pattern, error) {
	if strings.Count(abs, "**") > 1 {
		return Pattern{}, fmt.Errorf("glob %q may contain ** at most once", abs)
	}
	parts := strings.Split(strings.TrimPrefix(abs, "/"), "/")
	i := 0
	for i < len(parts) && !hasMeta(parts[i]) {
		i++
	}
	root := "/" + strings.Join(parts[:i], "/")
	if i == 0 {
		root = "/"
	}
	segs := parts[i:]
	for _, s := range segs {
		if s == "" {
			return Pattern{}, fmt.Errorf("glob %q has an empty segment", abs)
		}
		if s != "**" && strings.Contains(s, "**") {
			return Pattern{}, fmt.Errorf("glob %q: ** must be a whole segment", abs)
		}
		if _, err := path.Match(s, ""); err != nil && s != "**" {
			return Pattern{}, fmt.Errorf("glob %q: %v", abs, err)
		}
	}
	return Pattern{Raw: abs, Root: root, Segs: segs}, nil
}

// Literal reports whether the pattern has no wildcards at all.
func (p Pattern) Literal() bool { return len(p.Segs) == 0 }

func hasMeta(s string) bool { return strings.ContainsAny(s, "*?[") }

// Match reports whether rel (segments below Root) is matched by the pattern.
func (p Pattern) Match(rel []string) bool { return matchSegs(p.Segs, rel) }

// CanDescend reports whether some deeper path under rel could still match.
func (p Pattern) CanDescend(rel []string) bool { return canDescend(p.Segs, rel) }

func matchSegs(segs, rel []string) bool {
	if len(segs) == 0 {
		return len(rel) == 0
	}
	if segs[0] == "**" {
		for i := 0; i <= len(rel); i++ {
			if matchSegs(segs[1:], rel[i:]) {
				return true
			}
		}
		return false
	}
	if len(rel) == 0 {
		return false
	}
	if ok, _ := path.Match(segs[0], rel[0]); !ok {
		return false
	}
	return matchSegs(segs[1:], rel[1:])
}

func canDescend(segs, rel []string) bool {
	if len(segs) == 0 {
		return false
	}
	if segs[0] == "**" {
		return true
	}
	if len(rel) == 0 {
		return true
	}
	if ok, _ := path.Match(segs[0], rel[0]); !ok {
		return false
	}
	return canDescend(segs[1:], rel[1:])
}
