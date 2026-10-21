> Historical document. Superseded by [`docs/DESIGN.md`](../DESIGN.md) and the newer plans [`2026-10-20-ms-ingest.md`](2026-10-20-ms-ingest.md) and [`2026-10-20-review-fixes.md`](2026-10-20-review-fixes.md); kept for context. Copy mode (`sm sync --copy`) and the `ms` ingest tool came later.

# skills-mapper store→agents fan-out — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild `sm` as a pure symlink fan-out tool that mirrors the canonical skills store into each configured agent's skills directory and keeps it reconciled.

**Architecture:** A canonical, agent-neutral store (`$XDG_DATA_HOME/skills/`) is the source of truth; `sm` enumerates it, symlinks each skill into every agent listed in `agents.toml`, and reconciles links it owns. Small single-purpose packages: `store` (enumerate), `agents` (config + registry), `state` (reconcile planner), `link` (symlink I/O), `probe` (agent detection), `cli` (command wiring). Pure cores (`store`, `state`) hold the correctness and are unit-tested in isolation.

**Tech Stack:** Go 1.26.2, stdlib `flag` for the CLI, `github.com/BurntSushi/toml` for `agents.toml`, `gopkg.in/yaml.v3` for `SKILL.md` frontmatter. No new dependencies.

**Spec:** [`../../SKILLS-DIRECTORY-SPEC.md`](../../SKILLS-DIRECTORY-SPEC.md) (the store standard) and [`../DESIGN.md`](../DESIGN.md) (the `sm` design). [`../../VISION.md`](../../VISION.md) is the why.

## Global Constraints

- Module `github.com/gherlein/sm`, Go 1.26.2.
- **Build and test ONLY through the Makefile** — `make build`, `make test`. Never run `go build`/`go test`/`go vet` directly (a hook enforces this).
- Store path: `$XDG_DATA_HOME/skills`, default `~/.local/share/skills` when `XDG_DATA_HOME` is unset, empty, or relative.
- Config path: `$XDG_CONFIG_HOME/skills-mapper/agents.toml`, default `~/.config/skills-mapper/agents.toml`.
- State path: `$XDG_DATA_HOME/skills-mapper/state.json`, default `~/.local/share/skills-mapper/state.json`.
- Skill name regex: `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`. A skill's directory name is authoritative and MUST equal its `SKILL.md` frontmatter `name`.
- Symlink fan-out only; Linux and macOS. v1 is user-global scope only (no project scope).
- `sm` only ever touches links it created (tracked in state); it never overwrites or deletes an unmanaged entry, and it removes only symlinks, never real directories.
- Idiomatic Go: explicit error handling, meaningful names, named constants (no magic numbers), small composable interfaces. No forgiving/defensive code — assert preconditions and return errors. Comments explain WHY. No emoji anywhere.

## Review Focus

