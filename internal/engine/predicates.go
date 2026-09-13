package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/IGoryunov/macsweep/internal/rules"
)

type predicate func(ctx context.Context, env Env, t Target, args map[string]any) (bool, error)

var registry = map[string]predicate{
	"app_installed":        predAppInstalled,
	"bundle_id_registered": predBundleRegistered,
	"mtime_older_than":     predMtimeOlderThan,
	"process_running":      predProcessRunning,
	"path_exists":          predPathExists,
	"larger_than":          predLargerThan,
}

var errNoApps = errors.New("application index unavailable")
var errNoProcs = errors.New("process list unavailable")

func predAppInstalled(_ context.Context, env Env, t Target, args map[string]any) (bool, error) {
	if env.Apps == nil {
		return false, errNoApps
	}
	name := t.Base
	if re := rules.ArgStr(args, "strip_version"); re != "" {
		r, err := regexp.Compile(re)
		if err != nil {
			return false, err
		}
		name = r.ReplaceAllString(name, "")
	}
	if m := rules.ArgStrMap(args, "map"); m != nil {
		mapped, ok := m[name]
		if !ok {
			return false, fmt.Errorf("app name %q not in map", name)
		}
		name = mapped
	}
	return env.Apps.InstalledByName(name)
}

func predBundleRegistered(_ context.Context, env Env, t Target, _ map[string]any) (bool, error) {
	if env.Apps == nil {
		return false, errNoApps
	}
	return env.Apps.BundleRegistered(t.Base)
}

func predMtimeOlderThan(_ context.Context, env Env, t Target, args map[string]any) (bool, error) {
	days := rules.ArgIntVal(args, "days")
	if days <= 0 {
		return false, fmt.Errorf("days must be positive")
	}
	if t.Result.MaxModTime.IsZero() {
		return false, fmt.Errorf("no measurement available")
	}
	return env.now().Sub(t.Result.MaxModTime) > time.Duration(days)*24*time.Hour, nil
}

func predProcessRunning(_ context.Context, env Env, _ Target, args map[string]any) (bool, error) {
	if env.Procs == nil {
		return false, errNoProcs
	}
	return env.Procs.Running(rules.ArgStr(args, "name"))
}

func predPathExists(_ context.Context, env Env, _ Target, args map[string]any) (bool, error) {
	paths, warn := rules.ExpandPath(rules.ArgStr(args, "path"), env.rulesEnv())
	if warn != "" {
		return false, errors.New(warn)
	}
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil {
			return true, nil
		}
	}
	return false, nil
}

func predLargerThan(_ context.Context, _ Env, t Target, args map[string]any) (bool, error) {
	return t.Result.Bytes > rules.ArgSizeVal(args, "bytes"), nil
}

// evalExpr evaluates "name" or "not name" against a target.
func evalExpr(ctx context.Context, env Env, t Target, expr string, args map[string]any) (bool, error) {
	name, not := rules.SplitPredicate(expr)
	p, ok := registry[name]
	if !ok {
		return false, fmt.Errorf("unknown predicate %q", name)
	}
	v, err := p(ctx, env, t, args)
	if err != nil {
		return false, err
	}
	return v != not, nil
}
