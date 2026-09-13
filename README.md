# macsweep

Find recoverable disk space on macOS and understand *why* each candidate is
safe to remove. macsweep is a knowledge base of where macOS and developer tools
keep re-creatable data (caches, orphaned configs, build artifacts, toolchain
duplicates), plus heuristics that separate "the app will recreate this" from
"this is your data".

Nothing is ever deleted outright: `--clean` moves items to the Finder Trash
through `NSFileManager`, so **Put Back** works, and every run is journaled.

## Install

As a Mac application plus a terminal command sharing one binary:

```bash
make install
```

This builds `Macsweep.app` (icon included, ad-hoc signed), copies it to
`/Applications` and links `macsweep` into your PATH (Homebrew's `bin`,
`/usr/local/bin` or `~/.local/bin`). Double-clicking the app opens the browser
UI and quits when you close the tab; the `macsweep` command gives you the TUI,
`--json`, `undo` and `docker prune`. macOS may ask for Full Disk Access on the
first run to read protected folders such as Mail and Messages; grant it to
Macsweep in System Settings → Privacy & Security.

Command only, without the app:

```bash
go install github.com/IGoryunov/macsweep/cmd/macsweep@latest
```

Requires macOS 13+ and Full Disk Access for your terminal
(System Settings → Privacy & Security → Full Disk Access).

## Usage

```
macsweep                     scan and print a report; nothing is modified
macsweep --json              machine-readable report on stdout
macsweep --explain PATH      why does this path get its verdict?
macsweep --discover          walk all of ~ and list the largest directories no rule covers
macsweep --web               browser UI: disk-usage sunburst with verdicts, 127.0.0.1 only
macsweep --clean             move all ✓ safe candidates to the Trash (asks first)
macsweep --clean --yes       same without asking (scripts); exit 2 if anything was skipped
macsweep --clean --select ~/.m2/repository   trash exactly these candidates (any verdict but keep)
macsweep --group NAME        restrict to a group (repeatable)
macsweep --projects DIR      project root for build artifacts (repeatable)
macsweep --min-size 100MB    hide smaller candidates (default 50MB)
macsweep --rescan            ignore the 2-hour manifest cache
macsweep --rules DIR         use your own rules instead of the embedded ones
```

Verdicts: `✓ safe` — the owning application recreates the data;
`? review` — meaningful data, you decide; `- keep` — shown for information only.
Every candidate carries `reasons` explaining how the verdict was reached;
`--explain` shows the same for any path, including policy denials.

Optional config at `~/.config/macsweep/config.yaml`:

```yaml
project_roots: ["~/Work", "~/src"]   # default: autodetect ~/Work, ~/Projects, ~/src, ~/dev, ~/code, ~/repos, ~/go/src
min_size: 50MB
workers: 0                            # 0 = min(NumCPU*2, 16)
```

### Finding what the rules miss

`macsweep --discover` walks the whole home directory once (about 30 s for a
million files) and lists the largest directories. Each is tagged `unknown`
(no rule knows it: a candidate for a new rule), `covered` (already in the
report), `protected` (never a candidate, such as `~/Movies`) or `application`.
Use `--top N` and `--min-size` to tune the list. Nothing is deleted.

### Browser UI

Candidates are grouped and foldable, each group has a **mark all safe**
button. Downloads are split by age (older than a year, 180–365 days, …) with
the download date and source next to each file; unused applications show the
last launch date; the Docker section lists each unused object with a
copyable command.

`macsweep --web` starts an HTTP server on `127.0.0.1` with a random port and a
per-session token, opens your browser and exits when you close the tab. It
shows a sunburst of the candidates (safe green, review yellow, keep grey); the
**Full map** button walks the whole home directory so every directory appears,
with rule verdicts overlaid. Click sectors to zoom, use the side panel for
note, recovery, reasons and **Explain**, mark items and move them to the Trash
with the same checks as `--clean`. The page is plain SVG and JavaScript served
from the binary; nothing is loaded from the network.

### Undo

```
macsweep undo                list recorded clean runs and how many items are still in the Trash
macsweep undo last           put everything from the most recent run back where it was
macsweep undo <run-id> --select ~/Library/Caches/Foo
```

Items are moved from the Trash back to their journaled path. A path that
exists again (the application recreated it) is skipped, never overwritten;
an item you already removed from the Trash is reported as gone. Every undo is
journaled too.

### Cleaning

`--clean` re-measures (the cache is ignored), shows the plan grouped by rule
group with the total, lists running applications whose data will be skipped,
and asks you to type `yes`. Immediately before each move it re-checks that the
directory still has the same inode, has not been modified since the scan,
still resolves to itself and is still allowed by policy; anything that changed
is skipped with the reason. Results go to
`~/Library/Caches/macsweep/runs/<timestamp>.json`.

## Analyzers: results without writing rules