- **Store directory missing** → `sm sync` warns and makes NO changes; it must not mass-remove links because the store path is absent. (Task 6)
- **Unmanaged entry at a wanted name** (a hand-placed skill or an agent's own file) → skip and warn; never overwrite. (Task 3 planner, exercised in Task 6)
- **Non-conforming store entry** (missing `SKILL.md`, name/`name` mismatch, illegal charset) → warn and skip; never fatal. (Task 1)
- **Stale/dangling `sm` link** (its store skill was removed) → reconcile removes only our symlink, never a real directory left at that name. (Task 3 planner, Task 4 remove)
- **`agents.toml` value with `~`/env var, or not absolute** → expand it; a value that is not absolute after expansion is a loud validation error naming the agent. (Task 2)

---

### Task 1: Store enumeration (`internal/store`)

**Files:**
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Skill struct { Name string; Dir string }` — `Name` is the directory name (authoritative); `Dir` is the absolute path to the skill directory.
  - `func DefaultPath() (string, error)` — `$XDG_DATA_HOME/skills` with the documented default.
  - `func Enumerate(storeDir string) (skills []Skill, warnings []string, err error)` — conforming immediate subdirectories, with warnings for non-conforming subdirectories. Returns a non-nil `err` only when `storeDir` cannot be read (e.g. does not exist).

- [ ] **Step 1: Write the failing tests** in `internal/store/store_test.go` (table-driven, building fixture directories under `t.TempDir()`):

```
TestEnumerate_ConformingSkills:   store with "git-ops" and "clean-comments", each a dir with SKILL.md whose frontmatter name matches -> both returned, sorted by Name, no warnings.
TestEnumerate_MissingSkillMd:     dir "broken" with no SKILL.md -> not returned; one warning mentioning "broken".
TestEnumerate_NameMismatch:       dir "foo" whose SKILL.md name is "bar" -> not returned; warning mentions the mismatch.
TestEnumerate_IllegalName:        dir "Foo_Bar" -> not returned; warning mentions the name.
TestEnumerate_IgnoresRootFiles:   a regular file "README" in the store root -> ignored silently (no warning, not returned).
TestEnumerate_NotNested:          "parent/child/SKILL.md" where parent has no SKILL.md -> parent is non-conforming (warning), child is NOT discovered.
TestEnumerate_MissingStore:       storeDir that does not exist -> err != nil, skills nil.
TestDefaultPath_XDGDefault:       XDG_DATA_HOME unset -> ends with "/.local/share/skills".
TestDefaultPath_XDGSet:           XDG_DATA_HOME="/data" -> "/data/skills".
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `make test`
Expected: FAIL — `internal/store` undefined.

- [ ] **Step 3: Implement `internal/store/store.go`**

Define `const namePattern = "^[a-z0-9]([a-z0-9-]*[a-z0-9])?$"` (compiled once). `DefaultPath` reads `XDG_DATA_HOME`; when unset/empty/relative, use `os.UserHomeDir()` + `.local/share`; join `skills`. `Enumerate` reads `storeDir` with `os.ReadDir`; for each entry: skip non-directories silently; validate name against the pattern; require `SKILL.md`; parse its frontmatter name and require equality with the directory name; collect conforming ones into `[]Skill` sorted by `Name`, everything else into `warnings`. Frontmatter parsing: a private helper reads the file, extracts the YAML block between the first two `---` delimiter lines, and `yaml.Unmarshal`s into `struct{ Name string }`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `make test`
Expected: PASS for `internal/store`.

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "feat(store): enumerate conforming skills in the canonical store"
```

---

### Task 2: Agents config and registry (`internal/agents`)

**Files:**
- Create: `internal/agents/agents.go`
- Test: `internal/agents/agents_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Agent struct { Name string; Dir string }` — `Dir` is an absolute, expanded skills directory.
  - `type Config struct { Agents []Agent }` — enabled agents, sorted by `Name`.
  - `type Known struct { Name string; DefaultDir string; ConfigDir string; Binary string }` — well-known agent for suggestions (`DefaultDir`/`ConfigDir` may contain `~`).
  - `func Registry() []Known` — claude-code, copilot, hax, pi, oh-my-pi, kit (dirs per DESIGN's registry table).
  - `func DefaultConfigPath() (string, error)` — `$XDG_CONFIG_HOME/skills-mapper/agents.toml`.
  - `func Expand(path string) (string, error)` — resolve leading `~` and environment variables to an absolute path.
  - `func Load(path string) (Config, error)` — parse `[agents]` (name = dir), expand and validate each dir is absolute. A missing file is an error.
  - `func WriteStarter(path string) error` — write a starter `agents.toml`; error if the file already exists.

- [ ] **Step 1: Write the failing tests** in `internal/agents/agents_test.go`:

```
TestExpand_Tilde:            "~/x" -> filepath.Join(home, "x").
TestExpand_EnvVar:           "$XDG_DATA_HOME/x" with env set -> "<val>/x".
TestExpand_RelativeIsError:  "rel/path" (no ~, no abs) -> err.
TestLoad_Valid:              file with claude-code and copilot -> Config.Agents sorted, dirs expanded absolute.
TestLoad_CommentsIgnored:    file with a "# ..." line -> parses fine.
TestLoad_NonAbsoluteValue:   agent dir "rel" -> err naming the agent.
TestLoad_Missing:            nonexistent path -> err.
TestWriteStarter_CreatesThenRefuses: first call writes a file containing "[agents]"; second call -> err (exists).
TestRegistry_HasKnownAgents: Registry() includes name "claude-code" with DefaultDir "~/.claude/skills".
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `make test`
Expected: FAIL — `internal/agents` undefined.

- [ ] **Step 3: Implement `internal/agents/agents.go`**

Decode with BurntSushi/toml into `struct{ Agents map[string]string `toml:"agents"` }`. `Expand`: handle a leading `~` via `os.UserHomeDir`, then `os.ExpandEnv`, then require `filepath.IsAbs`. `Load`: decode, expand each value, error on any non-absolute, return `Config` with agents sorted by name. `WriteStarter`: refuse if the path exists (`os.O_EXCL`); write `[agents]` plus one commented example line per `Registry()` entry (`# claude-code = "~/.claude/skills"`). `DefaultConfigPath`: `XDG_CONFIG_HOME` or `~/.config`, then `skills-mapper/agents.toml`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `make test`
Expected: PASS for `internal/agents`.

- [ ] **Step 5: Commit**

```bash
git add internal/agents
git commit -m "feat(agents): agents.toml loader, path expansion, and known-agent registry"
```

---

### Task 3: State and reconcile planner (`internal/state`)

**Files:**
- Create: `internal/state/state.go`
- Test: `internal/state/state_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Target struct { Links map[string]string }` — link name → store skill path `sm` created at this target.
  - `type State struct { Version int; Targets map[string]Target; UpdatedAt time.Time }` — `Targets` keyed by absolute agent directory.
  - `func Load(path string) (State, error)` — a missing file returns an empty, usable `State` and nil error.
  - `func (s State) Save(path string) error` — create parent dirs; write JSON.
  - `type TargetPlan struct { Create map[string]string; Remove []string; Unmanaged []string }`.
  - `func PlanTarget(desired, recorded map[string]string, inspect func(name string) (exists, isSymlink bool)) TargetPlan` — pure reconcile planner for one target.

- [ ] **Step 1: Write the failing tests** in `internal/state/state_test.go`:

```
TestPlanTarget_CreateNew:        desired has "a"; recorded empty; inspect("a")->(false,_) -> Create{"a"}.
TestPlanTarget_KeepRecorded:     desired "a"; recorded "a"; inspect("a")->(true,true) -> no Create, no Remove.
TestPlanTarget_RemoveStale:      desired empty; recorded "a"; inspect("a")->(true,true) -> Remove ["a"].
TestPlanTarget_UnmanagedSkipped: desired "a"; recorded empty; inspect("a")->(true,false) -> Unmanaged ["a"], no Create.
TestPlanTarget_StaleAlreadyGone: desired empty; recorded "a"; inspect("a")->(false,_) -> Remove ["a"] (tolerated as no-op later).
TestPlanTarget_UnmanagedSymlinkNotOurs: desired "a"; recorded empty; inspect("a")->(true,true) -> Unmanaged ["a"] (present, we have no record).
TestSaveLoad_RoundTrip:          Save then Load returns equal Targets.
TestLoad_Missing:                nonexistent path -> empty State, nil err.
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `make test`
Expected: FAIL — `internal/state` undefined.

- [ ] **Step 3: Implement `internal/state/state.go`**

`PlanTarget`: for each `desired` name — if it does not exist, add to `Create`; if it exists and we recorded it, keep (no action); if it exists and we did not record it, add to `Unmanaged`. For each `recorded` name absent from `desired`, add to `Remove`. `Remove` and `Unmanaged` are sorted for deterministic output. `Load`/`Save` use `encoding/json`; `Save` creates parent dirs and sets `UpdatedAt`/`Version`. Define `const stateVersion = 1`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `make test`
Expected: PASS for `internal/state`.

- [ ] **Step 5: Commit**

```bash
git add internal/state
git commit -m "feat(state): central state file and pure reconcile planner"
```

---

### Task 4: Symlink I/O (`internal/link`)

**Files:**
- Create: `internal/link/link.go`
- Test: `internal/link/link_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `func Inspect(targetDir, name string) (exists, isSymlink bool, err error)` — suitable as the `inspect` argument to `state.PlanTarget` (via a closure).
  - `func Create(targetDir, name, storePath string) error` — ensure `targetDir` exists, then symlink `targetDir/name` → `storePath`.
  - `func Remove(targetDir, name string) error` — delete `targetDir/name` only if it is a symlink; a no-op if absent; an error if it is a real file/directory.

- [ ] **Step 1: Write the failing tests** in `internal/link/link_test.go` (use `t.TempDir()`):

```
TestCreate_MakesSymlink:        Create links name->storePath; os.Lstat shows a symlink; Readlink == storePath.
TestCreate_MakesTargetDir:      targetDir does not pre-exist -> Create creates it and the link.
TestInspect_Symlink:            after Create -> Inspect returns (true,true).
TestInspect_RealDir:            a real subdirectory -> Inspect returns (true,false).
TestInspect_Absent:             missing name -> (false,false), nil err.
TestRemove_Symlink:             Create then Remove -> name gone.
TestRemove_RealDirErrors:       a real directory at name -> Remove returns err and leaves it intact.
TestRemove_AbsentNoop:          missing name -> Remove returns nil.
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `make test`
Expected: FAIL — `internal/link` undefined.

- [ ] **Step 3: Implement `internal/link/link.go`**

`Inspect` uses `os.Lstat` and `fi.Mode()&os.ModeSymlink`. `Create` uses `os.MkdirAll(targetDir, 0o755)` then `os.Symlink`; name is validated as a single clean path segment (no `/`, `\`, `.`, `..`). `Remove` `os.Lstat`s first: absent → nil; symlink → `os.Remove`; otherwise an error that names the path and says it is not an `sm`-created link. Define the directory mode as a named constant.

- [ ] **Step 4: Run tests to verify they pass**

Run: `make test`
Expected: PASS for `internal/link`.

- [ ] **Step 5: Commit**

```bash
git add internal/link
git commit -m "feat(link): symlink create, inspect, and symlink-only remove"
```

---

### Task 5: Agent probe (`internal/probe`)

**Files:**
- Create: `internal/probe/probe.go`
- Test: `internal/probe/probe_test.go`

**Interfaces:**
- Consumes: `agents.Known`, `agents.Agent`, `agents.Expand`.
- Produces:
  - `type Found struct { Name string; Dir string }` — a detected agent and its expanded default skills dir.
  - `func Detect(registry []agents.Known, home string, onPath func(bin string) bool) ([]Found, error)` — a known agent is found when its `ConfigDir` exists under `home` OR `onPath(Binary)` is true.
  - `func AddMissing(cfgPath string, found []Found, existing []agents.Agent, confirm func(Found) bool) (added []string, err error)` — append confirmed, not-yet-listed agents to `agents.toml`.

- [ ] **Step 1: Write the failing tests** in `internal/probe/probe_test.go`:

```
TestDetect_ByConfigDir:      fake home containing ".claude" -> Found includes "claude-code" with expanded Dir.
TestDetect_ByBinary:         onPath returns true only for "copilot" -> Found includes "copilot".
TestDetect_NoneWhenAbsent:   empty home, onPath always false -> no Found.
TestAddMissing_AppendsConfirmed: confirm returns true -> file gains `claude-code = "<dir>"`; added==["claude-code"].
TestAddMissing_SkipsDeclined:    confirm returns false -> file unchanged; added empty.
TestAddMissing_SkipsExisting:    found already in `existing` -> not re-added.
TestAddMissing_CreatesFileWithHeader: no file yet -> AddMissing writes "[agents]" then the entry.
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `make test`
Expected: FAIL — `internal/probe` undefined.

- [ ] **Step 3: Implement `internal/probe/probe.go`**

`Detect` iterates the registry; `ConfigDir` is expanded against `home` and checked with `os.Stat`; falls back to `onPath(Binary)`. `AddMissing` builds a set of `existing` names, filters `found` to the not-yet-listed, calls `confirm` per candidate, and for each accepted one appends a `name = "dir"` line to `cfgPath` (creating the file with an `[agents]` header first if absent). Appending bare key/value lines is valid because `[agents]` is the only table in the file.

- [ ] **Step 4: Run tests to verify they pass**

Run: `make test`
Expected: PASS for `internal/probe`.

- [ ] **Step 5: Commit**

```bash
git add internal/probe
git commit -m "feat(probe): detect installed agents and append them to agents.toml"
```

---

### Task 6: CLI core — `sync` and `init`, plus entrypoint (`internal/cli`, `cmd/sm`)

**Files:**
- Create: `internal/cli/cli.go`, `internal/cli/sync.go`, `cmd/sm/main.go`
- Test: `internal/cli/sync_test.go`

**Interfaces:**
- Consumes: `store.Enumerate`, `store.DefaultPath`, `agents.Load`, `agents.DefaultConfigPath`, `agents.WriteStarter`, `state.*`, `link.*`.
- Produces:
  - `func Run(args []string, stdout, stderr io.Writer) int` — parse the subcommand and global `-v`/`--verbose`; dispatch. Unknown/absent subcommand prints usage.
  - `func doSync(cfg agents.Config, storeDir, statePath string, dryRun, verbose bool, log io.Writer) (summary string, err error)` — the testable orchestration: enumerate the store, plan per agent via `state.PlanTarget` (with a `link.Inspect` closure), apply creates/removes through `link`, update and save state (unless `dryRun`), return a per-agent `+N -M` summary.

- [ ] **Step 1: Write the failing tests** in `internal/cli/sync_test.go` (fake store + fake agent dirs under `t.TempDir()`):

```
TestDoSync_LinksAllSkills:       store has 2 skills, 1 agent -> 2 symlinks created; summary "+2 -0"; state records them.
TestDoSync_ReconcilesRemoved:    second run after a skill is deleted from the store -> its link removed; "+0 -1".
TestDoSync_SkipsUnmanaged:       a real dir pre-placed at a skill's name in the agent dir -> not overwritten; warning emitted; state does not record it.
TestDoSync_MissingStoreNoChanges: storeDir absent -> no links removed, returns a clear error/warning; a previously-linked agent dir is untouched.
TestDoSync_DryRunTouchesNothing: dryRun true -> no symlinks created, state file not written, summary still computed.
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `make test`
Expected: FAIL — `internal/cli` undefined.

- [ ] **Step 3: Implement `doSync` and the `sync`/`init` commands, and `cmd/sm/main.go`**

`doSync`: call `store.Enumerate`; on a missing-store error, log a warning and return without touching any agent (Review Focus item 1). Build `desired` (skill name → skill `Dir`) once. For each agent: `recorded := state.Targets[agent.Dir].Links`; `plan := state.PlanTarget(desired, recorded, func(n string)(bool,bool){ e,s,_ := link.Inspect(agent.Dir,n); return e,s })`; for `Unmanaged` names log a warning and skip; unless `dryRun`, `link.Create` each `plan.Create` and `link.Remove` each `plan.Remove`, then set the new recorded map; accumulate `+len(Create) -len(Remove)`. Save state unless `dryRun`. `cmd/sm/main.go` is `os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))`. `init` calls `agents.WriteStarter(agents.DefaultConfigPath())`.

- [ ] **Step 4: Run tests and build to verify**

Run: `make test` then `make build`
Expected: tests PASS; `make build` produces `bin/sm`.

- [ ] **Step 5: Commit**

```bash
git add internal/cli cmd/sm
git commit -m "feat(cli): sync fan-out with reconcile, init, and the sm entrypoint"
```

---

### Task 7: CLI `status` and `clean` (`internal/cli`)

**Files:**
- Create: `internal/cli/status.go`, `internal/cli/clean.go`
- Modify: `internal/cli/cli.go` (register the two subcommands)
- Test: `internal/cli/status_test.go`, `internal/cli/clean_test.go`

**Interfaces:**
- Consumes: `store.Enumerate`, `agents.Load`, `state.*`, `link.*`.
- Produces:
  - `func doStatus(cfg agents.Config, storeDir, statePath string, out io.Writer) error` — read-only report: store skill count, and per agent the counts of `sm`-owned links, missing (in store, not linked), unmanaged (present, not ours), and dangling (our link whose store skill is gone).
  - `func doClean(cfg agents.Config, statePath string, only string, out io.Writer) (summary string, err error)` — remove all `sm`-owned links (optionally just for agent `only`), clear their state, leave the store and unmanaged entries intact.

- [ ] **Step 1: Write the failing tests**:

```
TestDoStatus_ReportsCounts:     2 store skills, 1 linked + 1 missing + 1 unmanaged -> report contains those counts; store and agent dirs unchanged.
TestDoStatus_ReportsDangling:   state records a link whose store skill no longer exists -> dangling count 1.
TestDoClean_RemovesOwnedLinks:  two sm links -> both removed; state cleared; "-2".
TestDoClean_LeavesUnmanaged:    an unmanaged real dir at a name -> left intact by clean.
TestDoClean_OnlyAgent:          only="copilot" with two agents -> only copilot's links removed.
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `make test`
Expected: FAIL — undefined `doStatus`/`doClean`.

