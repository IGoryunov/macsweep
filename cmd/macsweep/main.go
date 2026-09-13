// Command macsweep finds recoverable disk space on macOS and explains why each
// candidate is considered safe or not. This milestone never modifies the
// filesystem: there is no deletion code and no deletion flag.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/IGoryunov/macsweep/internal/analyze"
	"github.com/IGoryunov/macsweep/internal/apps"
	"github.com/IGoryunov/macsweep/internal/clean"
	"github.com/IGoryunov/macsweep/internal/config"
	"github.com/IGoryunov/macsweep/internal/engine"
	"github.com/IGoryunov/macsweep/internal/manifest"
	"github.com/IGoryunov/macsweep/internal/policy"
	"github.com/IGoryunov/macsweep/internal/procs"
	"github.com/IGoryunov/macsweep/internal/report"
	"github.com/IGoryunov/macsweep/internal/rules"
	"github.com/IGoryunov/macsweep/internal/scan"
	"github.com/IGoryunov/macsweep/internal/trash"
	"github.com/IGoryunov/macsweep/internal/tui"
	"github.com/IGoryunov/macsweep/internal/undo"
	"github.com/IGoryunov/macsweep/internal/web"
	rulesfs "github.com/IGoryunov/macsweep/rules"
)

const cacheTTL = 2 * time.Hour

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// version is set by the build script (-ldflags -X main.version=…).
var version = "dev"

func main() {
	os.Exit(run())
}

// launchedAsApp reports whether we were started by double-clicking the
// Macsweep.app bundle: the executable lives in Contents/MacOS and there are no
// arguments (LaunchServices adds a -psn_… argument on older systems).
func launchedAsApp() bool {
	exe, err := os.Executable()
	if err != nil || !strings.Contains(exe, ".app/Contents/MacOS/") {
		return false
	}
	for _, a := range os.Args[1:] {
		if !strings.HasPrefix(a, "-psn_") {
			return false
		}
	}
	return os.Getenv("MACSWEEP_APP_CHILD") == ""
}

// runAsApp is the double-click path. LaunchServices allows one instance of a
// bundle, so the bundle process only spawns a detached copy of itself running
// the web UI and exits at once; that way every double-click opens the UI
// again (the child stops when the browser tab is closed).
func runAsApp() int {
	exe, _ := os.Executable()
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, "Library", "Caches", "macsweep")
	_ = os.MkdirAll(logDir, 0o755)
	logf, err := os.OpenFile(filepath.Join(logDir, "app.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return alert("Macsweep cannot write its log: " + err.Error())
	}
	defer logf.Close()
	cmd := exec.Command(exe, "--web")
	cmd.Env = append(os.Environ(), "MACSWEEP_APP_CHILD=1")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return alert("Macsweep could not start: " + err.Error())
	}
	return 0
}

func alert(msg string) int {
	_ = exec.Command("osascript", "-e", fmt.Sprintf(`display alert "Macsweep" message %q`, msg)).Run()
	return 1
}

