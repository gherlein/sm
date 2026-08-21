# skills-mapper — VISION

## What this is

**skills-mapper** (binary **`sm`**, Go) is a cross-agent bridge for **git-distributed AI
coding skills**. Skills are published as ordinary **git repos of `SKILL.md` directories** —
the open standard — and `sm` keeps those repos current on disk and links their skills into
every AI agent a developer uses. One library serves all agents, with no agent-specific
packaging.

The full design is in [`docs/DESIGN.md`](docs/DESIGN.md). This VISION captures the purpose,
the guiding philosophy, and the decisions that shaped it; the detailed reverse-engineering of
the prior-art tool `sk` is retained as [Appendix A](#appendix-a-reverse-engineered-sk-reference-prior-art).

## Philosophy

- **One standard, no drift.** The point of the tool is that skills (and `AGENTS.md`) follow a
  single standard every coding tool is expected to read. So there is **no plugin support** —
  bridging Claude Code plugins would let Claude users consume skills Copilot users can't,
  re-creating the very drift `sm` exists to kill.
- **Git repos are the universal distribution channel.** What Claude's plugin marketplace does
  for Claude only, a plain git repo of `SKILL.md`s does for everyone. `sm` is the last-mile
  adapter — nothing more.
- **Adaptation is placement, not transformation.** Because there is one standard, "adapt to
  each agent" collapses to putting the standard files where each agent looks — symlinks. A
  transform seam is left open but deliberately unused.
- **Compose, don't reinvent, except where composing costs more.** We evaluated GNU stow + a
  repo-updater. stow can't remap names (collision handling) or discover skills at arbitrary
  depth, so once `sm` computes the `source → target` mapping it symlinks directly — stow would
  be a redundant layer.

## Decisions (from brainstorming, 2026-08-21)

| Decision | Choice |
| --- | --- |
| Core model | Full manifest-driven tool: declare skill *sources*, sync them into agents |
| Sources | `git` repos (+ `ref`, `subdir`) and local `path`; **no** Claude plugins, **no** hosted registry |
| Target agents | **claude-code, copilot, hax, pi (pi-go), oh-my-pi (omp)** — each with global + project dirs; registry extensible (Codex/Amp/OpenCode later) |
| Scope | **Global** (default) and **project** (walk-up `skills.toml`); `--global`/`--project` force |
| Commands | `init`, `sync`, `update`, `link`, `add`, `remove`, `list` |
| Hosted auth / account | **Dropped entirely** |
| Manifest | Our own clean `skills.toml`, global at `~/.config/skills-mapper/`; explicit keyed handles |
| Cache | **Durable** clone cache at `~/.local/share/skills-mapper/repos/…`, kept fresh with `git pull` |
| Placement | Whole-directory symlinks; four steps: discover → resolve → link → reconcile |
| Names / collisions | Clean unprefixed names by default; **duplicate name across sources fails loud**; opt-in `prefix_on_collision` |
| Safety | Central state file; only ever touch links `sm` created; never clobber hand-placed skills |
| Binary | `sm` |

### Why we built our own rather than use `sk`

`sk` (the prior art, Appendix A) can't be installed cleanly here:
- Its internal `@skills-supply/*` workspace packages are **unpublished** → `npm i -g` fails
  **E404** on `@skills-supply/core`.
- Its `npmrc.sh` injects a private **FontAwesome** registry token → plain `npm install` fails
  **E401**.
- Its TOML grammar lives in a further external, un-checked-out package.

Beyond installability, `sk` targets five agents that are **not** the ones we use, includes a
hosted-account subsystem we don't want, and supports Claude plugins as a source — which our
no-drift philosophy explicitly rejects. A self-contained Go binary, scoped to our standard,
is cleaner than bending `sk` to fit.

## Core concepts

| Term | Meaning |
| --- | --- |
| **Skill** | A directory containing a `SKILL.md` (YAML frontmatter: `name` required, `description` optional; then markdown). The whole directory is the unit — bundled scripts/resources travel with it. |
| **Source** | A git repo (or local path) of one or more skills, listed in `skills.toml` under an explicit alias. |
| **Manifest** | `skills.toml` — the single source of truth: which sources to pull and which agents to install into. |
| **Agent** | An AI coding tool that reads skills from a known directory. |
| **Cache** | The durable local clone of each git source, kept fresh with `git pull`. |
| **Alias** | The manifest key naming a source; a stable human handle for the CLI/status/overrides — it does **not** alter skill names or placement. |

---

## Appendix A: reverse-engineered `sk` reference (prior art)

The following is a faithful functional breakdown of `sk` (from
[`803/skills-supply`](https://github.com/803/skills-supply), cloned at
`~/src/tools/skills-supply`) at the analyzed commit. It is background/reference — **not** a
description of `sm`. Where `sm` diverges, the [Decisions](#decisions-from-brainstorming-2026-08-21)
table and `docs/DESIGN.md` are authoritative.

### CLI surface
`sk` (commander-based). No args → help. Local scope by default; `--global` per command.
Commands: `init`, `pkg add <typeOrUrl> [spec]` / `pkg remove <alias>`, `agent add|remove
<name>`, `sync` (`--dry-run`, `--global`, `--non-interactive`), and hidden `auth` / `status`
/ `whoami` / `logout`. `pkg add` takes an explicit two-arg form (`sk pkg add gh owner/repo`)
or a one-arg auto-detect form. No `pkg list` — state lives only in `.sk-state.json`.

### Manifest (`agents.toml`)
- `[package]` (optional): name/version required if present; marks a "manifest-style package".
- `[agents]`: `<agentId> = <bool>`; ids limited to `amp, claude-code, codex, opencode,
  factory` (unknown id fails the parse).
- `[dependencies]`: `<alias> = <declaration>`; alias must not contain `/ \ . :`. Five forms:
  GitHub `{gh="owner/repo", tag?|branch?|rev?, path?}`; Git `{git="url", …}`; Registry
  `{registry, version}` or string `"name@version"`; Local `{path}`; Claude plugin
  `{type="claude-plugin", plugin, marketplace}`. Declaration schemas are strict; `tag`/
  `branch`/`rev` are mutually exclusive.
- `[exports.auto_discover] skills = "./skills" | false` (default `./skills`).

### Agent registry
Five agents, each with detect binary and per-scope dirs (`<root>/<basePath>/<skillsDir>`):
amp (`amp`, `./.agents/skills`, `~/.config/agents/skills`), claude-code (`claude`,
`./.claude/skills`, `~/.claude/skills`), codex (`codex`, `./.codex/skills`, `~/.codex/skills`),
factory (`droid`, `./.factory/skills`, `~/.factory/skills`), opencode (`opencode`,
`./.opencode/skill`, `~/.config/opencode/skill` — dir is singular `skill`). Detection is
`<bin> --version`, non-fatal.

### Package types & resolution
local → symlink; github/git → shallow `git clone --depth 1` (+ `--filter=blob:none --sparse`
when a `path` subdir is set), ref checkout with deepen-and-retry fallback, copied into agent;
claude-plugin → resolved from a marketplace (`.claude-plugin/marketplace.json`); registry →
parsed but **not implemented** (`sync` errors). URL auto-detection shallow-clones remote
sources into a temp dir, inspects, cleans up.

### Skill detection & extraction
Structure detection finds any of: `manifest` (`agents.toml` with `[package]`), `plugin`
(`.claude-plugin/plugin.json`, skills under `skills/`), `marketplace`, `subdir` (child dirs
each with a `SKILL.md`), `single` (root `SKILL.md`). Precedence: manifest → plugin → subdir →
single. Each skill's name/description come from `SKILL.md` frontmatter; the whole skill dir is
the install source. Every skill installs as **`<alias>-<name>`**; duplicate targets across
packages are rejected.

### Install & state
Target = `<agent skills dir>/<alias>-<name>`; local packages symlink, everything else copies.
A pre-existing target `sk` didn't previously manage **aborts** the sync (protects hand-placed
skills). State: `.sk-state.json` (version 1, `{version, skills:[...], updated_at}`) in each
agent's base dir. Sync reconciles: remove previously-managed targets no longer desired,
install desired, rewrite state; with no prior state it skips stale removal (never deletes
unrecorded files).

### Claude-plugin fork (the crux of cross-agent support)
For **claude-code**, `sk` shells out to the real `claude` CLI (`claude plugin marketplace
add`, `claude plugin install`) and installs no skills itself — Claude Code owns them. For
**every other agent**, `sk` resolves the plugin's source from the marketplace and
extracts/installs its skills like any other package.

### Scope & discovery
Global manifest `~/.sk/agents.toml` (installs into home dirs); local manifest found by
walking up from cwd (stopping at `$HOME` or `/`). Ancestor manifests prompt to reuse or
create locally.

### Auth (hosted, optional)
Device-code OAuth against `https://api.skills.supply` (override `SK_BASE_URL`); tokens stored
in git's credential helper keyed by the API host, reused as Bearer tokens. Purpose is a
personal marketplace + the (unimplemented) registry type — **not** git fetching.

### External dependencies
`git` (all remote fetches + credential storage); the `claude` CLI (claude-plugin deps on the
claude-code agent); agent detect binaries (`--version`); temp dirs cleaned in `finally`.