- [ ] **Step 3: Implement `doStatus` and `doClean`** and register `status` and `clean` in `Run`.

`doStatus` enumerates the store into a name set, reads state per agent, and `link.Inspect`s each name to classify it; writes a plain aligned report; changes nothing. `doClean` iterates recorded links for the in-scope targets, `link.Remove`s each, removes those targets from state, and saves.

- [ ] **Step 4: Run tests to verify they pass**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "feat(cli): status report and clean (remove sm-owned links)"
```

---

### Task 8: CLI `probe` command and usage (`internal/cli`)

**Files:**
- Create: `internal/cli/probe.go`
- Modify: `internal/cli/cli.go` (register `probe`; finalize top-level usage text)
- Test: `internal/cli/probe_test.go`

**Interfaces:**
- Consumes: `probe.Detect`, `probe.AddMissing`, `agents.Registry`, `agents.Load`, `agents.DefaultConfigPath`.
- Produces:
  - `func doProbe(cfgPath string, home string, onPath func(string) bool, confirm func(probe.Found) bool, out io.Writer) (added []string, err error)` — detect agents and add confirmed ones; the command supplies a stdin-reading `confirm`, or an always-true `confirm` under `--yes`, or a no-write path under `--dry-run`.

- [ ] **Step 1: Write the failing tests** in `internal/cli/probe_test.go`:

```
TestDoProbe_AddsConfirmed:   fake home with ".claude"; confirm always true -> agents.toml gains claude-code; added non-empty.
TestDoProbe_DryRunNoWrite:   same, but dry-run path uses a confirm that never writes -> file unchanged (assert by passing a confirm stub and empty existing config).
TestRun_UnknownCommand:      Run(["bogus"],...) returns non-zero and prints usage to stderr.
TestRun_NoArgsPrintsUsage:   Run([],...) returns non-zero and prints usage.
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `make test`
Expected: FAIL — undefined `doProbe` / usage behavior.