func run() int {
	if launchedAsApp() {
		return runAsApp()
	}
	if len(os.Args) > 1 && os.Args[1] == "version" || len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("macsweep", version)
		return 0
	}
	if len(os.Args) > 1 && os.Args[1] == "undo" {
		return runUndo(os.Args[2:])
	}
	if len(os.Args) > 2 && os.Args[1] == "docker" && os.Args[2] == "prune" {
		return runDockerPrune(os.Args[3:])
	}
	var (
		jsonOut  = flag.Bool("json", false, "machine-readable JSON report on stdout")
		rulesDir = flag.String("rules", "", "load rules from DIR instead of the embedded set")
		rescan   = flag.Bool("rescan", false, "ignore the cached manifest and measure again")
		minSize  = flag.String("min-size", "", "hide candidates smaller than this (e.g. 100MB); default 50MB")
		explain  = flag.String("explain", "", "explain why PATH gets its verdict, then exit")
		workers  = flag.Int("workers", 0, "parallel workers; default min(NumCPU*2, 16)")
		verbose  = flag.Bool("v", false, "debug logging on stderr")
		discover = flag.Bool("discover", false, "walk all of ~ and list the largest directories not covered by rules")
		top      = flag.Int("top", 25, "with --discover: how many directories to list")
		useAn    = flag.Bool("analyzers", true, "run the heuristic analyzers (caches, orphans, unused apps, Downloads, Docker)")
		useWeb   = flag.Bool("web", false, "open the browser UI on a random loopback port")
		useTUI   = flag.Bool("tui", true, "interactive interface when stdout is a terminal (--tui=false for the plain report)")
		doClean  = flag.Bool("clean", false, "move the selected candidates to the Trash (asks for confirmation)")
		yes      = flag.Bool("yes", false, "with --clean: do not ask for confirmation")
		groups   multiFlag
		projects multiFlag
		selects  multiFlag
	)
	flag.Var(&selects, "select", "with --clean: trash exactly this candidate path (repeatable) instead of all safe ones")
	flag.Var(&groups, "group", "restrict to this group (repeatable, case-insensitive)")
	flag.Var(&projects, "projects", "project root to search for build artifacts (repeatable)")
	flag.Usage = usage
	flag.Parse()

	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	home, err := os.UserHomeDir()
	if err != nil {
		return fail("cannot determine home directory: %v", err)
	}
	home, _ = filepath.EvalSymlinks(home)
	if err := checkFullDiskAccess(home); err != nil {
		if os.Getenv("MACSWEEP_APP_CHILD") != "" {
			return alert(err.Error())
		}
		return fail("%v", err)
	}

	cfg, err := config.Load(config.DefaultPath(home))
	if err != nil {
		return fail("%v", err)
	}
	resolved, err := config.Resolve(cfg, config.Overrides{ProjectRoots: projects, MinSize: *minSize, Workers: *workers}, home)
	if err != nil {
		return fail("%v", err)
	}

	var rfs fs.FS = rulesfs.FS()
	if *rulesDir != "" {
		rfs = os.DirFS(*rulesDir)
	}
	set, err := rules.Load(rfs)
	if err != nil {
		return fail("%v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cacheDir := manifest.CacheDir(home)
	appsCh := make(chan *apps.Index, 1)
	go func() { appsCh <- apps.Load(ctx, home, cacheDir, cacheTTL) }()
	lazyApps := &lazyIndex{ch: appsCh}

	env := engine.Env{
		Home: home, ProjectRoots: resolved.ProjectRoots, Now: time.Now,
		Apps: lazyApps, Procs: procs.Snap(), Policy: policy.New(home),
		MinSize: resolved.MinSize, Log: log, UnusedAppDays: resolved.UnusedAppDays,
	}
	var analyzers []analyze.Analyzer
	if *useAn {
		analyzers = analyze.Default(resolved.Analyzers)
	}

	var progress chan scan.Progress
	showProgress := !*jsonOut && *explain == "" && isTerminal(os.Stderr)
	if showProgress {
		progress = make(chan scan.Progress, 8)
	}
	sc := scan.New(scan.Options{Workers: resolved.Workers, Progress: progress})

	if *explain != "" {
		ex, err := engine.Explain(ctx, env, set, sc, *explain)
		if err != nil {
			return fail("%v", err)
		}
		if *jsonOut {
			return emitJSON(ex)
		}
		report.WriteExplanation(os.Stdout, ex, home)
		return 0
	}

	want := manifest.Manifest{Home: home, Roots: resolved.ProjectRoots, RulesHash: rules.Hash(rfs)}
	var cached map[string]scan.Result
	if *doClean {
		if err := trash.Available(); err != nil {
			return fail("%v", err)
		}
		*rescan = true // never trash from stale measurements
	}
	if !*rescan {
		if m, ok := manifest.Load(manifest.DefaultPath(home), want, cacheTTL); ok {
			cached = m.ByPath()
			log.Debug("using cached manifest", "created", m.CreatedAt, "results", len(m.Results))
		}
	}

	if *useWeb {
		if err := trash.Available(); err != nil {
			return fail("%v", err)
		}
		srv, err := web.New(ctx, web.Deps{
			Env: env, Set: set, Groups: groups, Workers: resolved.Workers, Cached: cached, Analyzers: analyzers,
			Trasher: trash.System{}, Policy: env.Policy, HeartbeatTimeout: 20 * time.Second,
			OnScan: func(rep *report.Report, measured []scan.Result) {
				want.Results = mergeResults(measured, cached)
				if err := manifest.Save(manifest.DefaultPath(home), want); err != nil {
					log.Warn("could not save manifest", "err", err)
				}
			},
			OnClean: func(j clean.Journal) {
				if _, err := clean.SaveJournal(clean.RunsDir(home), j); err != nil {
					log.Warn("could not write journal", "err", err)
				}
			},
		})
		if err != nil {
			return fail("%v", err)
		}
		url, errCh, err := srv.Start(ctx)
		if err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(os.Stderr, "macsweep web UI: %s\n(the server stops when you close the tab or press Ctrl-C)\n", url)
		if err := exec.Command("open", url).Run(); err != nil {
			log.Warn("could not open browser", "err", err)
		}
		if err := <-errCh; err != nil && !errors.Is(err, web.ErrServerClosed) {
			return fail("%v", err)
		}
		return 0
	}

	if *useTUI && isTerminal(os.Stdout) && !*jsonOut && !*discover && !*doClean {
		if err := trash.Available(); err != nil {
			return fail("%v", err)
		}
		code, err := tui.Run(ctx, tui.Deps{
			Env: env, Set: set, Groups: groups, Workers: resolved.Workers, Cached: cached, Analyzers: analyzers,
			Trasher: trash.System{}, Policy: env.Policy,
			OnScan: func(rep *report.Report, measured []scan.Result) {
				if lazyApps.loaded != nil && lazyApps.loaded.Err() != nil {
					rep.Warnings = append(rep.Warnings, "application index unavailable: "+lazyApps.loaded.Err().Error())
				}
				want.Results = mergeResults(measured, cached)
				if err := manifest.Save(manifest.DefaultPath(home), want); err != nil {
					log.Warn("could not save manifest", "err", err)
				}
			},
			OnClean: func(j clean.Journal) {
				if _, err := clean.SaveJournal(clean.RunsDir(home), j); err != nil {
					log.Warn("could not write journal", "err", err)
				}
			},
		})
		if err != nil {
			return fail("%v", err)
		}
		return code
	}

	var rep *report.Report
	var measured []scan.Result
	var runErr error
	work := func() {
		rep, measured, runErr = engine.Run(ctx, env, set, engine.Options{Groups: groups, Scanner: sc, Cached: cached, Analyzers: analyzers})
	}
	if showProgress {
		done := make(chan struct{})
		go func() {
			defer close(done)
			for p := range progress {
				fmt.Fprintf(os.Stderr, "\r\033[2K  scanning: %d paths, %s  %s", p.Paths, report.HumanSize(p.Bytes), truncate(report.DisplayPath(p.Current, home), 50))
			}
			fmt.Fprint(os.Stderr, "\r\033[2K")
		}()
		sc.WithProgress(work)
		close(progress)
		<-done
	} else {
		work()
	}
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) {
			return fail("cancelled")
		}
		return fail("%v", runErr)
	}
	if lazyApps.loaded != nil && lazyApps.loaded.Err() != nil {
		rep.Warnings = append(rep.Warnings, "application index unavailable, orphan detection degraded to review: "+lazyApps.loaded.Err().Error())
	}

	want.Results = mergeResults(measured, cached)
	if err := manifest.Save(manifest.DefaultPath(home), want); err != nil {
		log.Warn("could not save manifest", "err", err)
	}

	if *doClean {
		return runClean(ctx, rep, selects, *yes, *jsonOut, home, log)
	}
	if *discover {
		threshold := int64(1 << 30)
		if *minSize != "" {
			threshold = resolved.MinSize
		}
		var disc *engine.Discovery
		var derr error
		discWork := func() { disc, derr = engine.Discover(ctx, env, sc, rep, threshold, *top) }
		if showProgress {
			progress = make(chan scan.Progress, 8)
			sc = scan.New(scan.Options{Workers: resolved.Workers, Progress: progress})
			done := make(chan struct{})
			go func() {
				defer close(done)
				for p := range progress {
					fmt.Fprintf(os.Stderr, "\r\033[2K  discovering: %d paths, %s  %s", p.Paths, report.HumanSize(p.Bytes), truncate(report.DisplayPath(p.Current, home), 50))
				}
				fmt.Fprint(os.Stderr, "\r\033[2K")
			}()
			sc.WithProgress(discWork)
			close(progress)
			<-done
		} else {
			discWork()
		}
		if derr != nil {
			return fail("%v", derr)
		}
		if *jsonOut {
			return emitJSON(disc)
		}
		rows := make([]report.DiscoveryRow, 0, len(disc.Hotspots))
		for _, h := range disc.Hotspots {
			rows = append(rows, report.DiscoveryRow{DisplayPath: h.DisplayPath, Bytes: h.Bytes, Status: h.Status, Detail: h.Detail, Remainder: h.Remainder})
		}
		report.WriteDiscovery(os.Stdout, home, disc.HomeBytes, disc.HomeFiles, disc.Threshold, rows, isTerminal(os.Stdout) && os.Getenv("NO_COLOR") == "")
		return 0
	}
	if *jsonOut {
		return emitJSON(rep)
	}
	report.WriteText(os.Stdout, rep, isTerminal(os.Stdout) && os.Getenv("NO_COLOR") == "")
	return 0
}

