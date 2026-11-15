# `sm map` (store-sourced) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `sm map` — project a profile-named subset of the store into any folder the caller names, as reconciled symlinks (or copies).

**Architecture:** A new `profile` package (parse the skills list) and `mapsel` package (match names against a `store.Listing`, fail loud on unknown/unusable) feed a new `runMap` command that reuses `runSync`'s reconcile primitives (`applyLinkTarget`/`applyCopyTarget`, keyed on a synthetic `agents.Agent{Name, Dir}` for the target). State gains a `Mapped` marker so `clean --orphans` and `status` treat a mapped folder as deliberate, not orphaned.

**Tech Stack:** Go; `github.com/BurntSushi/toml v1.6.0`; stdlib. Build/test via Makefile only.

**Spec:** `docs/superpowers/specs/2026-11-13-sm-store-select-design.md`

## Global Constraints

- Build and test ONLY via Makefile: `make build`, `make run-tests`. Never run `go build/test/vet/run/install` directly.
- TOML via `github.com/BurntSushi/toml v1.6.0` (already a direct dependency).
- Store path: `store.DefaultPath()` (`$XDG_DATA_HOME/skills`); a `--store DIR` override mirrors the other verbs.
- Named profiles live at `<XDG_CONFIG_HOME|~/.config>/skills-mapper/profiles/<name>.toml`.
- The target folder is taken literally (no `.agents/skills` append) and normalized to an absolute, clean, symlink-resolved path (`pathutil.Abs` then `pathutil.ResolveExisting`) before it is used as a state key — `state` rejects a non-absolute/unclean key.
- Fail loud on an unknown or Unusable named skill: report every bad name and place nothing.
- Only `sm`-recorded entries are ever removed; a pre-existing unmanaged entry is reported and skipped, never overwritten.
- The state lock (`state.Lock(statePath, stateLockHolder)`) is held across load→save outside `--dry-run`.
- Commit messages: present-tense verb, single line, end with a period; no Claude attribution.

## Review Focus

1. A flag after the target (`sm map --from f ./run --dry-run`) must still be honored — stdlib `flag` stops at the first positional; the hand-rolled parser must not. → Task 4 `TestMapFlagAfterTarget`.
2. A profile naming a skill absent from the store fails loud and places nothing. → Task 2 `TestSelectUnknownFailsClosed`, Task 4 `TestMapUnknownPlacesNothing`.
3. A named skill present in the store but non-conforming (in `Listing.Unusable`) fails loud, not silently skipped. → Task 2 `TestSelectUnusableNamedFails`.
4. Re-mapping with a smaller profile prunes only `sm`-made links and never touches a hand-placed file in the target. → Task 4 `TestMapReconcileShrinks`, `TestMapProtectsUnmanaged`.
5. `sm clean --orphans` leaves a mapped target alone. → Task 3 `TestCleanOrphansSkipsMappedTarget`.

---

### Task 1: `profile` package — parse and locate the skills list

**Files:**
- Create: `internal/profile/profile.go`
- Test: `internal/profile/profile_test.go`

**Interfaces:**
- Produces:
  - `func Parse(data []byte) (names []string, duplicates []string, err error)` — decode TOML `skills = [...]` with `toml.Unmarshal` (lenient, ignores unknown keys), dedupe preserving first-seen order, return sorted `duplicates`, and error when the list is absent or empty (`"profile lists no skills"`).
  - `func DefaultDir() (string, error)` — `<XDG_CONFIG_HOME|~/.config>/skills-mapper/profiles`.
  - `func NamePath(dir, name string) string` — `filepath.Join(dir, name+".toml")`.

- [ ] **Step 1: Write failing tests** `TestParseReturnsNamesInOrder`, `TestParseDedupesKeepingFirst` (asserts `duplicates` sorted), `TestParseEmptyListIsError`, `TestParseIgnoresUnknownKeys`, `TestNamePathAppendsToml`, `TestDefaultDirHonorsXDG` (set `XDG_CONFIG_HOME`, assert suffix `skills-mapper/profiles`).
- [ ] **Step 2: Run tests, verify they fail** — Run: `make run-tests`. Expected: FAIL (package/functions undefined).
- [ ] **Step 3: Implement the three functions** in `internal/profile/profile.go`. A `struct{ Skills []string }` with `toml:"skills"` is the decode target; mirror the sources-branch version at `git show bd5cb0b:internal/profile/profile.go` with the import path unchanged (it uses only stdlib + toml).
- [ ] **Step 4: Run tests, verify pass** — Run: `make run-tests`. Expected: PASS.
- [ ] **Step 5: Commit** — `git add internal/profile && git commit -m "feat(profile): parse and locate store skill-subset profiles."`

---

### Task 2: `mapsel` package — select store skills by name

**Files:**
- Create: `internal/mapsel/mapsel.go`
- Test: `internal/mapsel/mapsel_test.go`