Besides the YAML rules, five analyzers look at the machine itself. Their
findings go through the same policy, verdict, Trash and journal pipeline; a
rule always wins when both name the same path. Switch any of them off in the
config (`analyzers: {docker: false}`) or all with `--analyzers=false`.

| Analyzer | What it finds | Verdict |
|---|---|---|
| Caches by convention | directories named `Cache`, `Caches`, `.cache`, `*Cache`, `tmp` inside app data and dotfiles | safe |
| Orphaned app data | Application Support, Containers, Group Containers, Caches, saved state and web storage whose application is not installed (by bundle id, name, vendor label or developer team id) | caches safe, data review |
| Unused applications | apps with no trace of activity for `unused_app_days` (default 180): Spotlight last-used date, saved window state, preferences, caches and container contents; bulk re-index timestamps are ignored | app keep, caches safe, data review |
| Downloads | each item in `~/Downloads` with its real download date (quarantine stamp or birth time) and source host; installers older than 30 days and unfinished downloads are safe | mostly review |
| Docker (advisor) | dangling and unused images, stopped containers, dangling volumes, build cache | keep, with the exact command |

Docker objects have no Trash, so macsweep never removes them from the report.
`macsweep docker prune [--select ID] [--yes]` does, after its own confirmation
and with a journal that `undo` refuses because nothing can be restored.

## Rule groups

System caches and logs, Browsers, Electron apps, JetBrains, Toolchains,
Projects (build artifacts under your project roots), Mobile development,
Docker, Games and Windows, Local AI models. See [`rules/`](rules/).

## Adding a rule

Rules are YAML files in [`rules/`](rules/), one group per file:

```yaml
version: 1
group: JetBrains
rules:
  - id: jetbrains-orphan-config          # unique, stable
    glob: "$AS/JetBrains/*"              # or paths: [...]; one ** allowed
    exclude: ["Toolbox"]                 # basenames a glob never matches
    prune: true                          # do not descend into matches
    verdict: review                      # base verdict: safe | review | keep
    note: IDE settings and plugins for one IDE version
    recovery: reinstall plugins and re-import settings
    when:                                # blocks are OR-ed; a block fires when all its predicates hold
      - if: not app_installed
        args: { strip_version: '[0-9]{4}\.[0-9]+$', map: { PyCharm: PyCharm } }
        then: safe
        reason: IDE is not installed, this configuration is orphaned
```

Placeholders (must be the first path segment): `~`, `$HOME`, `$AS`
(Application Support), `$CA` (Caches), `$CT` (Containers), `$GC` (Group
Containers), `$DEV` (Library/Developer), `$LOGS`, `$PROJECTS` (each project root).

Predicates: `app_installed` (`strip_version`, `map`), `bundle_id_registered`,
`mtime_older_than` (`days`), `process_running` (`name`), `path_exists` (`path`),
`larger_than` (`bytes`). Prefix with `not` to invert. Extra conditions go in
`and: [{if, args}]`.

Verdict rules: if any block fires, the most conservative fired verdict wins;
if none fires, the base verdict stays; if any predicate fails (for example the
application index is unavailable), the result is never below `review`.

Test your rules with `macsweep --rules ./rules --explain <path>`; validation
errors (unknown predicate, duplicate id, absolute path, missing reason) fail
loading with a list of every problem.

## Safety invariants

- Nothing outside `~` (and configured project roots) is ever a candidate.
- Hard denylist that no rule can override: `~/Documents`, `~/Desktop`,
  `~/Pictures`, `~/Movies`, `~/Music`, `~/.ssh`, `~/.gnupg`, `~/.aws`,
  `~/.config`, `~/.Trash`, keychains, any `.git`, `*.photoslibrary`,
  `*.sparsebundle`, and any directory that looks like a source tree.
- Unknown is `review`: a failing heuristic can only raise a verdict.
- Sizes are allocated blocks (what `du` reports); hardlinks are counted once
  per candidate and once across the report.
- No outbound network access: the only listener is the `--web` loopback
  server, and a test rejects any HTTP client use or non-127.0.0.1 listener.
- The only removal primitive is `trashItemAtURL:` in `internal/trash`; tests
  enforce that no `os.Remove*`, `unlink` or `removeItem` appears anywhere, and
  that `os.Rename` exists only in `internal/undo`, which moves items out of the
  Trash and never overwrites.
- Before each move the inode and mtime are compared with the scan; a changed
  directory is skipped, never trashed.
- Data of a running application (`owner_app`) is never touched.

## Known limitations

- APFS clones share extents but have distinct inodes; like `du`, macsweep
  counts them twice.
- Rules for application bundles in `/Applications` are not shipped: they lie
  outside the allowed roots.

## Development

```bash
make test          # go vet + go test
make bench         # scanner benchmark
go test -tags manual -run DuConformance ./internal/scan   # compare with du -sk
go test ./internal/engine -update                          # refresh golden JSON
```

## License

MIT, see [LICENSE](LICENSE).