- [ ] **Step 3: Implement `doProbe`** and wire the `probe` subcommand (flags `--yes`, `--dry-run`) plus the final top-level usage listing all commands (`sync`, `status`, `probe`, `clean`, `init`). The interactive `confirm` reads `y/N` from stdin.

- [ ] **Step 4: Run tests and build to verify**

Run: `make test` then `make build`
Expected: tests PASS; `make build` produces `bin/sm`.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "feat(cli): probe command to populate agents.toml from installed agents"
```

---

### Task 9: Rewrite README for the new model

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: the shipped CLI and the two specs.
- Produces: user-facing docs matching the built tool.

- [ ] **Step 1: Rewrite `README.md`** to describe the store→agents model: the canonical store, `agents.toml`, and the `sync`/`probe`/`status`/`clean`/`init` commands. Remove all `skills.toml`/sources/cache content. Link to `SKILLS-DIRECTORY-SPEC.md` and `docs/DESIGN.md`. Keep the build/targets sections (they are current).

- [ ] **Step 2: Verify the tool matches the docs**

Run: `make build` then `./bin/sm` (prints usage) and `./bin/sm init` against a temp `XDG_CONFIG_HOME`.
Expected: usage lists the documented commands; `init` writes a starter `agents.toml`.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs(readme): describe the store->agents fan-out model"
```

---

## Notes for the executor

- Run `make deps` once if the build complains about `go.sum` (the TOML/YAML deps are already required; `go mod tidy` will drop anything unused from the old code).
- Every task is independently testable via `make test`; Tasks 6 and 8 also build the binary via `make build`.
- Do not reintroduce `skills.toml`, a clone cache, or cross-source resolution — they are removed by design.