**Interfaces:**
- Consumes: `store.Listing` (`Skills []store.Skill{Name,Dir,Identity,ManifestIdentity}`, `Unusable []string`).
- Produces:
  - `type SelectError struct { Unknown []string; Unusable []string }` with `func (e *SelectError) Error() string` listing both sets; names unique in the store, so there is no ambiguity case.
  - `func Select(listing store.Listing, names []string) ([]store.Skill, error)` — for each name: present in `Skills` → include that `store.Skill`; present in `Unusable` → record Unusable; in neither → record Unknown. If any Unknown or Unusable, return `nil, &SelectError{...}` (both lists sorted). Otherwise return the selected skills sorted by `Name`.

- [ ] **Step 1: Write failing tests** `TestSelectReturnsSkillsSortedByName`, `TestSelectUnknownFailsClosed` (one unknown → `*SelectError` with it in `Unknown`, nil skills), `TestSelectUnusableNamedFails` (name in `Listing.Unusable` → `*SelectError.Unusable`), `TestSelectCollectsAllBadNames` (two unknown + one unusable all reported). Build `store.Listing` literals directly in the test.
- [ ] **Step 2: Run tests, verify they fail** — Run: `make run-tests`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement `SelectError` and `Select`** in `internal/mapsel/mapsel.go`. Index `Skills` by `Name` and `Unusable` into a set; one pass over `names`.
- [ ] **Step 4: Run tests, verify pass** — Run: `make run-tests`. Expected: PASS.
- [ ] **Step 5: Commit** — `git add internal/mapsel && git commit -m "feat(mapsel): select store skills by name, failing loud on unknown or unusable."`

---

### Task 3: `Mapped` state marker + `clean --orphans`/`status` exemption

**Files:**
- Modify: `internal/state/state.go` (the `Target` struct, ~line 50)
- Modify: `internal/cli/clean.go` (the `unmappedKind` consts ~line 130, `classifyUnmapped` ~line 203, `cleanOrphans` switch ~line 357)
- Modify: `internal/cli/status.go` (the kind switch ~line 169)
- Test: `internal/cli/clean_test.go` (add case)

**Interfaces:**
- Produces: `state.Target` gains `Mapped bool` with tag `json:"mapped,omitempty"` (so sync/copy-only state stays byte-identical). A new `mappedTarget unmappedKind` const.
- Consumes: `classifyUnmapped(cfg, current)` already iterates state targets; it gains a branch, before the `orphanTarget` fallthrough, that classifies `current.Targets[dir].Mapped` as `mappedTarget`.

- [ ] **Step 1: Write the failing test** `TestCleanOrphansSkipsMappedTarget` in `clean_test.go`: seed state with one target dir (not in `agents.toml`) whose `Target{Links: {"a": storePath}, Mapped: true}`; run `sm clean --orphans`; assert exit 0, the recorded link is **still present** in saved state, and output reports it left alone (not `"<dir> -N"`).
- [ ] **Step 2: Run test, verify it fails** — Run: `make run-tests`. Expected: FAIL (a mapped target is currently swept as `orphanTarget`).
- [ ] **Step 3: Implement.** Add `Mapped` to `state.Target`. Add `mappedTarget` to the `unmappedKind` consts. In `classifyUnmapped`, before appending `orphanTarget`, `if current.Targets[dir].Mapped { append ...kind: mappedTarget; continue }`. In `cleanOrphans`, add `case mappedTarget:` that prints a "left alone (mapped target)" line and makes no change. In `status.go`'s switch, add `case mappedTarget:` reporting it as a mapped projection. (Both switches have a `default` that errors on an unknown kind, so both must handle it.)
- [ ] **Step 4: Run tests, verify pass** — Run: `make run-tests`. Expected: PASS (new test green; existing clean/status tests unaffected because `Mapped` defaults false).
- [ ] **Step 5: Commit** — `git add internal/state/state.go internal/cli/clean.go internal/cli/status.go internal/cli/clean_test.go && git commit -m "feat(state): mark map targets and exempt them from clean --orphans."`

---

### Task 4: `sm map` command — wire profile + mapsel + store + reconcile

**Files:**
- Create: `internal/cli/map.go`
- Modify: `internal/cli/cli.go` (register `"map"` in the `commands` map)
- Modify: `README.md` and `skill/using-skills-mapper/SKILL.md` (document `sm map`)
- Test: `internal/cli/map_test.go`

**Interfaces:**
- Consumes: `profile.Parse`/`DefaultDir`/`NamePath` (Task 1); `mapsel.Select` (Task 2); `state.Target.Mapped` (Task 3); existing `store.Enumerate`, `store.DefaultPath`, `store.RequireNotWorldWritable`; `pathutil.Abs`, `pathutil.ResolveExisting`; `state.Lock`, `state.Load`, `State.Save`; and `runSync`'s primitives `applyLinkTarget(current, agent, desired, unusable, statePath, opts, log)` / `applyCopyTarget(..., pins, ...)` with `agents.Agent{Name, Dir}` and `syncOptions{CopyMode, DryRun, Verbose}`.
- Produces: `func runMap(ctx commandContext) int`; a `commands["map"]` registry entry with summary `"link a profile's skills from the store into a target folder"` and flagSummary `"[--profile NAME | --from PATH] [--copy] [--dry-run] [--store DIR] <target-dir> [skill ...]"`.

