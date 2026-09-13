package rules

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// fileYAML mirrors one rules file on disk.
type fileYAML struct {
	Version int        `yaml:"version"`
	Group   string     `yaml:"group"`
	Rules   []ruleYAML `yaml:"rules"`
}

type ruleYAML struct {
	ID       string     `yaml:"id"`
	Paths    []string   `yaml:"paths"`
	Glob     string     `yaml:"glob"`
	Prune    bool       `yaml:"prune"`
	Exclude  []string   `yaml:"exclude"`
	Verdict  string     `yaml:"verdict"`
	When     []whenYAML `yaml:"when"`
	Note     string     `yaml:"note"`
	Recovery string     `yaml:"recovery"`
	MinSize  string     `yaml:"min_size"`
	OwnerApp string     `yaml:"owner_app"`
}

type whenYAML struct {
	If     string         `yaml:"if"`
	Args   map[string]any `yaml:"args"`
	And    []condYAML     `yaml:"and"`
	Then   string         `yaml:"then"`
	Reason string         `yaml:"reason"`
}

type condYAML struct {
	If   string         `yaml:"if"`
	Args map[string]any `yaml:"args"`
}

// Load reads every *.yaml / *.yml file at the root of fsys in lexical order,
// validates the whole set and returns it. Validation problems are returned as
// ValidationErrors listing every issue found.
func Load(fsys fs.FS) (*Set, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("rules: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := path.Ext(e.Name()); ext == ".yaml" || ext == ".yml" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("rules: no *.yaml files found")
	}

	set := &Set{byID: map[string]int{}}
	var errs ValidationErrors
	seenGroup := map[string]bool{}
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		var f fileYAML
		if err := dec.Decode(&f); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		fileErrs, rules := validateFile(name, f)
		errs = append(errs, fileErrs...)
		for _, r := range rules {
			if _, dup := set.byID[r.ID]; dup {
				errs = append(errs, fmt.Sprintf("%s:%s: duplicate rule id", name, r.ID))
				continue
			}
			set.byID[r.ID] = len(set.Rules)
			set.Rules = append(set.Rules, r)
		}
		if f.Group != "" && !seenGroup[f.Group] {
			seenGroup[f.Group] = true
			set.Groups = append(set.Groups, f.Group)
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return set, nil
}

// LoadUser loads the user's overlay rules from dir (typically
// ~/.config/macsweep/rules.d). A missing or empty directory yields nil, nil.
func LoadUser(dir string) (*Set, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("rules: %w", err)
	}
	hasYAML := false
	for _, e := range entries {
		if ext := path.Ext(e.Name()); !e.IsDir() && (ext == ".yaml" || ext == ".yml") {
			hasYAML = true
		}
	}
	if !hasYAML {
		return nil, nil
	}
	return Load(os.DirFS(dir))
}

// Merge layers overlay on top of base: an overlay rule with an existing id
// replaces the base rule in place, new rules are appended, new groups follow
// the base groups. Either argument may be nil.
func Merge(base, overlay *Set) *Set {
	out := &Set{byID: map[string]int{}}
	add := func(r Rule) {
		if i, ok := out.byID[r.ID]; ok {
			out.Rules[i] = r
			return
		}
		out.byID[r.ID] = len(out.Rules)
		out.Rules = append(out.Rules, r)
	}
	seenGroup := map[string]bool{}
	for _, set := range []*Set{base, overlay} {
		if set == nil {
			continue
		}
		for _, r := range set.Rules {
			add(r)
		}
		for _, g := range set.Groups {
			if !seenGroup[g] {
				seenGroup[g] = true
				out.Groups = append(out.Groups, g)
			}
		}
	}
	return out
}

// PlaceholderNames lists the path prefixes a rule may use.
func PlaceholderNames() []string { return append([]string(nil), placeholderNames...) }

// Hash returns a stable fingerprint of all rule files in fsys (used to
// invalidate the manifest cache when rules change).
func Hash(fsys fs.FS) string {
	var b strings.Builder
	entries, _ := fs.ReadDir(fsys, ".")
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if ext := path.Ext(e.Name()); !e.IsDir() && (ext == ".yaml" || ext == ".yml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		data, _ := fs.ReadFile(fsys, n)
		b.WriteString(n)
		b.WriteByte(0)
		b.Write(data)
		b.WriteByte(0)
	}
	return sha256Hex(b.String())
}
