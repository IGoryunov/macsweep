package rules

import (
	"fmt"
	"regexp"
	"strings"
)

// ValidationErrors lists every problem found while loading a rule set.
type ValidationErrors []string

func (e ValidationErrors) Error() string {
	return fmt.Sprintf("rules: %d validation error(s):\n  %s", len(e), strings.Join(e, "\n  "))
}

// ArgKind is the expected type of a predicate argument.
type ArgKind int

const (
	ArgInt       ArgKind = iota // positive integer
	ArgString                   // non-empty string
	ArgRegexp                   // compiles with regexp.Compile
	ArgStringMap                // map of string to string
	ArgSize                     // ParseSize-able string or integer
	ArgPath                     // placeholder-prefixed path
)

// ArgSpec describes one predicate argument.
type ArgSpec struct {
	Name     string
	Kind     ArgKind
	Required bool
}

// PredSpec describes a predicate's accepted arguments.
type PredSpec struct {
	Args []ArgSpec
}

// Predicates is the fixed registry of predicate names and their argument schemas.
// Implementations live in internal/engine; this table lets rules be validated
// without importing the engine.
var Predicates = map[string]PredSpec{
	"app_installed": {Args: []ArgSpec{
		{Name: "strip_version", Kind: ArgRegexp},
		{Name: "map", Kind: ArgStringMap},
	}},
	"bundle_id_registered": {},
	"mtime_older_than":     {Args: []ArgSpec{{Name: "days", Kind: ArgInt, Required: true}}},
	"process_running":      {Args: []ArgSpec{{Name: "name", Kind: ArgString, Required: true}}},
	"path_exists":          {Args: []ArgSpec{{Name: "path", Kind: ArgPath, Required: true}}},
	"larger_than":          {Args: []ArgSpec{{Name: "bytes", Kind: ArgSize, Required: true}}},
}

// SplitPredicate separates an optional "not " prefix from the predicate name.
func SplitPredicate(s string) (name string, not bool) {
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "not "); ok {
		return strings.TrimSpace(rest), true
	}
	return s, false
}

func validateFile(file string, f fileYAML) (ValidationErrors, []Rule) {
	var errs ValidationErrors
	if f.Version != 1 {
		errs = append(errs, fmt.Sprintf("%s: version must be 1 (got %d)", file, f.Version))
	}
	if strings.TrimSpace(f.Group) == "" {
		errs = append(errs, fmt.Sprintf("%s: group is required", file))
	}
	var out []Rule
	for i, ry := range f.Rules {
		id := ry.ID
		if strings.TrimSpace(id) == "" {
			errs = append(errs, fmt.Sprintf("%s: rule #%d has empty id", file, i+1))
			id = fmt.Sprintf("#%d", i+1)
		}
		prefix := file + ":" + id + ": "
		add := func(format string, a ...any) { errs = append(errs, prefix+fmt.Sprintf(format, a...)) }

		r := Rule{ID: ry.ID, Group: f.Group, Paths: ry.Paths, Glob: ry.Glob, Prune: ry.Prune,
			Exclude: ry.Exclude, Note: ry.Note, Recovery: ry.Recovery, OwnerApp: ry.OwnerApp}

		switch {
		case len(ry.Paths) > 0 && ry.Glob != "":
			add("paths and glob are mutually exclusive")
		case len(ry.Paths) == 0 && ry.Glob == "":
			add("either paths or glob is required")
		}
		for _, p := range ry.Paths {
			if !HasAllowedPrefix(p) {
				add("path %q must start with a placeholder (%s)", p, strings.Join(placeholderNames, ", "))
			}
		}
		if ry.Glob != "" {
			if !HasAllowedPrefix(ry.Glob) {
				add("glob %q must start with a placeholder (%s)", ry.Glob, strings.Join(placeholderNames, ", "))
			}
			if strings.Count(ry.Glob, "**") > 1 {
				add("glob %q may contain ** at most once", ry.Glob)
			}
		} else {
			if ry.Prune {
				add("prune requires glob")
			}
			if len(ry.Exclude) > 0 {
				add("exclude requires glob")
			}
		}
		if err := r.Verdict.UnmarshalText([]byte(ry.Verdict)); err != nil {
			if ry.Verdict == "" {
				add("verdict is required")
			} else {
				add("%v", err)
			}
		}
		if ry.MinSize != "" {
			n, err := ParseSize(ry.MinSize)
			if err != nil {
				add("min_size: %v", err)
			}
			r.MinSize = n
		}
		for j, w := range ry.When {
			wp := fmt.Sprintf("when[%d]: ", j)
			errs = append(errs, validatePredicate(prefix+wp, w.If, w.Args)...)
			var conds []Cond
			for k, c := range w.And {
				errs = append(errs, validatePredicate(prefix+wp+fmt.Sprintf("and[%d]: ", k), c.If, c.Args)...)
				conds = append(conds, Cond{If: c.If, Args: c.Args})
			}
			var then Verdict
			if w.Then == "" {
				add("%sthen is required", wp)
			} else if err := then.UnmarshalText([]byte(w.Then)); err != nil {
				add("%sthen: %v", wp, err)
			}
			if strings.TrimSpace(w.Reason) == "" {
				add("%sreason is required", wp)
			}
			r.When = append(r.When, When{If: w.If, Args: w.Args, And: conds, Then: then, Reason: w.Reason})
		}
		out = append(out, r)
	}
	return errs, out
}