**Approach:** `runMap` hand-rolls argument parsing (the shared `parseFlags` rejects positionals and stdlib `flag` stops at the first). A small `parseMapArgs(args) (sel selection, target string, copyMode, dryRun bool, storeOverride string, err error)` scans tokens: `--profile`/`--from`/`--store` consume the next token, `--copy`/`--dry-run` are booleans, `-v` already stripped upstream; the first non-flag token is the target, the rest are ad-hoc skill names. Resolve the selection to a name list: `--from PATH` → read+`profile.Parse`; `--profile NAME` → `profile.Parse(read(NamePath(DefaultDir(), NAME)))`; otherwise the ad-hoc names. `--profile` and `--from` together, or no selection at all, or no target, are usage errors (exit 2). Then: resolve the store (`--store` or `DefaultPath`), `Enumerate` + `RequireNotWorldWritable`, `mapsel.Select` (fail loud → exit non-zero, nothing placed), build from the selected skills both `desired{name→skill.Dir}` and, for copy mode, `pins{name→link.StorePin{Dir: skill.Identity, Manifest: skill.ManifestIdentity}}`, plus a `unusable` set from `listing.Unusable` (so a previously-mapped entry gone non-conforming is withheld, not pruned). Normalize the target (`Abs`→`ResolveExisting`), construct `agents.Agent{Name: targetDisplay, Dir: resolvedTarget}`, take the lock unless dry-run, `state.Load`, set `Targets[dir].Mapped = true`, call `applyLinkTarget(current, agent, desired, unusable, statePath, opts, log)` (or `applyCopyTarget(..., pins, ...)`), `Save` unless dry-run, and print `map: +C -R` (symlink) / `map: +C ~F -R` (copy) from the returned `syncCounts`.

- [ ] **Step 1: Write failing integration tests** in `map_test.go`, each using a temp `HOME`/`XDG_CONFIG_HOME`/`XDG_DATA_HOME`, a store dir with two conforming skills (`rebuild`, `lint`) and one non-conforming entry (`broken`, a dir with no `SKILL.md`), driven via `Run([]string{"map", ...}, &out, &err)`:
  - `TestMapFromPlacesSubset` — `--from` profile `["rebuild"]` into `./run`: `./run/rebuild` is a symlink into the store, `./run/lint` absent, stdout `map: +1 -0`.
  - `TestMapAdhocPlaces` — `map ./run rebuild` places `rebuild`.
  - `TestMapNamedProfile` — a profile file at `profiles/dev.toml`, `--profile dev`, places its skills.
  - `TestMapFlagAfterTarget` — `map --from f ./run --dry-run` exits 0, prints `map: +1 -0`, places nothing (dry-run honored though it follows the target).
  - `TestMapUnknownPlacesNothing` — profile `["ghost"]` → non-zero exit, `./run/ghost` absent, message names `ghost`.
  - `TestMapUsableButBrokenNamedFails` — profile `["broken"]` → non-zero exit, nothing placed.
  - `TestMapReconcileShrinks` — map `[rebuild,lint]` then `[rebuild]`: `lint` link removed, `rebuild` kept, second run `-1`.
  - `TestMapProtectsUnmanaged` — pre-create a real dir `./run/rebuild`; map `[rebuild]` → it is not replaced by a symlink and a "not managed by sm" warning is emitted.
  - `TestMapLiteralTarget` — `map ./run rebuild` writes `./run/rebuild`, not `./run/.agents/skills/rebuild`.
  - `TestMapExclusiveSources` — `--from f --profile p` → exit 2.
  - `TestMapRequiresSelection` — `map ./run` with no names/profile → exit 2.
  - `TestMapMarksTargetMapped` — after a map, saved state's target has `Mapped: true`.
  - `TestMapCopyMode` — `--copy` materializes a real directory, not a symlink.
- [ ] **Step 2: Run tests, verify they fail** — Run: `make run-tests`. Expected: FAIL (`unknown command "map"`).
- [ ] **Step 3: Implement `parseMapArgs` and `runMap`** in `internal/cli/map.go`; register `"map"` in `cli.go`'s `commands` map. Reuse the sources-branch selection/parsing shape at `git show bd5cb0b:internal/cli/cli.go` (the `parsePositionals`/`cmdMap` functions) adapted to the store flags and to `store.Enumerate`/`applyLinkTarget`.
- [ ] **Step 4: Run tests, verify pass** — Run: `make run-tests`. Expected: PASS (whole suite green).
- [ ] **Step 5: Document** `sm map` in `README.md` (command list + a short "map a subset into a folder" example block) and in `skill/using-skills-mapper/SKILL.md` (a "Mapping a subset for a one-shot run" section, store-sourced).
- [ ] **Step 6: Build and commit** — `make build` (verify `./bin/sm` builds), then `git add internal/cli/map.go internal/cli/cli.go internal/cli/map_test.go README.md skill/using-skills-mapper/SKILL.md && git commit -m "feat(cli): add sm map to project a profile subset from the store into a folder."`
