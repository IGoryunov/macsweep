---
name: macsweep-tune
description: Tune macsweep for this Mac by turning uncovered large directories into personal rules in ~/.config/macsweep/rules.d, validated with `macsweep rules lint`. Use when the user asks to personalise macsweep, add rules for their machine, explain why something is or is not proposed, or reduce "unknown" space in the disk map. Never runs cleanup.
---

# macsweep-tune

Follow `AGENTS.md` in the repository root; this skill is the step list.

1. Confirm the binary: `macsweep --version`. If missing, `make install` in
   the macsweep repository.
2. Gather facts once: `macsweep agent-context --top 40 > /tmp/macsweep-context.json`
   (30–60 s: it maps the whole home directory). Read `hotspots_uncovered`,
   `report.groups[].candidates[]` (`reasons`, `tags`) and `rules`.
3. For each uncovered hotspot, decide or ask. Ask one question per unclear
   directory, offering the three verdicts with one-line consequences:
   safe = trashable with one confirmation, review = shown for a decision,
   keep = information only.
4. Write rules to `~/.config/macsweep/rules.d/<topic>.yaml` following the
   skeleton in `AGENTS.md`. Group related paths in one file. Reuse an embedded
   rule's `id` to override its verdict for this person.
5. Validate: `macsweep rules lint` must print OK. Check one path per rule with
   `macsweep --explain <path>`; the output must name your rule and verdict.
6. Report: a table of id, path, verdict, one-line reason, and the total size
   that moved from "unknown" to covered. Remind the user that cleanup is theirs:
   `macsweep` (TUI) or `macsweep --web`.

Hard limits: no `--clean --yes`, no `docker prune --yes`, no edits under
`rules/` for personal needs, no `safe` without the user's confirmation or a
proving predicate.
