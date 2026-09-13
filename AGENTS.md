# Working on this machine with macsweep as an agent

This file is for automated assistants (Claude Code, Codex, Cursor agents…)
that help a person tune macsweep for *their* Mac. It is a contract, not a
tutorial: follow it literally.

## What you may do

- Read: `macsweep agent-context` (one JSON document), `macsweep --json`,
  `macsweep --discover --json`, `macsweep --explain PATH --json`,
  `macsweep rules list`.
- Write **personal rules** into `~/.config/macsweep/rules.d/*.yaml` and
  settings into `~/.config/macsweep/config.yaml`. Validate with
  `macsweep rules lint`; confirm the effect with `macsweep --explain PATH`.
- Ask the person what an unknown directory is before deciding.

## What you must not do

- Never run `macsweep --clean --yes`, `macsweep undo … --yes` or
  `macsweep docker prune --yes`. Removal is the person's action, through the
  TUI, the web UI or an explicit command with its confirmation prompt.
- Never put `safe` on something you inferred yourself. `safe` means "the
  application recreates it": use it only when the person confirmed it, or when
  a predicate can prove it (for example `mtime_older_than`, `not app_installed`).
  Everything else is `review`, with an honest `note`.
- Never touch the embedded rules under `rules/` in this repository for a
  personal need; that directory is the shared knowledge base. Override an
  embedded rule by reusing its `id` in your personal file instead.
- Do not try to disable the safety layer: the denylist (`~/Documents`,
  `~/.ssh`, `.git`, …), Trash-only removal and the inode check before every
  move are not configurable, and rules pointing outside `~` fail validation.

## The tuning loop

1. `macsweep agent-context --top 40` → read `hotspots_uncovered` (large
   directories with no rule), `report.groups` (current candidates and their
   reasons), `rules` (what exists), `rule_schema` (how to write rules).
2. For each uncovered hotspot decide what it is. Use the directory name, its
   parent application, `macsweep --explain PATH`, and ask the person when the
   purpose is not obvious. Typical outcomes:
   - a cache or build output that the tool recreates → `safe`, describe the
     recreation in `recovery`;
   - data the person may want (projects, exports, VMs, models) → `review` with a
     `note` that says what is inside;
   - something that must never be proposed → `keep`;
   - already covered by a shipped rule with a wrong verdict for this person →
     same `id`, new verdict, in the personal file.
3. Write the rule to `~/.config/macsweep/rules.d/<topic>.yaml` (one group per
   file, `version: 1`). Prefer literal `paths` over `glob`; use `$PROJECTS/**/x`
   with `prune: true` for build artifacts inside projects.
4. `macsweep rules lint` must print OK. Then `macsweep --explain <path>` must
   show your rule and the intended verdict.
5. Show the person a short summary: rule id, path, verdict, why. Stop there;
   they run the cleanup.

## Rule file skeleton

```yaml
version: 1
group: Personal
rules:
  - id: claude-vm-bundles
    paths: ["$AS/Claude/vm_bundles"]
    verdict: review
    note: Claude desktop VM images (about 11 GB); re-downloaded when a VM feature is used again
    recovery: Claude downloads the bundle again on demand
  - id: nvm-cache            # same id as the embedded rule: this overrides it
    paths: ["~/.nvm/.cache"]
    verdict: safe
    note: nvm source-build cache
    recovery: re-downloaded on the next nvm install
```

Placeholders: `~`, `$HOME`, `$AS` (Application Support), `$CA` (Caches),
`$CT` (Containers), `$GC` (Group Containers), `$DEV` (Library/Developer),
`$LOGS`, `$PROJECTS`. Predicates: `app_installed`, `bundle_id_registered`,
`mtime_older_than`, `process_running`, `path_exists`, `larger_than`; prefix
with `not`. Full schema with argument kinds is in `agent-context` under
`rule_schema`.

## Promoting a rule to the shared knowledge base

If a personal rule describes something everyone has (a well-known tool's
cache), propose it as a change to `rules/*.yaml` in this repository with a
test fixture, in English, with `note` and `recovery`. Personal paths and
personal verdicts stay personal.
