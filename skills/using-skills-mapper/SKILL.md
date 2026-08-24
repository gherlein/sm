---
name: using-skills-mapper
description: Use when registering, syncing, listing, or removing AI-agent skill sources with the sm (skills-mapper) CLI, editing skills.toml, or when a skill is missing from an agent's skill list (claude-code, copilot, hax, pi, oh-my-pi) after being added to a repo or folder.
---

# Using skills-mapper (sm)

## Overview

`sm` places skills (directories containing a `SKILL.md`) from declared sources
(git repos or local paths) into each enabled agent's skills directory as
symlinks. It is manifest-driven: **you edit a TOML file, then run `sm sync`** —
placement only, no packaging.

## Critical Facts (save yourself the probing)

- **`sm add`, `remove`, `update`, `link` are stubs** — they return
  "not implemented". Registration is ALWAYS: edit the manifest, then `sm sync`.
- **`sm --help` and `sm help` fail.** Run bare `sm` to print usage.
- Global manifest: `~/.config/skills-mapper/skills.toml` (honors
  `XDG_CONFIG_HOME`). `sm init` creates a starter one.
- **Scope is auto-detected**: a `skills.toml` found walking up from the cwd
  (stopping before `$HOME`) switches sm to project scope. Force with
  `sm --global ...` or `sm --project ...` when in doubt.

## Manifest Format

```toml
[skills]
# alias = exactly ONE of git|path; ref/subdir optional, git-only
gherlein = { git = "git@github.com:gherlein/claude.git", ref = "main", subdir = "skills" }
local    = { path = "~/dev/my-skills" }   # symlinked in place, never cloned

[agents]          # unknown agent names fail loudly
claude-code = true    # -> ~/.claude/skills
copilot     = true    # -> ~/.copilot/skills
hax         = false   # -> ~/.config/hax/skills
pi          = false   # -> ~/.pi-go/skills
oh-my-pi    = false   # -> ~/.config/agents/skills

[options]
prefix_on_collision = false   # duplicate skill name across sources fails loud
```

## The Workflow

```bash
# 1. add/change the source entry in ~/.config/skills-mapper/skills.toml
# 2. parse check
sm list                    # alias, source, ref (tab-separated)
# 3. preview, then apply
sm sync --dry-run
sm -v sync                 # verbose steps on stderr, summary "global sync: +N -M"
# 4. verify
readlink ~/.claude/skills/<skill-name>
```

Agents scan skills at startup — restart the agent session to see a new skill.

## Gotchas

- **`--dry-run` counts can exceed what a real sync does**: dry-run skips the
  git-fetch step, and it counts links that a real sync will then *skip* with
  `warning: <path> exists and was not created by sm`. Those warnings are safe:
  sm never clobbers files it did not create.
- Local `path` sources are live symlinks — edits to the skill folder appear in
  agents immediately, no re-sync. Re-sync only when skills are added/removed
  or paths change.
- **Moved a source folder?** Fix the `path` in the manifest and re-run
  `sm sync`; it re-points the links (reconcile removes only links sm made).
- Git sources cache under `~/.local/share/skills-mapper/repos/`; link state
  lives in `~/.local/share/skills-mapper/state.json`.

## Reference

Full docs: `README.md` and `docs/DESIGN.md` in the sm repo
(github.com/gherlein/sm).