func validatePredicate(prefix, expr string, args map[string]any) ValidationErrors {
	var errs ValidationErrors
	name, _ := SplitPredicate(expr)
	if name == "" {
		return append(errs, prefix+"if is required")
	}
	spec, ok := Predicates[name]
	if !ok {
		return append(errs, fmt.Sprintf("%sunknown predicate %q", prefix, name))
	}
	known := map[string]ArgSpec{}
	for _, a := range spec.Args {
		known[a.Name] = a
		if a.Required {
			if _, present := args[a.Name]; !present {
				errs = append(errs, fmt.Sprintf("%s%s: missing required argument %q", prefix, name, a.Name))
			}
		}
	}
	for k, v := range args {
		a, ok := known[k]
		if !ok {
			errs = append(errs, fmt.Sprintf("%s%s: unknown argument %q", prefix, name, k))
			continue
		}
		if err := checkArg(a, v); err != nil {
			errs = append(errs, fmt.Sprintf("%s%s: argument %q: %v", prefix, name, k, err))
		}
	}
	return errs
}

func checkArg(a ArgSpec, v any) error {
	switch a.Kind {
	case ArgInt:
		n, ok := v.(int)
		if !ok || n <= 0 {
			return fmt.Errorf("must be a positive integer, got %v", v)
		}
	case ArgString:
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return fmt.Errorf("must be a non-empty string, got %v", v)
		}
	case ArgRegexp:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("must be a regexp string, got %v", v)
		}
		if _, err := regexp.Compile(s); err != nil {
			return err
		}
	case ArgStringMap:
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("must be a map of strings, got %T", v)
		}
		for k, mv := range m {
			if _, ok := mv.(string); !ok {
				return fmt.Errorf("value for %q must be a string, got %T", k, mv)
			}
		}
	case ArgSize:
		switch x := v.(type) {
		case int:
			if x < 0 {
				return fmt.Errorf("must be non-negative")
			}
		case string:
			if _, err := ParseSize(x); err != nil {
				return err
			}
		default:
			return fmt.Errorf("must be a size string or integer, got %T", v)
		}
	case ArgPath:
		s, ok := v.(string)
		if !ok || !HasAllowedPrefix(s) {
			return fmt.Errorf("must be a placeholder-prefixed path, got %v", v)
		}
	}
	return nil
}

// ArgString returns a string argument or "".
func ArgStr(args map[string]any, name string) string {
	s, _ := args[name].(string)
	return s
}

// ArgStrMap returns a string→string map argument (nil when absent).
func ArgStrMap(args map[string]any, name string) map[string]string {
	m, ok := args[name].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k], _ = v.(string)
	}
	return out
}

// ArgIntVal returns an integer argument or 0.
func ArgIntVal(args map[string]any, name string) int {
	n, _ := args[name].(int)
	return n
}

// ArgSizeVal returns a size argument in bytes (string or int) or 0.
func ArgSizeVal(args map[string]any, name string) int64 {
	switch x := args[name].(type) {
	case int:
		return int64(x)
	case string:
		n, _ := ParseSize(x)
		return n
	}
	return 0
}