// runDockerPrune implements `macsweep docker prune [--select ID…] [--yes] [--json]`.
// Docker has no Trash: this is the one irreversible command, kept separate on purpose.
func runDockerPrune(args []string) int {
	fs := flag.NewFlagSet("docker prune", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	jsonOut := fs.Bool("json", false, "JSON output")
	var selects multiFlag
	fs.Var(&selects, "select", "remove only this image/container/volume id or name (repeatable)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `macsweep docker prune              remove unused Docker images, stopped containers, dangling volumes and build cache
  --select ID|NAME                 only these objects (repeatable)
  --yes                            no confirmation
  --json                           machine-readable output

WARNING: Docker objects have no Trash. This cannot be undone. Tagged images that
are still used by a container are never touched.
`)
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	items, warn := analyze.DockerItems(ctx, analyze.Env{})
	if warn != "" {
		return fail("%s", warn)
	}
	if len(selects) > 0 {
		var chosen []analyze.DockerItem
		for _, sel := range selects {
			found := false
			for _, it := range items {
				if it.ID == sel || it.Name == sel || strings.HasPrefix(it.ID, sel) {
					chosen = append(chosen, it)
					found = true
				}
			}
			if !found {
				return fail("--select %s: not an unused Docker object", sel)
			}
		}
		items = chosen
	}
	if len(items) == 0 {
		fmt.Fprintln(os.Stderr, "macsweep: nothing unused to remove")
		return 0
	}
	home, _ := os.UserHomeDir()
	var total int64
	fmt.Fprintf(os.Stderr, "\nWill remove (%d object(s)):\n", len(items))
	for _, it := range items {
		total += it.Bytes
		fmt.Fprintf(os.Stderr, "  %-11s %9s  %s\n", it.Kind, report.HumanSize(it.Bytes), it.Name)
	}
	fmt.Fprintf(os.Stderr, "\nThis CANNOT be undone: Docker has no Trash. Estimated %s.\n", report.HumanSize(total))
	if !*yes {
		if !isTerminal(os.Stdin) {
			return fail("refusing to remove without confirmation; pass --yes in non-interactive use")
		}
		fmt.Fprint(os.Stderr, "Type yes to continue: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(line) != "yes" {
			fmt.Fprintln(os.Stderr, "Cancelled. Nothing was changed.")
			return 1
		}
	}
	j := clean.Journal{Version: clean.JournalVersion, Kind: "docker", StartedAt: time.Now(), Home: home}
	for _, it := range items {
		o := clean.Outcome{Path: "docker://" + it.Kind + "/" + it.ID, RuleID: "docker/" + it.Kind, Verdict: "keep", Bytes: it.Bytes}
		if ctx.Err() != nil {
			o.Status, o.Reason = "skipped", "cancelled"
			j.Skipped++
		} else if out, err := analyze.DefaultExec(ctx, "docker", it.Args...); err != nil {
			o.Status, o.Reason = "failed", strings.TrimSpace(string(out))+" "+err.Error()
			j.Failed++
		} else {
			o.Status = "removed"
			j.Trashed++
			j.TrashedBytes += it.Bytes
		}
		j.Items = append(j.Items, o)
		if !*jsonOut {
			mark := map[string]string{"removed": "✓", "failed": "✗", "skipped": "–"}[o.Status]
			line := fmt.Sprintf("  %s %-11s %9s  %s", mark, it.Kind, report.HumanSize(it.Bytes), it.Name)
			if o.Reason != "" {
				line += "  (" + o.Reason + ")"
			}
			fmt.Println(line)
		}
	}
	j.FinishedAt = time.Now()
	jpath, jerr := clean.SaveJournal(clean.RunsDir(home), j)
	if *jsonOut {
		emitJSON(j)
	} else {
		fmt.Printf("\nRemoved %d object(s), about %s; failed %d.\n", j.Trashed, report.HumanSize(j.TrashedBytes), j.Failed)
		if jerr == nil {
			fmt.Printf("Journal (not restorable): %s\n", report.DisplayPath(jpath, home))
		}
	}
	if j.Failed > 0 || j.Skipped > 0 {
		return 2
	}
	return 0
}

// runUndo implements `macsweep undo [last|<run-id>] [--select PATH] [--yes] [--json]`.
func runUndo(args []string) int {
	fs := flag.NewFlagSet("undo", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	jsonOut := fs.Bool("json", false, "JSON output")
	var selects multiFlag
	fs.Var(&selects, "select", "restore only this original path (repeatable)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `macsweep undo                list recorded clean runs
macsweep undo last           restore everything from the most recent clean run
macsweep undo <run-id>       restore a specific run (prefix of the id is enough)
  --select PATH              restore only this original path (repeatable)
  --yes                      no confirmation
  --json                     machine-readable output

Items are moved back from the Trash to their original path. A path that exists
again (recreated by its application) is never overwritten and is skipped.
`)
	}
	// Accept flags before or after the run id: `undo last --yes` and `undo --yes last`.
	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return 1
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(positional) > 1 {
		return fail("undo takes at most one run id, got %v", positional)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fail("%v", err)
	}
	home, _ = filepath.EvalSymlinks(home)
	dir := clean.RunsDir(home)
	if len(positional) == 0 {
		runs, err := undo.ListRuns(dir)
		if err != nil {
			return fail("%v", err)
		}
		if *jsonOut {
			return emitJSON(runs)
		}
		if len(runs) == 0 {
			fmt.Println("No recorded runs. Journals appear here after --clean or a TUI/web trash action.")
			return 0
		}
		fmt.Printf("%-28s %-6s %6s %6s %10s\n", "run id", "kind", "items", "left", "size")
		for _, r := range runs {
			left := "-"
			if r.Kind == "clean" {
				left = fmt.Sprint(r.Restorable)
			}
			fmt.Printf("%-28s %-6s %6d %6s %10s\n", r.ID, r.Kind, r.Trashed, left, report.HumanSize(r.TrashedBytes))
		}
		fmt.Println("\n\"left\" = items still in the Trash. Restore with: macsweep undo last  (or a run id)")
		return 0
	}
	path, err := undo.Resolve(dir, positional[0])
	if err != nil {
		return fail("%v", err)
	}
	j, err := undo.Load(path)
	if err != nil {
		return fail("%v", err)
	}
	if j.Kind == "undo" || j.Kind == "docker" {
		return fail("%s is a %s journal, not a clean run; only Trash moves can be undone", positional[0], j.Kind)
	}
	abs := make([]string, 0, len(selects))
	for _, s := range selects {
		if s == "~" || strings.HasPrefix(s, "~/") {
			s = filepath.Join(home, strings.TrimPrefix(s, "~"))
		}
		a, _ := filepath.Abs(s)
		abs = append(abs, a)
	}
	items, err := undo.Plan(j, abs)
	if err != nil {
		return fail("%v", err)
	}
	if len(items) == 0 {
		fmt.Fprintln(os.Stderr, "macsweep: nothing to restore in this run")
		return 0
	}
	var total int64
	fmt.Fprintf(os.Stderr, "\nWill restore from the Trash (%d item(s)):\n", len(items))
	for _, it := range items {
		total += it.Bytes
		note := ""
		if _, err := os.Lstat(it.TrashedTo); err != nil {
			note = "  (no longer in the Trash, will be skipped)"
		} else if _, err := os.Lstat(it.Path); err == nil {
			note = "  (exists again, will be skipped)"
		}
		fmt.Fprintf(os.Stderr, "    %9s  %s%s\n", report.HumanSize(it.Bytes), report.DisplayPath(it.Path, home), note)
	}
	if !*yes {
		if !isTerminal(os.Stdin) {
			return fail("refusing to restore without confirmation; pass --yes in non-interactive use")
		}
		fmt.Fprintf(os.Stderr, "\nType yes to restore %d item(s), %s: ", len(items), report.HumanSize(total))
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(line) != "yes" {
			fmt.Fprintln(os.Stderr, "Cancelled. Nothing was changed.")
			return 1
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res := undo.Execute(ctx, items, home)
	jpath, jerr := clean.SaveJournal(dir, res)
	if *jsonOut {
		emitJSON(res)
	} else {
		for _, o := range res.Items {
			mark := "✓"
			switch o.Status {
			case "skipped":
				mark = "–"
			case "failed":
				mark = "✗"
			}
			line := fmt.Sprintf("  %s %9s  %s", mark, report.HumanSize(o.Bytes), report.DisplayPath(o.Path, home))
			if o.Reason != "" {
				line += "  (" + o.Reason + ")"
			}
			fmt.Println(line)
		}
		fmt.Printf("\nRestored: %s in %d item(s); skipped %d, failed %d.\n", report.HumanSize(res.TrashedBytes), res.Trashed, res.Skipped, res.Failed)
		if jerr == nil {
			fmt.Printf("Journal: %s\n", report.DisplayPath(jpath, home))
		}
	}
	if res.Skipped > 0 || res.Failed > 0 {
		return 2
	}
	return 0
}

// mergeResults keeps cached measurements for paths not re-measured this run.
func mergeResults(measured []scan.Result, cached map[string]scan.Result) []scan.Result {
	all := measured
	seen := map[string]bool{}
	for _, m := range measured {
		seen[m.Path] = true
	}
	for p, r := range cached {
		if !seen[p] {
			all = append(all, r)
		}
	}
	return all
}

// runClean builds the plan, confirms, trashes and journals. Exit codes per
// SPEC §8: 0 all trashed, 2 some skipped or failed, 1 error before acting.
func runClean(ctx context.Context, rep *report.Report, selects []string, yes, jsonOut bool, home string, log *slog.Logger) int {
	abs := make([]string, 0, len(selects))
	for _, s := range selects {
		if s == "~" || strings.HasPrefix(s, "~/") {
			s = filepath.Join(home, strings.TrimPrefix(s, "~"))
		}
		a, err := filepath.Abs(s)
		if err != nil {
			return fail("%v", err)
		}
		if r, err := filepath.EvalSymlinks(a); err == nil {
			a = r
		}
		abs = append(abs, a)
	}
	plan, err := clean.BuildPlan(rep, abs)
	if err != nil {
		return fail("%v", err)
	}
	if len(plan.Items) == 0 && len(plan.Blocked) == 0 {
		fmt.Fprintln(os.Stderr, "macsweep: nothing to trash")
		return 0
	}
	if !jsonOut || !yes {
		printPlan(os.Stderr, plan, home)
	}
	if !yes {
		if !isTerminal(os.Stdin) {
			return fail("refusing to trash without confirmation; pass --yes in non-interactive use")
		}
		fmt.Fprintf(os.Stderr, "\nType yes to move %d item(s), %s, to the Trash: ", len(plan.Items), report.HumanSize(plan.TotalBytes))
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(line) != "yes" {
			fmt.Fprintln(os.Stderr, "Cancelled. Nothing was changed.")
			return 1
		}
	}
	j := clean.Execute(ctx, plan, trash.System{}, policy.New(home), home)
	jpath, jerr := clean.SaveJournal(clean.RunsDir(home), j)
	if jerr != nil {
		log.Warn("could not write journal", "err", jerr)
	}
	if jsonOut {
		emitJSON(j)
	} else {
		for _, o := range j.Items {
			mark := "✓"
			switch o.Status {
			case "skipped":
				mark = "–"
			case "failed":
				mark = "✗"
			case "contained":
				continue
			}
			line := fmt.Sprintf("  %s %9s  %s", mark, report.HumanSize(o.Bytes), report.DisplayPath(o.Path, home))
			if o.Reason != "" {
				line += "  (" + o.Reason + ")"
			}
			fmt.Fprintln(os.Stdout, line)
		}
		fmt.Fprintf(os.Stdout, "\nMoved to Trash: %s in %d item(s); skipped %d, failed %d.\n", report.HumanSize(j.TrashedBytes), j.Trashed, j.Skipped, j.Failed)
		fmt.Fprintln(os.Stdout, "Space is reclaimed when you empty the Trash. Finder → Put Back restores any item.")
		if jerr == nil {
			fmt.Fprintf(os.Stdout, "Journal: %s\n", report.DisplayPath(jpath, home))
		}
	}
	if j.Skipped > 0 || j.Failed > 0 {
		return 2
	}
	return 0
}

func printPlan(w *os.File, plan clean.Plan, home string) {
	fmt.Fprintf(w, "\nWill move to the Trash (%s in %d item(s)):\n", report.HumanSize(plan.TotalBytes), len(plan.Items))
	group := ""
	for _, it := range plan.Items {
		if it.Group != group {
			group = it.Group
			fmt.Fprintf(w, "  %s\n", group)
		}
		fmt.Fprintf(w, "    %s %9s  %s\n", report.Mark(it.Verdict), report.HumanSize(it.Bytes), report.DisplayPath(it.Path, home))
	}
	if len(plan.Contained) > 0 {
		fmt.Fprintf(w, "  plus %d nested item(s) that go with their parent\n", len(plan.Contained))
	}
	if len(plan.Blocked) > 0 {
		fmt.Fprintf(w, "\nSkipped because their application is running (quit it and rerun): %s\n", strings.Join(plan.Apps, ", "))
		for _, it := range plan.Blocked {
			fmt.Fprintf(w, "    – %9s  %s\n", report.HumanSize(it.Bytes), report.DisplayPath(it.Path, home))
		}
	}
}

// lazyIndex blocks on the first query until the application index is ready,
// so lsregister runs concurrently with scanning.
type lazyIndex struct {
	ch     chan *apps.Index
	loaded *apps.Index
}

func (l *lazyIndex) get() *apps.Index {
	if l.loaded == nil {
		l.loaded = <-l.ch
	}
	return l.loaded
}

func (l *lazyIndex) InstalledByName(name string) (bool, error) { return l.get().InstalledByName(name) }

// All implements analyze.AppLister.
func (l *lazyIndex) All() []analyze.AppInfo {
	var out []analyze.AppInfo
	for _, a := range l.get().All() {
		out = append(out, analyze.AppInfo{Name: a.Name, Path: a.Path, BundleID: a.BundleID, TeamID: a.TeamID})
	}
	return out
}
func (l *lazyIndex) BundleRegistered(id string) (bool, error) { return l.get().BundleRegistered(id) }

func checkFullDiskAccess(home string) error {
	f, err := os.Open(filepath.Join(home, "Library", "Application Support"))
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf("cannot read ~/Library/Application Support: Full Disk Access is required.\n" +
				"Open System Settings → Privacy & Security → Full Disk Access and add your terminal application, then restart it")
		}
		return err
	}
	_, err = f.Readdirnames(1)
	f.Close()
	if err != nil && errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("cannot list ~/Library/Application Support: Full Disk Access is required.\n" +
			"Open System Settings → Privacy & Security → Full Disk Access and add your terminal application, then restart it")
	}
	return nil
}

func emitJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fail("%v", err)
	}
	return 0
}

func fail(format string, a ...any) int {
	fmt.Fprintf(os.Stderr, "macsweep: "+format+"\n", a...)
	return 1
}

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n+1:]
}

func usage() {
	fmt.Fprint(os.Stderr, `macsweep — find recoverable disk space on macOS, safely.

  macsweep                     interactive TUI when run in a terminal; nothing moves without confirmation
  macsweep --tui=false         plain text report instead
  macsweep --web               browser UI with a disk-usage sunburst (127.0.0.1 only)
  macsweep --json              machine-readable report on stdout
  macsweep --explain PATH      why does this path get its verdict?
  macsweep --discover          walk all of ~, list the largest directories no rule covers
  macsweep --discover --top 50 --min-size 500MB
  macsweep --clean             move all ✓ safe candidates to the Trash (asks first)
  macsweep --clean --yes       same, without asking (scripts)
  macsweep --clean --select ~/Library/Caches/Foo   trash exactly these candidates (any verdict but keep)
  macsweep undo [last|<run-id>]   restore a clean run from the Trash (see macsweep undo -h)
  macsweep docker prune           remove unused Docker objects (irreversible; asks first)
  macsweep --analyzers=false      rules only, skip the heuristic analyzers
  macsweep --group NAME        restrict to a group (repeatable)
  macsweep --projects DIR      project root for build artifacts (repeatable)
  macsweep --min-size 100MB    hide smaller candidates (default 50MB)
  macsweep --rescan            ignore the 2-hour manifest cache
  macsweep --rules DIR         use your own rules instead of the embedded ones
  macsweep --workers N         parallelism (default min(NumCPU*2, 16))
  macsweep -v                  debug logging

Config: ~/.config/macsweep/config.yaml (project_roots, min_size, workers).
Exit codes: 0 success, 1 error, 2 some items were skipped or failed.
Everything goes to the Finder Trash; nothing is deleted outright.
`)
}
