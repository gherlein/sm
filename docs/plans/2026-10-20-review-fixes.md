> **Status: completed (2026-10-20).** Every task in this plan is implemented and
> was re-verified. A final round of five verification reviews then found further
> issues, fixed in a last wave after this plan: empty flag values and stray
> arguments as usage errors, `ms` sharing `sm`'s state lock, the empty-store guard
> rule, state key and `targets` strictness, per-skill failure isolation, source
> identity pinning, deletion through handles, the `SKILL.md` symlink policy (`sm
> --copy` dereferences, `ms` refuses), copy ownership checks and terminal-safe
> messages. Where this plan and [`docs/DESIGN.md`](../DESIGN.md) or the README
> differ, those are current; this plan is kept as the record of the work.

# Review Fixes — Hardening `sm` and `ms` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix every finding from the 2026-10-20 four-lens review (goals, correctness, security, documentation) of `sm` and `ms`, then independently re-verify each fix.

**Architecture:** The tools already have the right shape (store -> agents fan-out; marker/ledger provenance; `ms` ingest). The defects are in how they treat the filesystem: they trust path strings, predictable names, and "is it a symlink" instead of identity. The fixes (1) make every write symlink-safe and race-safe (`os.Root`, random staging names, ownership checks), (2) make every delete identity-checked (recorded link target / ledger-matched marker), (3) make replacement a swap that cannot lose the old data, (4) add locking and state-version safety, (5) harden discovery and config parsing, then (6) bring the documentation in line with the code.

**Tech Stack:** Go 1.26.2 (`os.Root` incl. `MkdirAll`/`Symlink`/`OpenFile`/`WriteFile`/`Chmod`/`Rename`; `syscall.Flock` behind `//go:build unix`), stdlib `flag`, `github.com/BurntSushi/toml`, `gopkg.in/yaml.v3`. **No new dependencies.**

**Spec:** [`../../SKILLS-DIRECTORY-SPEC.md`](../../SKILLS-DIRECTORY-SPEC.md), [`../DESIGN.md`](../DESIGN.md), [`../../VISION.md`](../../VISION.md). The findings are listed in the traceability table below (IDs R1-R39).

## Global Constraints

- Module `github.com/gherlein/sm`, Go 1.26.2. No new dependencies.
- **Build and test ONLY through the Makefile** — `make build`, `make test`, `make lint`. Never `go build`/`go test`/`go vet`/`go run` directly (a hook blocks it). Editing the Makefile requires the `gherlein:makefile-builds` skill.
- House rules (CLAUDE.md): idiomatic Go; explicit error handling, never ignore returned errors; no forgiving code (assert preconditions, return errors); named constants, no magic numbers; comments explain WHY; no emoji; comments above code, not end-of-line. **Never skip, stub, or weaken a test.** Existing tests may be edited only to track an intentional contract change listed in this plan, and the edit must keep the assertion at least as strict.
- Every task must leave `make test` (all packages) and `make build` (both `bin/sm` and `bin/ms`) green and `gofmt` clean.
- Platform: Linux and macOS. Unix-only syscalls (ownership checks, `flock`) go in files with `//go:build unix` plus a `//go:build !unix` stub so the package still compiles elsewhere.
- Store name pattern `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$` is exported as `skillmeta.NamePattern`; do not duplicate it.
- File modes: directories created by sm/ms are `0o755`, state dir `0o700`, state/lock/ledger files `0o600`; copied files/dirs have group/other **write** bits removed (`perm &^ 0o022`), setuid/setgid/sticky never carried.
- Provenance rule (applies to every delete): sm/ms only remove an entry whose **identity** matches what they recorded — a symlink whose `readlink` equals the recorded target string, or a directory whose `.sm-copy` marker content equals the recorded store path. Type alone (is-a-symlink / has-a-marker) is never enough.
- Exit codes unchanged: sm and ms `0` success, `1` operational error, `2` usage error; `-h`/`--help` exits `0`.

## Behavior contract after the fixes (docs and tests derive from this)

1. **Writes are symlink-safe.** Copy/ingest stage in a random `.sm-stage-*` directory created with `os.MkdirTemp` in the target directory, write only through an `os.Root` opened on that stage, and rename into place after verifying the stage is still the directory that was created. The target directory must be owned by the current user and not world-writable, else sm/ms refuse.
2. **Replacement is a swap.** Refresh/`--force` build the complete new directory first, move the old directory aside (`.sm-old-*`), move the new one in, then delete the old; on failure the old directory is restored. A failed copy never loses the old skill.
3. **Overlap is refused.** `ms` refuses when the resolved target equals, contains, or is contained by the resolved source (either direction), and when dest overlaps source. sm refuses an agent dir that overlaps the store.
4. **Special content.** FIFOs, sockets, devices are skipped with a warning; symlinks inside a skill are copied only when relative and staying inside the skill (others skipped with a warning); a root-level `.sm-copy` in a source is never copied; `SKILL.md` must be a regular file (after following symlinks) and <= 1 MiB.
5. **Symlinked roots are resolved.** A symlinked store skill (copy mode) and a symlinked `ms` source root are resolved with `EvalSymlinks` first. Symlinked skill directories *inside* an `ms` source tree are skipped with a warning unless `--follow-symlinks` is given (loops guarded by visited real paths).
6. **Identity-based reconcile.** A recorded symlink is "ours" only if `readlink` equals the recorded target. If ours and the desired store target differs -> repointed (summary term `~N`, printed in link mode only when N>0). If the recorded name's on-disk entry is no longer ours -> record dropped, entry left alone, warning. A name blocked by a foreign entry -> unmanaged warning.
7. **Locking.** `sync` (non-dry-run), `clean`, and `ms` take an exclusive non-blocking `flock` on `<state path>.lock` (ms: `<ingest ledger path>.lock`); a second concurrent run fails fast: `another sm process is running (lock <path>)`.
8. **State safety.** `state.json` must have `"version": 1`; any other version (or a `null`/versionless document) is a loud error; state file `0o600`, state dir `0o700`.
9. **Empty/unusable store.** Non-conforming or unreadable store entries are *unusable*, not absent: links already recorded for them are kept (warning). If the store has zero skills (and zero unusable entries) while state records entries for an in-scope target, `sync` refuses unless `--allow-empty`.
10. **Orphans.** `sm status` lists state targets not mapped by `agents.toml` (when no `--agent-home`); `sm clean --orphans` removes their sm-owned entries (identity-checked) and drops them from state; `--orphans` is incompatible with `--agent`/`--agent-home` (usage error).
11. **Config strictness.** `agents.toml`: unknown top-level keys are an error; an undefined environment variable or an empty expansion is an error; agent dirs `/` and `$HOME` itself are refused; two agents with the same (resolved) directory are synced once with a warning. CLI path flags expand only `~` (never environment variables); `--agent-home` is symlink-resolved.
12. **Store enumeration** silently ignores entries whose name begins with `.` (e.g. `.git`); other non-conforming entries warn as before; `Stat` errors warn.
13. **`ms` discovery.** Personal skills (relative path has no `plugins` component) outrank plugin skills; among plugin duplicates the newest `SKILL.md` mtime wins, then path order; every skip (invalid, duplicate, collision) counts in `skipped`; unreadable subdirectories warn and are skipped (an unreadable root is an error); invalid-YAML frontmatter is skipped with an actionable message (file path + parser error + hint). `ms` prints a notice when any ingested skill came from a plugin tree and records every ingest in a private ledger `$XDG_DATA_HOME/skills-mapper/ingest-log.json` (never inside the store).
14. **`probe`** inserts new entries inside the existing `[agents]` table (adds the header when absent), refuses an inline `agents = {...}` layout, and TOML-escapes names and directories.
15. **CLI:** `sm`/`ms` with `-h`/`--help` (and `sm help`) print usage to stdout and exit 0; `sm init` rejects unknown flags and `-h` never writes; the "(run 'sm init')" hint appears only when the config file does not exist; `sync` with zero agents prints `no agents enabled in agents.toml`.

## Traceability — every finding maps to a task

| ID | Finding | Task |
| --- | --- | --- |
| R1 | `ms --force` deletes source when source inside dest; non-atomic force | 9 (+2) |
| R2 | symlinked `ms` source root ingests nothing | 8 |
| R3 | `sm` deletes a user-replaced symlink (provenance by type) | 3, 5 |
| R4 | `--store` change leaves stale link target; never repointed | 3, 5 |
| R5 | `sm init -h` writes the config | 7 |
| R6 | predictable staging dir race -> arbitrary file write | 2 |
| R7 | `--copy` of a symlinked store skill (defeats copy, writes marker outside) | 2, 5 |
| R8 | FIFO / `/dev/zero` skill hangs or OOMs | 1, 2 |
| R9 | `SweepStages` deletes foreign `*.sm-stage` entries | 2 |
| R10 | `.sm-copy` marker spoofing | 2, 3 |
| R11 | agent dirs `/` or `$HOME`; unset env var -> `/skills` | 4 |
| R12 | double env expansion of CLI paths | 4, 10 |
| R13 | no locking; concurrent runs lose state | 3, 5, 10 |
| R14 | removed agents orphan links; `clean` can't reach them | 5 |
| R15 | `probe` corrupts hand-edited config | 6 |
| R16 | `ms --` still parses flags | 10 |
| R17 | future/absent state version silently downgraded | 3 |
| R18 | strict YAML rejects real skills with a cryptic message | 1, 8 |
| R19 | aliased / duplicate agent dirs double-count | 4, 5 |
| R20 | transient unreadable entry or empty mount prunes everything | 1, 5 |
| R21 | symlinked skill dirs inside `ms` source skipped silently | 8 |
| R22 | `ms`/copy carry `.sm-copy`, dangling/escaping/absolute symlinks | 2 |
| R23 | `ms` precedence (plugin beats personal), wrong `skipped` count | 8, 10 |
| R24 | one unreadable subdir aborts whole ingest | 8 |
| R25 | `ms` stage leftovers inside the store | 2, 9 |
| R26 | `.git` in store warns on every sync | 1 |
| R27 | status drops warnings / broken links; zero-agent silence; typo'd config key; wrong hint | 4, 5, 7 |
| R28 | permissions: state 0644, world-writable bits copied | 2, 3 |
| R29 | no trust boundary / provenance for ingested third-party skills | 10, 12 |
| R30 | no `-h`/`help`; `-h` exit 2; `ms -h` hides positionals | 7, 10 |
| R31 | BOM / trailing-space frontmatter delimiters | 1 |
| R32 | stale/wrong docs (README, DESIGN, VISION, NEEDED, plans, specs) | 12, 13 |
| R33 | stray report file; `make lint` doesn't fail; no vuln check | 14 |
| R34 | case-insensitive-FS symlink traversal in copy | 2 (via `os.Root`, `O_EXCL`) |
| R35 | README misstatements (`--store` "without touching default", `ms` build/structure) | 12 |
| R36 | README quick start / after-ingest / limitations / troubleshooting / uninstall missing | 12 |
| R37 | Windows claims untested | 13 |
| R38 | SKILL-INSTALL-SPEC wrong cross-references | 13 |
| R39 | regression coverage for all of the above | 11 |

## Review Focus

- **Delete paths** — every `RemoveAll`/`Remove` must be reachable only after an identity check; after Tasks 2/3/5/9 no code path removes an entry on type alone. (Tasks 2, 3, 5, 9)
- **Swap atomicity** — kill the process (simulated failure) between "old moved aside" and "new moved in": the old data must be restorable and never deleted before the new is in place. (Task 2)
- **Backward compatibility of state/markers** — existing `state.json` (version 1) and existing `.sm-copy` markers written by the previous release must still be recognized as ours. (Tasks 2, 3)
- **Both directions of overlap** and **symlinked aliases of the same directory** in `ms` and agent dirs. (Tasks 4, 9)
- **`make test` stays green at every task**, including the two existing e2e suites. (all)

---

### Task 1: `skillmeta` + `store` hardening

**Files:**
- Modify: `internal/skillmeta/skillmeta.go`, `internal/skillmeta/skillmeta_test.go`, `internal/store/store.go`, `internal/store/store_test.go`, and the callers of `store.Enumerate` (`internal/cli/sync.go`, `internal/cli/status.go`, their tests).

**Interfaces:**
- Produces:
  - `skillmeta.ReadName(skillMdPath string) (string, error)` — now: follows symlinks but requires the final target be a **regular file** (`os.Stat` + `IsRegular`); size capped at `MaxSkillFileBytes = 1 << 20` (error beyond); tolerates a UTF-8 BOM before the opening `---` and trailing spaces/tabs on delimiter lines; on a YAML parse error returns an error whose message contains the file path, the parser message, and the hint `frontmatter must be valid YAML (quote values that contain ": ")`.
  - `store.Listing{ Skills []Skill; Warnings []string; Unusable []string }` and `store.Enumerate(storeDir string) (Listing, error)` — replaces the 3-value return. `Unusable` is the sorted set of immediate subdirectory names that exist but are non-conforming or unreadable (including a symlink whose target is missing or whose `Stat` fails). Entries whose name begins with `.` are ignored silently (not warned, not unusable). A `Stat` error on an entry is a warning and the name goes to `Unusable`.
- Consumes: nothing new.

- [ ] **Step 1: Write failing tests.**
  - `skillmeta`: `TestReadName_BOMAndTrailingSpace`, `TestReadName_RejectsNonRegular` (a symlink to a FIFO and a directory named `SKILL.md`), `TestReadName_SizeCap` (file > 1 MiB errors), `TestReadName_InvalidYAMLMessage` (a `description: Use when: x` frontmatter -> error text contains the path and the hint), `TestReadName_SymlinkedSkillMdToRegularFile` (still works).
  - `store`: `TestEnumerate_IgnoresDotEntries` (`.git/` produces no warning and is not in `Unusable`), `TestEnumerate_UnusableSetIncludesBrokenAndNonConforming` (dangling symlink, missing SKILL.md, name mismatch, unreadable SKILL.md via mode 0), `TestEnumerate_DanglingSymlinkWarns`, plus the existing conforming/sorted tests adapted to `Listing`.
- [ ] **Step 2: Run `make test`** — expect FAIL (new signatures undefined).
- [ ] **Step 3: Implement** the changes above. `ReadName` reads through `io.LimitReader(file, MaxSkillFileBytes+1)` and errors when the cap is exceeded. Update `store.Enumerate` to build `Listing`; update every caller (`sync.go`, `status.go`, tests) mechanically — `sync`/`status` print `Listing.Warnings` exactly as before and otherwise ignore `Unusable` for now (Task 5 uses it).
- [ ] **Step 4: Run `make test`** — all packages PASS; `make build` OK.
- [ ] **Step 5: Commit** `fix(store,skillmeta): safe SKILL.md reads, ignore dot entries, report unusable entries`.

---

### Task 2: `link` — secure staging, copy, swap, sweep

**Files:**
- Modify: `internal/link/link.go` (copy side), `internal/link/*_test.go`; create `internal/link/owner_unix.go`, `internal/link/owner_other.go`.
- Modify callers to compile: `internal/cli/sync.go`, `internal/cli/clean.go`, `internal/ingest/write.go`, and tests.

**Interfaces:**
- Produces (package `link`):
  - `type CopyReport struct{ Skipped []string }` — one human-readable line per entry not copied (special file, escaping symlink, absolute symlink, root `.sm-copy`).
  - `func CheckTargetDir(dir string) error` — `MkdirAll(dir, 0o755)`; then `os.Stat(dir)` (follows a symlinked dir) must be a directory, owned by the current effective uid, and not world-writable (`mode&0o002 == 0`); otherwise an error naming the directory and the reason. (`owner_other.go` skips the uid check.)
  - `func CopyDir(src, dst string) (CopyReport, error)` — `src` symlink-resolved with `filepath.EvalSymlinks` first (must resolve to a directory); `dst` must not exist; marker-free. Staging per the contract: `os.MkdirTemp(filepath.Dir(dst), ".sm-stage-*")`, sentinel file `.sm-stage-sentinel` written first, all writes through `os.OpenRoot(stage)`, files created with `O_EXCL`, directories `perm|0o700` masked by `&^ 0o022`, regular files `perm &^ 0o022`; symlinks recreated with `root.Symlink` only when relative and not escaping the skill (Clean(join(dir(rel), target)) does not start with `..`); special files skipped; before rename `os.Lstat(stage)` must be a directory and `os.SameFile` with the creation-time info; sentinel removed; `os.Rename(stage, dst)`; stage removed with `RemoveAll` on every error path.
  - `func CreateCopy(targetDir, name, storePath string) (CopyReport, error)` — `CheckTargetDir(targetDir)`, `CopyDir`-style staging into `targetDir/name` plus marker `.sm-copy` (content `storePath + "\n"`, mode 0o644) written through the root.
  - `func ReplaceDir(src, dst string) (CopyReport, error)` — like `CopyDir` but `dst` may exist: build the full stage first; if `dst` exists, `os.MkdirTemp(parent, ".sm-old-*")` then `os.Rename(dst, oldAside)`; rename stage -> `dst`; on failure of that rename restore (`os.Rename(oldAside, dst)`); only after success `RemoveAll(oldAside)`. Marker-free.
  - `func RefreshCopy(targetDir, name, storePath, recordedSource string) (CopyReport, error)` — `IsOwnedCopy(targetDir, name, recordedSource)` must be true else an error; then the same swap as `ReplaceDir` with a new marker; the old copy is never removed before the new one is in place.
  - `func IsOwnedCopy(targetDir, name, recordedSource string) (bool, error)` — `Lstat` the entry: real directory (not symlink); marker `.sm-copy` is a regular file (`Lstat`), read via `os.OpenRoot(entry)` with a 4096-byte cap, and `strings.TrimSpace(content) == recordedSource`. Anything else is `false, nil`.
  - `func RemoveCopy(targetDir, name, recordedSource string) (removed bool, err error)` — removes only when `IsOwnedCopy` is true.
  - `func SweepStages(targetDir string) (int, error)` — removes only entries named `.sm-stage-*` that are real directories, owned by the current uid, and contain a regular `.sm-stage-sentinel`. A missing `targetDir` is `0, nil`. It never touches `.sm-old-*` (those hold user data after a crash) or old-format `<name>.sm-stage` entries.
- Consumes: nothing new.

- [ ] **Step 1: Write failing tests** (real temp dirs, no mocks):
  - `TestCopyDir_StagingRaceCannotEscape` — pre-plant `<dst>.sm-stage` as a symlink to an outside directory: `CopyDir` writes nothing outside and succeeds (random stage).
  - `TestCopyDir_SymlinkedSourceRootResolved` (absolute and relative link roots): copy equals the real tree; **nothing is written into the real source directory** (no `.sm-copy`, no stage).
  - `TestCopyDir_SkipsSpecialFiles` (FIFO via `syscall.Mkfifo`): returns within a timeout, FIFO reported in `CopyReport.Skipped`, rest copied.
  - `TestCopyDir_SymlinkPolicy` — relative in-tree link copied; absolute link and `../escape` link skipped and reported.
  - `TestCopyDir_NeverCopiesRootMarker` — a source containing `.sm-copy` yields a destination without it.
  - `TestCopyDir_StripsGroupOtherWriteAndSetuid` — source modes `0o4777`/`0o777`/`0o2775` -> `0o755`-ish results with no setuid/setgid and no `go+w`.
  - `TestCopyDir_CaseCollisionFailsLoudly` (two names differing only in case on a case-insensitive FS; if the FS is case-sensitive assert both are created) — uses `O_EXCL` so a colliding write is an error, never an overwrite through a link.
  - `TestCheckTargetDir` — not-owned and world-writable directories refused (world-writable via `chmod 0o777` in the test), group-writable allowed, symlinked directory allowed.
  - `TestReplaceDir_RestoresOldOnFailure` — make the stage rename fail (read-only parent after staging via a build hook) or make the copy fail mid-way (unreadable file): the old directory and its contents are intact, no `.sm-old-*`/`.sm-stage-*` leftovers.
  - `TestReplaceDir_SwapsWhenSuccessful`, `TestRefreshCopy_RequiresOwnership`, `TestRefreshCopy_KeepsOldIfCopyFails`.
  - `TestIsOwnedCopy_MarkerMustMatchRecordedSource` — marker content differs / marker is a symlink / marker is a directory -> false; exact match -> true. `TestRemoveCopy_RefusesSpoofedMarker`.
  - `TestSweepStages_OnlyOwnedSentinelStages` — foreign `x.sm-stage`, `.sm-stage-foo` without sentinel, `.sm-old-abc` all survive; a real orphan (random name + sentinel) is removed.
  - Existing copy/sweep/copydir tests updated to the new signatures with equal or stricter assertions.
- [ ] **Step 2: Run `make test`** — expect FAIL.
- [ ] **Step 3: Implement.** Private helper `stageAndSwap(dst string, replace bool, build func(root *os.Root) error) (CopyReport-less error)` shared by `CopyDir`, `CreateCopy`, `ReplaceDir`, `RefreshCopy`. Walk the real source with `filepath.WalkDir`, but open source files through `os.OpenRoot(realSrc)` (`root.Open(rel)`) so a source-side swap cannot escape either. Update callers (`sync.go` refresh loop uses `RefreshCopy` and passes the ledger value as `recordedSource`; `clean.go`/remove paths pass the ledger value; `ingest/write.go` uses `ReplaceDir`/`CopyDir` and surfaces `CopyReport.Skipped` as warnings — keep the pre-existing non-atomic behaviour of `ms` ONLY until Task 9, i.e. call `CopyDir` for new targets and `ReplaceDir` for forced ones).
- [ ] **Step 4: Run `make test` and `make build`** — PASS.
- [ ] **Step 5: Commit** `fix(link): symlink-safe staged copy, atomic swap, identity-checked removal`.

---

### Task 3: `link` symlink identity + `state` (planner, lock, version, perms)

**Files:**
- Modify: `internal/link/link.go` (symlink side), `internal/state/state.go`; create `internal/state/lock_unix.go`, `internal/state/lock_other.go`; update tests. Callers keep compiling: `link.Inspect` and `link.Remove` remain until Task 5.

**Interfaces:**
- Produces:
  - `link.Entry{ Exists, IsSymlink, IsDir bool; Target string }` and `link.Probe(targetDir, name string) (Entry, error)` — `Lstat` + `Readlink` (raw string); validates the name segment like `Inspect`.
  - `link.RemoveResult` (`Removed`, `Absent`, `NotOurs`) and `link.RemoveIfTarget(targetDir, name, wantTarget string) (RemoveResult, error)` — removes the entry only if it is a symlink whose raw `readlink` equals `wantTarget`; absent -> `Absent`; anything else -> `NotOurs` (left untouched, no error).
  - `state.EntryInfo{ Exists, Ours bool }` (caller computes `Ours`), and
    `state.PlanTarget(desired, recorded map[string]string, inspect func(name string) EntryInfo) state.TargetPlan` with
    `TargetPlan{ Create map[string]string; Repoint map[string]string; Keep []string; Remove []string; Unmanaged []string; Lost []string }`.
    Rules per name: **desired & absent** -> Create. **desired & present & recorded & Ours** -> `recorded[name] == desired[name]` ? Keep : Repoint. **desired & present & recorded & !Ours** -> Unmanaged **and** Lost. **desired & present & !recorded** -> Unmanaged. **recorded & !desired**: absent -> Remove; Ours -> Remove; present & !Ours -> Lost. All slices sorted.
  - `state.Load` rejects `version != 1` and a `null` document with `unsupported state version`; `state.Save` writes the file `0o600` and creates its directory `0o700`.
  - `state.Lock(statePath string) (unlock func() error, err error)` — creates `<statePath>.lock` (`0o600`, dir `0o700`) and takes `flock(LOCK_EX|LOCK_NB)`; on `EWOULDBLOCK` the error text is `another sm process is running (lock <path>)`; `unlock` releases and closes. `lock_other.go` returns a no-op unlock.
- Consumes: Task 2 `link` (none required).

- [ ] **Step 1: Write failing tests.**
  - `planner` table test covering every rule above including Repoint, Lost+Unmanaged, absent-recorded Remove, sorted output (`TestPlanTarget_*`).
  - `TestRemoveIfTarget_*`: removes a matching symlink; `NotOurs` for a user-replaced symlink with a different target; `NotOurs` for a real directory; `Absent`.
  - `TestProbe_*`: symlink target string, dangling link reports `Exists` true with the raw target, real dir, absent.
  - `TestLoad_RejectsVersionMismatchAndNull`, `TestLoad_AcceptsVersion1` (existing round-trip), `TestSave_Modes` (file `0o600`, dir `0o700`).
  - `TestLock_SecondLockFailsFast` (two `Lock` calls on one path in the same test via two file descriptors), `TestLock_ReleaseAllowsRelock`.
  - **Backward compatibility:** `TestLoad_ReadsPreviousReleaseState` — a literal version-1 JSON document with `links` and `copies` as the previous release wrote them.
- [ ] **Step 2: Run `make test`** — expect FAIL.
- [ ] **Step 3: Implement.** Replace the old planner, keeping a thin adapter only if the old signature is still called by `cli`; if callers must change to keep the build green, update `sync.go` to the new planner using `Ours = IsSymlink && Target == recorded[name]` for symlink mode and `link.IsOwnedCopy(..., recorded[name])` for copy mode (Task 5 finishes the behaviour; here keep outputs equivalent where the old tests assert them, and update tests only for the new Repoint/Lost semantics).
- [ ] **Step 4: Run `make test` and `make build`** — PASS.
- [ ] **Step 5: Commit** `fix(state,link): identity-based ownership, version check, locking, private modes`.

---

### Task 4: `pathutil` + `agents` + option parsing hardening

**Files:**
- Create: `internal/pathutil/pathutil.go`, `internal/pathutil/pathutil_test.go`.
- Modify: `internal/agents/agents.go` (+ tests), `internal/cli/options.go` (+ `options_test.go`).

**Interfaces:**
- Produces:
  - `pathutil.ExpandTilde(path string) (string, error)` — `~` and `~/x` only; `~user` is an error; **no environment expansion**.
  - `pathutil.Abs(path string) (string, error)` — `ExpandTilde` + `filepath.Abs` (relative resolves against cwd) + `Clean`.
  - `pathutil.ResolveExisting(path string) (string, error)` — longest existing ancestor through `filepath.EvalSymlinks`, remainder re-appended; non-`ErrNotExist` errors propagate.
  - `pathutil.Within(base, target string) bool` (target equals or is inside base, lexical on resolved inputs) and `pathutil.Overlaps(a, b string) bool`.
  - `agents.Expand` — `~/` and environment expansion where an **undefined** variable is an error (`undefined environment variable $X`) and an empty result is an error; result must be absolute.
  - `agents.Load` — errors when `meta.Undecoded()` is non-empty (message lists the keys; catches `[agent]` typos), keeps the `agents` must-be-a-table check.
  - `agents.CheckDir(dir, home string) error` — refuses `/` and `home` itself.
  - `cli.applyOverrides` — `--store`/`--agent-home` use `pathutil.Abs` (tilde only), `--agent-home` additionally `ResolveExisting`; agent dirs are checked with `CheckDir` and for overlap with the store (`pathutil.Overlaps` on resolved paths, error names both); duplicate agent directories (compared after `ResolveExisting`) are collapsed to the first agent with a warning returned to the caller (`applyOverrides` returns the warnings slice).
- Consumes: Task 3 nothing.

- [ ] **Step 1: Write failing tests:** `pathutil` table tests (tilde forms, `~user` error, relative abs, env var NOT expanded: `"$HOME/x"` stays literal -> `<cwd>/$HOME/x`, ResolveExisting through a symlink with missing tail, Within/Overlaps edge cases incl. `..foo`); `agents`: `TestExpand_UndefinedVarIsError`, `TestExpand_EmptyResultIsError`, `TestLoad_UnknownTopLevelKeyIsError`, `TestLoad_AgentTypoTableIsError`, `TestCheckDir`; `options`: `TestApplyOverrides_AgentHomeSymlinkAliasesResolveToSameDir`, `TestApplyOverrides_DuplicateAgentDirsCollapsed`, `TestApplyOverrides_RejectsAgentDirOverlappingStore`, `TestApplyOverrides_DoesNotExpandEnvInFlags` (`--agent-home '$UNSET'` is a literal relative path, not the current directory).
- [ ] **Step 2: Run `make test`** — expect FAIL.
- [ ] **Step 3: Implement**; migrate `cli/options.go` onto `pathutil` (leave `mscli` for Task 10).
- [ ] **Step 4: Run `make test` and `make build`** — PASS (update existing tests that relied on env expansion in CLI flags to the new contract).
- [ ] **Step 5: Commit** `fix(config): strict agents.toml, safe path expansion, alias/overlap guards`.

---

### Task 5: `sync`, `clean`, `status` rework

**Files:**
- Modify: `internal/cli/sync.go`, `clean.go`, `status.go`, `cli.go`, and their tests; delete `link.Inspect`/`link.Remove` (and their tests, replaced by `Probe`/`RemoveIfTarget` tests from Task 3) once no caller remains.

**Interfaces:**
- Produces: `doSync(cfg, storeDir, statePath string, opts syncOptions, log io.Writer) (string, error)` with `syncOptions{ CopyMode, DryRun, Verbose, AllowEmpty bool }`; `doClean(cfg, statePath, only string, orphans bool, out io.Writer) (string, error)`; `doStatus(cfg, storeDir, statePath string, orphanScope bool, out io.Writer) error`; flags `sync --allow-empty`, `clean --orphans`.
- Consumes: Tasks 1-4 (`Listing`, `Probe`, `RemoveIfTarget`, `PlanTarget`, `IsOwnedCopy/RefreshCopy/RemoveCopy`, `state.Lock`, `applyOverrides` warnings).

- [ ] **Step 1: Write failing tests** (real fixtures; extend the existing `newSyncFixture`):
  - R3: `TestDoSync_DoesNotDeleteUserReplacedSymlink` (replace the link with the user's own, remove the skill from the store, sync: the user's link and its target survive, the record is dropped, a warning is logged).
  - R4: `TestDoSync_RepointsLinkWhenStoreChanges` (`~1` in the summary; `readlink` now equals the new store path); `TestDoSync_RepointsDanglingLinkAfterOldStoreRemoved`.
  - R7/R20: `TestDoSync_KeepsLinksForUnusableStoreEntries` (an unreadable `SKILL.md` and a dangling store symlink: the agent link stays and a warning is logged); `TestDoSync_EmptyStoreRefusedWithRecordedState` (error mentions `--allow-empty`; links intact); `TestDoSync_AllowEmptyPrunes`; `TestDoSync_EmptyStoreWithNoStateIsFine`.
  - R13: `TestDoSync_ConcurrentSecondRunFailsFast` (hold `state.Lock` in the test, run sync, expect the exact error text); `TestDoClean_RespectsLock`.
  - R14: `TestDoClean_OrphansRemovesIdentityCheckedEntries` (agent dropped from config; `--orphans` removes sm links/copies, leaves a user-replaced entry, drops the state target); `TestDoClean_OrphansRejectsAgentFlags`; `TestDoStatus_ListsOrphanedTargets`.
  - R19: `TestDoSync_AliasedAgentDirsSyncedOnce` (warning text names both agents).
  - R27: `TestDoStatus_CountsBrokenLinkAsDangling`, `TestDoStatus_PrintsStoreWarnings`, `TestDoSync_ZeroAgentsMessage`.
  - Copy mode: `TestDoSync_CopyRefreshKeepsOldWhenCopyFails`, `TestDoSync_CopySymlinkedStoreSkillIsMaterializedAsRealDir` (R7) and that the real source directory gets no marker, `TestDoSync_CopyReportsSkippedEntries`.
  - Summary format: link mode prints `+N -M` and adds `~R` only when R>0; copy mode always `+N ~R -M`.
  - Existing sync/clean/status/e2e tests updated for the changed signatures and for `TestSyncEmptyStorePrunesEverything` -> now expects the refusal unless `--allow-empty` (keep the same assertions of what gets pruned under `--allow-empty`).
- [ ] **Step 2: Run `make test`** — expect FAIL.
- [ ] **Step 3: Implement** per the behavior contract items 6, 7, 9, 10, 15 (zero-agent message). `sync`/`clean` take the lock when not dry-run; `Unusable` names are excluded from removal and logged; a recorded name in `Lost` has its record dropped without touching the entry; sweep stages with `link.SweepStages` before copy-mode work; print `Listing.Warnings`.
- [ ] **Step 4: Run `make test` and `make build`** — PASS; `grep -rn "link.Inspect\|link.Remove(" internal/` finds nothing outside tests that deliberately cover `RemoveIfTarget`.
- [ ] **Step 5: Commit** `fix(sync,clean,status): identity-based reconcile, repoint, locking, orphans, empty-store guard`.

---

### Task 6: `probe` rewrite

**Files:** Modify `internal/probe/probe.go`, `internal/probe/probe_test.go`, `internal/cli/probe.go`, `internal/cli/probe_test.go`.

**Interfaces:**
- Produces: `probe.AddMissing(cfgPath string, found []Found, existing []agents.Agent, confirm func(Found) bool) ([]string, error)` (same signature) with new behavior: entries are inserted **inside** the existing `[agents]` table (after its last key/line, before the next `[...]` header or EOF); when no `[agents]` header exists (empty, whitespace, comment-only, or other tables only) a header plus the entries are appended; an inline `agents = {...}` or `agents.<name> =` dotted layout returns an error telling the user to edit the file by hand; keys are bare when they match `^[A-Za-z0-9_-]+$`, else quoted; directory values are written as TOML basic strings with `\` and `"` and control characters escaped; names already present are skipped; the written file must round-trip through `agents.Load`.
- Consumes: Task 4 `agents.Load`.

- [ ] **Step 1: Write failing tests:** `TestAddMissing_InsertsInsideAgentsTableBeforeNextTable`, `TestAddMissing_CommentOnlyFileGetsHeader`, `TestAddMissing_OtherTablesOnlyGetsHeader`, `TestAddMissing_InlineAgentsTableRefused`, `TestAddMissing_SecondRunIsIdempotent` (no duplicate keys; `agents.Load` succeeds), `TestAddMissing_EscapesQuotesAndBackslashesInDir` (a dir containing `"` and `\` round-trips exactly), `TestAddMissing_RoundTripsThroughLoad` for every layout above.
- [ ] **Step 2: Run `make test`** — expect FAIL.
- [ ] **Step 3: Implement** with a line scanner (do not rewrite the file from a decoded struct — comments must be preserved).
- [ ] **Step 4: Run `make test` and `make build`** — PASS.
- [ ] **Step 5: Commit** `fix(probe): insert into the agents table, escape values, refuse unsupported layouts`.

---

### Task 7: `sm` CLI — help, init, hints

**Files:** Modify `internal/cli/cli.go`, `internal/cli/*_test.go` (+ e2e helpers if they assert exit codes of usage).

**Interfaces:**
- Produces: `sm -h|--help|help` -> usage to **stdout**, exit 0; `sm` with no args -> usage to stderr, exit 2 (unchanged); `sm <cmd> -h` -> flag usage, exit 0; `runInit` parses flags via `flag.NewFlagSet` (unknown flag -> exit 2, `-h` -> exit 0, **no file is written**); the `(run 'sm init' to create one)` hint is added only when the load error `errors.Is(err, fs.ErrNotExist)`; usage text lists each command's flags.
- Consumes: nothing new.

- [ ] **Step 1: Write failing tests:** `TestRun_HelpFlagsExitZeroToStdout` (table over `-h`, `--help`, `help`), `TestInit_HelpDoesNotWriteConfig` (config dir stays absent), `TestInit_UnknownFlagExits2`, `TestSubcommandHelpExitsZero` (each subcommand), `TestSync_InvalidConfigDoesNotSuggestInit` vs `TestSync_MissingConfigSuggestsInit`.
- [ ] **Step 2: Run `make test`** — expect FAIL.
- [ ] **Step 3: Implement.** `flag.ErrHelp` maps to exit 0 everywhere.
- [ ] **Step 4: Run `make test` and `make build`** — PASS.
- [ ] **Step 5: Commit** `fix(cli): help flags, init flag parsing, accurate config hints`.

---

### Task 8: `ingest.Discover` hardening

**Files:** Modify `internal/ingest/discover.go`, `internal/ingest/discover_test.go`, and its callers (`internal/mscli/mscli.go`, `internal/mscli/*_test.go`).

**Interfaces:**
- Produces:
  - `ingest.DiscoverOptions{ FollowSymlinks bool }`, `ingest.DiscoverResult{ Skills []SourceSkill; Warnings []string; Skipped int }`, and
    `ingest.Discover(sourceDir, format string, opts DiscoverOptions) (DiscoverResult, error)`.
  - `SourceSkill` gains `FromPlugin bool` (relative path has a `plugins` path component) and `ModTime time.Time` (`SKILL.md` mtime).
- Behavior: the source root is symlink-resolved with `filepath.EvalSymlinks` (must be a directory); a custom recursive walk (not `WalkDir`) so symlinked directories can be handled: without `FollowSymlinks` each symlinked **directory** entry yields the warning `skipping symlink <path> (use --follow-symlinks)` and `Skipped++`; with it, the link is followed once per real path (a visited set of resolved paths prevents loops and escapes duplicates); unreadable subdirectory -> warning + `Skipped++` + continue, unreadable root -> error; a `SKILL.md` whose frontmatter is not valid YAML -> the Task 1 actionable warning, `Skipped++`; invalid name -> warning, `Skipped++`; duplicates resolved by **priority**: personal before plugin, then newest `ModTime`, then path order; each loser -> warning naming both paths, `Skipped++`; prune the same five directory names; stop at a found skill.
- Consumes: Task 1 `skillmeta`.

- [ ] **Step 1: Write failing tests:** `TestDiscover_ResolvesSymlinkedSourceRoot` (absolute and relative link; trailing slash), `TestDiscover_WarnsOnSymlinkedSkillDirByDefault`, `TestDiscover_FollowSymlinksIngestsLinkedSkillOnce` (and a link loop `a -> .` terminates), `TestDiscover_UnreadableSubdirWarnsAndContinues` (mode 0 subdir), `TestDiscover_UnreadableRootErrors`, `TestDiscover_PersonalBeatsPlugin` (plugin path sorts first alphabetically yet loses), `TestDiscover_NewestPluginVersionWins` (`1.9` vs `1.10` by mtime), `TestDiscover_InvalidYAMLFrontmatterSkippedWithHint`, `TestDiscover_SkippedCountsEveryReason`; existing discovery tests adapted to the new return type with equal assertions.
- [ ] **Step 2: Run `make test`** — expect FAIL.
- [ ] **Step 3: Implement**; adapt `mscli` minimally to compile (it prints `Warnings`, uses `Skipped` in the summary; the full CLI work is Task 10).
- [ ] **Step 4: Run `make test` and `make build`** — PASS.
- [ ] **Step 5: Commit** `fix(ingest): resolve symlinked roots, warn on skipped symlinks, deterministic precedence`.

---

### Task 9: `ingest.WriteSkill` — overlap guard and swap

**Files:** Modify `internal/ingest/write.go`, `internal/ingest/write_test.go`.

**Interfaces:**
- Produces: `ingest.WriteSkill(skill SourceSkill, destDir string, force, dryRun bool) (WriteResult, error)` where `WriteResult` gains `Report link.CopyReport`; behavior: after validating the name, resolve `target = ResolveExisting(destDir/name)` and `src = EvalSymlinks(skill.Dir)`; if `pathutil.Overlaps(target, src)` (equal, either contains the other) return the error `refusing to write <target>: overlaps source <src>` **before any mutation, even under `--force` and `--dry-run`**; absent target -> `link.CopyDir` (so the new skill is never a partial directory); existing target without force -> skipped (reason, unchanged); with force -> `link.ReplaceDir` (the swap — the old skill survives any copy failure); `dryRun` mutates nothing; `link.CheckTargetDir(destDir)` is called before the first write.
- Consumes: Task 2 `ReplaceDir/CopyDir/CheckTargetDir`, Task 4 `pathutil`.

- [ ] **Step 1: Write failing tests:** `TestWriteSkill_RefusesWhenSourceInsideTarget` (source `<dest>/foo` with name `foo`, `--force`: error, **source tree intact**), `TestWriteSkill_RefusesWhenTargetInsideSource`, `TestWriteSkill_RefusesAncestorTarget` (source `<dest>/x/y/src`, skill name `x`), `TestWriteSkill_OverlapRefusedUnderDryRun`, `TestWriteSkill_ForceKeepsOldSkillWhenCopyFails` (unreadable file in the source), `TestWriteSkill_ForceSwapsContent`, `TestWriteSkill_DryRunForceExistingDoesNotRemove` (the previously deferred test), `TestWriteSkill_RefusesWorldWritableDest`, `TestWriteSkill_DoesNotCarryRootMarker`.
- [ ] **Step 2: Run `make test`** — expect FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run `make test` and `make build`** — PASS.
- [ ] **Step 5: Commit** `fix(ingest): refuse overlapping targets and make --force a non-destructive swap`.

---

### Task 10: `ms` CLI — flags, ledger, trust notice

**Files:** Modify `internal/mscli/mscli.go`, `internal/mscli/mscli_test.go`, `internal/mscli/e2e_test.go`; create `internal/ingest/ledger.go`, `internal/ingest/ledger_test.go`.

**Interfaces:**
- Produces:
  - `ms [flags] <source> [dest]` flags add `--follow-symlinks`; **`--`** terminates flag parsing (everything after is positional, including values that start with `-`); `-h`/`--help` prints usage (including `<source> [dest]` and every flag) to stdout, exit 0; paths use `pathutil.Abs` (tilde only; `$VAR` stays literal); source/dest guards use `pathutil.ResolveExisting`/`Overlaps` (the private helpers are deleted); the summary is `ms: +<written> (skipped <n>)` where `n` = `DiscoverResult.Skipped` + write-time skips; `-v` prints `copied <name> -> <path>` (with `(plugin)` suffix when `FromPlugin`).
  - `ingest.Ledger` at `$XDG_DATA_HOME/skills-mapper/ingest-log.json` (`0o600`, dir `0o700`; XDG fallback as elsewhere): `ingest.LoadLedger(path)`, `(*Ledger).Add(Record{Name, SourceDir, SourceRoot string; FromPlugin bool; At time.Time})`, `Save`; the whole ms run holds `state.Lock(ledgerPath)`; a dry-run neither locks nor writes.
  - Trust notice: when at least one **written** skill has `FromPlugin`, stderr gets `note: N ingested skill(s) came from plugin directories; review third-party skills before enabling them for your agents`.
- Consumes: Tasks 3, 4, 8, 9.

- [ ] **Step 1: Write failing tests:** `TestRun_DoubleDashEndsFlagParsing` (`ms -- src -weird` treats `-weird` as dest; `ms -- src --force` does NOT force), `TestRun_HelpExitsZeroWithPositionals`, `TestRun_DoesNotExpandEnvInPaths`, `TestRun_SkippedCountIncludesDuplicatesAndInvalid`, `TestRun_FollowSymlinksFlag`, `TestRun_WritesIngestLedger` (record per skill; none for dry-run; none inside the store), `TestRun_PluginNoticeOnStderr` (and absent when no plugin skill), `TestRun_SecondConcurrentRunFailsFast` (hold the lock), `TestRun_SymlinkedSourceRootIngests` (end-to-end R2); e2e tests updated to the new summary/flags.
- [ ] **Step 2: Run `make test`** — expect FAIL.
- [ ] **Step 3: Implement**; remove the now-dead private helpers from `mscli`.
- [ ] **Step 4: Run `make test` and `make build`** — PASS.
- [ ] **Step 5: Commit** `fix(ms): -- and -h handling, literal env in paths, ingest ledger, plugin trust notice`.

---

### Task 11: Independent regression suite (fresh author, black-box)

**Files:** Create `internal/cli/regress_test.go` (package `cli_test` or equivalent black-box package driving `cli.Run`) and `internal/mscli/regress_test.go` (package `mscli_test` driving `mscli.Run`). **The author must not read the implementation** (only the README/DESIGN/this plan's behavior contract); this is the Context-B role.

**Interfaces:** Consumes only the public entrypoints `cli.Run(args, stdout, stderr) int` and `mscli.Run(args, stdout, stderr) int`.

- [ ] **Step 1: Write one black-box test per finding R1-R31 that is observable through the CLI** (hermetic `HOME`/`XDG_*` temp dirs; assert real filesystem state, exit codes, exact stderr substrings from the behavior contract). Must include at least: ms force-inside-dest (source survives, exit 1); symlinked source root ingests; user-replaced symlink survives sync; `--store` change repoints; `sm init -h` writes nothing; `--copy` of a symlinked store skill yields a real directory and an untouched source; FIFO in a skill does not hang (run with a timeout); foreign `*.sm-stage` survives; spoofed `.sm-copy` is not removed; agent dir `/` and `$HOME` refused; undefined env var in `agents.toml` refused; second concurrent `sync` fails fast; `clean --orphans`; `probe` into a table-bearing config; `ms -- … --force`; state version 99 rejected; empty store refused without `--allow-empty`; `.git` in store silent; zero-agents message; plugin notice; ingest ledger outside the store.
- [ ] **Step 2: Run `make test`** — failures are findings for triage (do not edit sources); report any failing test with hypothesis.
- [ ] **Step 3: Commit** `test: black-box regression suite for the 2026-10-20 review findings`.

---

### Task 12: Documentation — README

**Files:** Modify `README.md`.

**Interfaces:** Consumes the final behavior (read the code and run `./bin/sm`/`./bin/ms` hermetically; follow the contract above).

- [ ] **Step 1: Rewrite the README** with this outline: (1) two-sentence what/why + store-to-agents Mermaid diagram (including `ms` as the on-ramp); (2) **Quick start** that works when typed literally (build both, `ms ~/.claude/skills` or clone, `sm probe`, `sm sync --dry-run`, `sm sync`, `sm status`), including the **after-ingest step** (move originals aside) and the note that ingesting all of `~/.claude` also pulls plugin skills; (3) concepts: store, agents, symlink vs copy, `sm` vs `ms` and when to use which; (4) getting skills into the store; (5) configuring agents (`probe`, `init`, `agents.toml`, known-agents table, custom agents); (6) one commands table with every flag (`--dry-run`, `--copy`, `--store`, `--agent-home`, `--allow-empty`, `clean --agent/--agent-home/--orphans`, `probe --yes/--dry-run`, `ms --force/--dry-run/--format/--follow-symlinks/-v`) and **sample output** for each (`+N -M`, `~R`, status columns, ms summary, prompts); (7) advanced: `--store` (state that without `--agent-home` it rewrites the real agent directories; pair with `--agent-home` to trial a library), `--agent-home`, `--copy` (shortened; agentdev as a link, not prose); (8) **Safety and trust model**: identity-based removal, what is verified, residual threat-model limits (directories you write into must be owned by you; skill content is untrusted instructions to an agent; plugin notice; the ingest ledger); (9) **Limitations** (user-global only; Linux/macOS tested, Windows untested; live symlinks vs snapshot copies; containers need `--copy`); (10) **Troubleshooting** (each warning string the tools print and what to do); (11) **Uninstall**; (12) files and locations table (config, state, lock, ingest ledger, store); (13) development: both binaries in build/install, accurate project structure (`cmd/sm`, `cmd/ms`, and every `internal/` package), make targets from `make help`, links to the specs including `SKILL-INSTALL-SPEC.md`.
- [ ] **Step 2: Verify by doing:** execute the Quick start literally in a hermetic temp `HOME` and confirm every claimed output string; fix any mismatch in the README, not the code.
- [ ] **Step 3: Commit** `docs(readme): quick start, trust model, troubleshooting, accurate ms and build sections`.

---

### Task 13: Documentation — DESIGN, VISION, specs, NEEDED, plans

**Files:** Modify `docs/DESIGN.md`, `VISION.md`, `SKILLS-DIRECTORY-SPEC.md`, `SKILL-INSTALL-SPEC.md`, `NEEDED.md`, `docs/plans/2026-08-21-skills-mapper-mvp.md`, `docs/plans/2026-10-20-sm-store-fanout.md`, `docs/plans/2026-10-20-ms-ingest.md`.

- [ ] **Step 1: DESIGN.md** — status `implemented (v1)`; document every behavior-contract item in the matching section (staging/swap/ownership, identity-based provenance, state version/lock/modes, empty-store guard, orphans, config strictness, probe layout rules, `ms` precedence/ledger/trust notice); fix the interrupted-run row (partial state is saved), the probe prompt text (`add <name> (<abs dir>)? [y/N]`), command flags (`status`: `--store/--agent-home`; `clean`: `--agent/--agent-home/--orphans`; `sync`: all flags), add `version` to the state description, correct "pure over a fs" and the plugin wording ("no plugin *format* support; `ms` extracts standard skill folders found inside plugin trees"), reword Windows (untested), extend the Testing section (copy mode, sweep, swap, ms, regression suite), and list the new packages (`pathutil`, `skillmeta`, `ingest`, `mscli`).
- [ ] **Step 2: VISION.md** — binaries `sm` and `ms`; symlinks or copies; plugin wording; Windows wording; trust note.
- [ ] **Step 3: SKILLS-DIRECTORY-SPEC.md** — §4: a consumer **SHOULD** silently ignore entries whose name begins with `.`; §5.4: a skill's symlinks **SHOULD** be relative and stay inside the skill; add a short "Trust" note (skill content is instructions an agent may follow; provenance is the producer's concern).
- [ ] **Step 4: SKILL-INSTALL-SPEC.md** — fix the §10 claim that the staging pattern is "defined once in the directory specification" (equivalence is in the directory spec; staging is §5 here) and the `[equivalence](#43-conflict-and-overwrite)` link (-> `./SKILLS-DIRECTORY-SPEC.md#55-skill-directory-equivalence`).
- [ ] **Step 5: NEEDED.md and the three plans** — prepend a banner to each (`> Historical document. Superseded by ...; kept for context.`) pointing at `VISION.md` / `docs/DESIGN.md` / the newer plan; the two older plans say copy mode and `ms` came later.
- [ ] **Step 6: Verify** every relative link and anchor resolves (script it with `grep`/`test -e`), then commit `docs: bring DESIGN, VISION, specs and historical notes in line with the implementation`.

---

### Task 14: Repository hygiene and tooling

**Files:** Modify `Makefile` (use the `gherlein:makefile-builds` skill), `.gitignore` if needed; `git rm .copy-behaviors-test-report.md`.

- [ ] **Step 1: Makefile:** `lint` must fail on unformatted files (`test -z "$$(gofmt -l .)" && $(GOVET) ./...`); add a `vuln` target that runs `govulncheck ./...` when installed and otherwise prints the install command and exits non-zero (kept out of `all`/`test`); update `help`; keep `build/test/run-tests/clean` and the default help goal.
- [ ] **Step 2: Remove** the stray tracked report file; confirm no other agent-generated report files are tracked (`git ls-files | grep -i report`).
- [ ] **Step 3: Verify:** `make lint` fails when a scratch file is deliberately unformatted (then restore it), passes on the real tree; `make help` lists `vuln`; `make build` produces both binaries; `make test` green.
- [ ] **Step 4: Commit** `build: enforce gofmt in lint, add vuln target; remove stray report`.

---

## Notes for the executor

- Tasks 2, 3 and 5 contain the security-critical changes; their reviews must trace every delete path.
- After Task 14: one whole-branch review (most capable model), then a fresh four-lens re-verification (goals, correctness, security, docs) that re-attempts every original reproduction against the rebuilt binaries and reports FIXED / NOT FIXED / REGRESSED for each finding ID.
- Rulings already made in this plan (cost if wrong in parentheses): strict YAML stays (skills with invalid frontmatter are skipped with a clear hint instead of being rewritten — keeps ingest "placement, not transformation"); symlinked skill directories inside an ms source are skipped with a warning unless `--follow-symlinks` (a third-party tree could otherwise pull in arbitrary directories); group-writable directories are allowed (Ubuntu-style 0775 home dirs) while world-writable and foreign-owned are refused (a group member can still race the target directory — documented residual); `.sm-old-*` directories are never auto-deleted (a crash mid-swap leaves user data that must not be silently removed); the ingest ledger lives outside the store because the directory spec forbids index files in it.

---

### Task 15: Deferred review minors (added during execution)

Added after Tasks 1-10 so the documentation tasks describe the final behavior. Each item was flagged by a task review and deferred as Minor; they are small and independent.

**Files:** Modify `internal/skillmeta/skillmeta.go` (+ new `open_unix.go`, `open_other.go`), `internal/pathutil/pathutil.go`, `internal/agents/agents.go`, `internal/cli/options.go`, `internal/cli/sync.go`, `internal/cli/cli.go`, `internal/probe/probe.go`, and tests in each package plus `internal/link`, `internal/state`, `internal/ingest`.

- [ ] **15.1 `skillmeta.ReadName` stat-then-open race.** A path swapped to a FIFO between `os.Stat` and `os.Open` blocks the open. Open with `O_RDONLY|O_NONBLOCK` (unix file `open_unix.go`, `!unix` fallback `open_other.go` using plain open), then require the OPEN handle's `Stat().Mode().IsRegular()`; keep the friendly pre-check; join the `Close` error instead of ignoring it. Tests: existing FIFO/dev-file cases still return promptly; a regular file still reads.
- [ ] **15.2 `pathutil.ResolveExisting` on a dangling symlink** drops the link name (`alias -> /real/missing` resolves to `<parent>/alias`, so duplicate detection misses it). When `EvalSymlinks` reports not-exist for a path whose final component is a symlink, follow it with `os.Readlink` (relative targets joined to the link's directory), at most 40 hops (a loop is an error), then continue resolving the remainder. Tests: dangling link resolves to its (missing) target; chain of two dangling links; loop -> error; relative dangling target.
- [ ] **15.3 `agents.Load` resolves the home directory unconditionally**, so a config with no agents fails when `HOME` is unset. Resolve it lazily, only when a value needs `~`. Test: an empty `[agents]` table loads with `HOME` unset.
- [ ] **15.4 `rerootAgents` uses the unresolved home.** If `HOME` itself is a symlink and an agent dir is spelled through the unresolved home, re-rooting must still work and must agree with the resolved spelling. Accept an agent dir under either spelling of home. Test: `HOME` is a symlink to the real home; an agent dir spelled via each spelling re-roots to the same directory.
- [ ] **15.5 sync link-mode accounting.** In `applyLinkTarget`, a `Removed` result returned together with an error must be counted (like `clean` does) before the error is returned; under `--verbose` an `Absent` result must NOT print "unlinked" (print `dropped record for <name> (link already gone)`). Tests for both.
- [ ] **15.6 `probe` on a dangling-symlink config.** Writing a config path that is a dangling symlink currently replaces the link with a regular file; resolve the link target (use `pathutil.ResolveExisting`-style logic) and write beside the real (possibly not yet existing) target so the link survives. Test: dangling link config -> link preserved and the target file created with the new entry.
- [ ] **15.7 CLI usage drift guard.** Add a test that, for every command, runs `<cmd> -h`, parses the flag names printed by `PrintDefaults`, and asserts the set equals the `--name` tokens in `commands[name].flagSummary` (both directions); tidy the `usage: sm init [flags]` header when the flag set is empty; make `TestSubcommandHelpExitsZero` match whole flag tokens (not substrings).
- [ ] **15.8 Test hardening.** (a) `TestWriteSkill_RefusesWhenDestEntryIsSymlinkToSource` (`dest/foo -> src`: refused, source intact, also under dry-run). (b) In package `link` (internal test), assert that the staging directory created for a copy is NOT named `<dst>.sm-stage`, is created mode `0o700`, and lives in `dst`'s parent (call the private stage helper with a build callback that inspects the root). (c) A `state` planner case pinning that `Ours` is ignored for an unrecorded name (a present, unrecorded entry is Unmanaged even when the caller reports `Ours: true`).
- [ ] **15.9 Verify and commit:** `make test`, `make build`, `make lint` green; commit `fix: resolve deferred review minors (open race, dangling links, home spelling, accounting, probe symlink, usage drift guard)`.

---

### Task 16: Alias adoption, symlink-mode target check, wording (found while verifying the README)

Found by the Task 12 README verification run: when a user respells an agent directory (for example through a symlink, as with `~/.claude -> ~/claude/claude`), the links sm created under the OLD spelling are still sm's own, but `sync` sees the same physical entries under a key with no records, classifies them Unmanaged, and warns on every run forever; the warning never suggests the fix.

**Files:** Modify `internal/cli/sync.go`, `internal/cli/clean.go` (share the existing alias-merge helper), `internal/mscli/mscli.go` (usage text), `README.md` (affected sections), and tests.

- [ ] **16.1 Adopt identity-verified records from aliased state keys during `sync`.** Before planning an agent directory `D` (key `K`), find other state keys `K'` that resolve (`pathutil.ResolveExisting`) to the same real directory as `D`. For each name recorded in `K'` (links ledger in link mode, copies ledger in copy mode) that `K` lacks: if the entry's identity verifies at `D` (link mode: `link.Probe(D, name)` is a symlink whose readlink equals the record; copy mode: `link.IsOwnedCopy(D, name, record)`), MOVE the record from `K'` into `K` (delete it from `K'`; drop `K'` entirely when both its ledgers become empty), log `adopted <n> record(s) from <K'> into <K>` (always, not only under `--verbose`), and then plan normally so the name is treated as ours. Records that do not verify are left untouched. Never adopt in dry-run (report `would adopt <n> record(s) ...`); adoption happens under the lock like every other state mutation and is persisted with the rest of the run (and by the failure path on error). Reuse the existing alias-merge helper in `clean.go` rather than duplicating the verification. Tests (black-box over `doSync`): `TestDoSync_AdoptsRecordsFromAliasedStateKey` (link mode: sync under spelling A, then sync under symlink-alias spelling B of the same directory: no "not managed by sm" warning, links still exist, state key B holds the records and key A is gone, a skill removed from the store afterwards is pruned through B), the copy-mode twin, `TestDoSync_DoesNotAdoptRecordThatNoLongerVerifies` (user replaced the link: not adopted, entry untouched, normal unmanaged warning), `TestDoSync_DryRunReportsAdoptionWithoutChangingState`.
- [ ] **16.2 Symlink-mode target check.** In `applyLinkTarget`, call `link.CheckTargetDir(agent.Dir)` before creating or repointing links (not in dry-run) so symlink mode refuses a world-writable or foreign-owned agent directory exactly like copy mode and `ms` already do (same error text, the failing agent stops with progress persisted). Test: `TestDoSync_LinkModeRefusesWorldWritableAgentDir` (detect root and `t.Fatal` rather than skip); existing tests must stay green (their temp dirs are owned by the test user).
- [ ] **16.3 `ms` usage wording.** In the `ms -h` text, make the `--follow-symlinks` description grammatically parallel to the other flags (`ingest skills reached through symlinks, including ones outside the source tree`); update any test or README sample that quotes it.
- [ ] **16.4 README.** Update the sections affected by 16.1-16.2: replace the troubleshooting advice about respelled agent directories with the adoption behavior (what `sync` now does and prints), and remove the statement that only copy mode verifies directory ownership (symlink mode now does too). Verify by running the commands in a hermetic temp HOME.
- [ ] **16.5 Verify and commit:** `make test`, `make build`, `make lint` green; commit `fix(sync): adopt identity-verified records from aliased keys; check symlink-mode target dirs`.

---

### Task 17: Final sweep (house-rule violation and README gaps found by the Task 12/13/16 runs)

**Files:** Modify `internal/agents/agents.go`, `README.md`, and tests.

- [ ] **17.1 Ignored error.** `internal/agents/agents.go` (~line 183) does `_ = file.Close()` (the house rules forbid ignoring returned errors). Handle the `Close` error properly (join it with the primary error when there is one; return it when it is the only error). If the surrounding function is a read-only open, a Close error is still reported. Add a test only if the Close failure can be provoked portably; otherwise say so in the report. Then `grep -rn --include='*.go' -E '_ = [A-Za-z_.()]+' internal cmd | grep -v _test.go` must show no remaining ignored-error pattern (explain any that are not errors).
- [ ] **17.2 README touch-ups (verify each by running the commands in a hermetic temp HOME):** (a) state in the Safety/ownership bullet that in symlink mode the target-directory ownership check runs before links are ADDED or repointed (a run that only removes links does not need it); (b) in the status/target-labels section and Troubleshooting, say that a layout made with `--agent-home` shows as `provisioned target` only while its agent is still configured in `agents.toml` — once the agent is removed the same directory shows as `orphaned target` and `sm clean --orphans` removes its sm-owned entries, so clean it with `sm clean --agent-home <root>` first if you want to be explicit; (c) say that after respelling an agent directory `sm status` shows the old entries as `unmanaged` until the next `sm sync` adopts them; (d) note that alternating `sm sync` and `sm sync --agent-home <home>` when `$HOME` itself is a symlink can print an `adopted N record(s)` line each run (harmless; nothing is deleted).
- [ ] **17.3 Verify and commit:** `make test`, `make build`, `make lint` green; every relative link in README still resolves; commit `fix: handle the agents.toml Close error; README target-label and ownership notes`.

---

## Final fix wave (from the five final verification reviews)

Findings are in `.superpowers/sdd/2026-10-20-review-fixes/progress.md` (the `FINAL` entries). Rulings made for this wave: (1) `sm --copy` dereferences a root `SKILL.md` symlink (regular file, at most 1 MiB, already validated by enumeration; the store is the user's own data) so a copy is never a skill without its manifest; `ms` REFUSES a skill whose `SKILL.md` is a symlink that is absolute or leaves the skill (warn, count as skipped, also in dry-run) because it ingests possibly untrusted trees, and verifies the written skill has a regular `SKILL.md`. (2) The empty-store guard exempts an agent only when EVERY recorded name of the in-scope ledger is among the unusable entries. (3) `sync` refuses a WORLD-WRITABLE store; group-writable and foreign-owned read-only stores stay legal. (4) Per-skill failures (an unreadable file) in copy mode and in `ms` are isolated: continue with the other skills, summarize, exit 1.

### Task 18: CLI argument safety, locking, state and guard fixes

**Files:** `internal/cli/*.go` (`cli.go`, `options.go`, `sync.go`, `clean.go`, `status.go`, `probe.go`), `internal/mscli/mscli.go`, `internal/state/*.go`, `internal/agents/agents.go`, `internal/ingest/write.go`, and tests.

- [ ] **18.1 Empty values and stray arguments are usage errors (exit 2).** An explicitly provided empty value for `--store`, `--agent-home`, `--agent` (detect "provided" with `flags.Visit`), and an empty `ms` source or destination positional, is a usage error with a message naming the flag; `sync`, `status`, `clean`, `probe` and `init` reject unexpected positional arguments (`flags.NArg() != 0`), so `sm clean a` and `sm sync now --dry-run` cannot silently widen scope. Tests (black-box over `cli.Run`/`mscli.Run`): each case exits 2, creates/changes nothing, and `sm sync now --dry-run` does not sync.
- [ ] **18.2 `ms` takes the `sm` state lock too.** A real (non-dry-run) `ms` run takes `state.Lock(<sm state path>)` first and then the ledger lock (fixed order; `sm` never takes the ledger lock, so no deadlock); `state.Lock` gains a label so the contention message names the tool (`another sm process is running` for the state lock held by `sm`, `another ms process is running` for the ledger lock); update the message tests. Test: holding the state lock makes `ms` fail fast and write nothing (and the reverse for `sync` while the ledger lock is held does NOT block it).
- [ ] **18.3 `provisionedUnder` accepts both spellings of home** (the unresolved and the `ResolveExisting` one), like `rerootAgentsFromHomes`. Test: `HOME` is a symlink; a layout made with `--agent-home` is `provisioned target` and `clean --orphans` leaves it alone.
- [ ] **18.4 `sm clean` adopts alias-key records before cleaning** each in-scope agent (reuse the sync adoption helper), so removing with `sm clean` after respelling an agent directory removes the links. Test: respell then `clean`: the links are gone and the state is empty.
- [ ] **18.5 Empty-store guard rule:** per ruling (2). Test `TestDoSync_EmptyStoreGuardNotDefeatedByOneNameCollision` (`mkdir work/{docs,Downloads,projects}` with `docs` recorded: refused) and the transient case (all recorded names unusable: kept).
- [ ] **18.6 State load strictness:** a version-1 file whose `targets` is absent or `null` is an error; state keys must satisfy `filepath.Clean(key) == key` (reject `..` segments and redundant separators). Tests for both, and that a previous-release state with `"targets": {}` still loads.
- [ ] **18.7 Adoption refinement:** a verified alias record replaces an owner record that does NOT verify (no false warning, one sync converges). Test.
- [ ] **18.8 Small fixes:** `agents.Expand` expands environment variables first and then a leading `~` (a `$` inside `HOME` is not re-expanded); `sm clean` with no agents prints `no agents enabled in agents.toml`; copy-mode `--dry-run` counts equal the real run for a recorded copy that is already gone (count it as removed in the real run too); a per-skill overlap in `ms` (for example a followed link whose real path is the destination skill) is a skip with a warning and `Skipped++`, not a fatal error (the global destination/source overlap stays fatal); `rerootAgents` (test-only) is removed or used. Tests for each.
- [ ] **18.9 Failure isolation:** in `applyCopyTarget` a per-skill create/refresh error (for example permission denied on a source file) is recorded, the other skills and agents are still processed, state is persisted, the summary is printed, and the command exits 1 with the errors joined; the same in the `ms` write loop (summary line always printed, exit 1 when any skill failed). Identity/state errors (probe errors, lock errors, ledger errors) still stop the run. Tests: unreadable file in one of three store skills with two agents (`sm sync --copy`): the other two skills are copied for BOTH agents, exit 1, a rerun after fixing the permission completes; `ms` with one unreadable skill among three: two written, summary printed, exit 1.
- [ ] **18.10 Verify and commit:** `make test`, `make build`, `make lint` green; commit `fix: argument safety, ms/sm lock sharing, guard and state strictness, failure isolation`.

### Task 19: copy/ingest source safety and removal hardening

**Files:** `internal/link/*.go`, `internal/store/store.go`, `internal/skillmeta`, `internal/ingest/*.go`, `internal/cli/sync.go`, `internal/mscli/mscli.go`, and tests (including new `*_unix_test.go` files).

- [ ] **19.1 Source/destination overlap guard in `link`.** `CopyDir`, `ReplaceDir`, `CreateCopy` and `RefreshCopy` refuse when the resolved source overlaps the resolved destination directory (the destination equal to, inside, or containing the source) — fixes the endless `.sm-stage-*` recursion when a store skill is a symlink to the agent directory or one of its parents. Test: `ln -s <agent dir> <store>/foo; sync --copy` returns promptly with an error naming both paths and creates no nested stages.
- [ ] **19.2 `SKILL.md` symlink handling per ruling (1).** `link` copies a root `SKILL.md` that is a symlink to a regular file (at most 1 MiB) as a regular file with its CONTENT (a new copy option used by `sm --copy` only); `ms` refuses a skill whose `SKILL.md` is a symlink that is absolute or leaves the skill: `Discover` marks it skipped with a warning (`SKILL.md is a symlink that leaves the skill; not ingested`, `Skipped++`) so dry-run reports it too, and `WriteSkill` verifies after the copy (before swapping into place) that the destination has a regular `SKILL.md`, failing the skill otherwise. A relative in-skill symlink to a regular file is still copied as a symlink by `ms`. Tests: `sm --copy` of a store skill whose `SKILL.md` is an absolute symlink yields a copy with a regular `SKILL.md` (same bytes); `ms` skips such a skill (nothing written, nothing ledgered, summary counts it skipped).
- [ ] **19.3 Source identity pinning (Y1).** `store.Skill` and `ingest.SourceSkill` carry the identity (`os.FileInfo` from `os.Stat` of the real directory) captured when the skill was validated; the `link` copy functions take that identity and, after opening the source `os.Root`, require `os.SameFile` against it (error `source changed since it was validated: <path>`); `sync` passes the identity from `Enumerate`, `ms` from `Discover`. `sync` refuses a world-writable store directory (`store <dir> is world-writable; refusing`; group-writable and foreign-owned read-only stores remain legal). Tests: a store skill replaced by a symlink to another directory between enumeration and copy is refused (use an internal test seam or a hook between validation and copy — do not rely on timing); world-writable store refused; group-writable allowed.
- [ ] **19.4 Deletion through handles (Y2).** `privateDir.removeAll`/`removeEmpty` and the aside/stage cleanup remove content through an `os.Root` opened on the verified directory (`SameFile` against the recorded info), deleting children with `Root.RemoveAll` and then removing the empty directory itself, so a path swapped in after `verify()` cannot be recursed into; `SweepStages`, `RemoveIfTarget` and `RemoveCopy` first check the target directory's safety WITHOUT creating it (a non-creating variant of `CheckTargetDir`; a missing directory is fine). Tests via a test hook between `verify()` and the delete: a swapped-in user directory is not deleted.
- [ ] **19.5 Ownership in `IsOwnedCopy` (Y4).** The copy directory and its marker must be owned by the current user (unix; a no-op elsewhere). Test with the strongest assertion possible without root (document what cannot be tested).
- [ ] **19.6 Terminal-safe messages (Y5).** Every warning or error that prints an untrusted name or path (discovery skip messages, `link` skip/report messages, symlink targets) uses `%q` or strips control characters; a shared helper in `pathutil` or a small `safetext` function. Test: a directory named with ESC bytes produces a warning without raw control characters.
- [ ] **19.7 Swap re-check coverage (whole-branch I4).** Add test seams (hooks) so the re-check after `moveAside` in `RemoveIfTarget`/`RemoveCopy`/`RefreshCopy` (`verifyOld`) and the `stage.verify`/`SameFile` checks are exercised by swapping an entry in between; assert the swapped-in entry is restored and the call reports NotOurs or an error. These tests must FAIL when the corresponding check is removed (the implementer demonstrates this once by temporarily deleting a check and recording the failing run).
- [ ] **19.8 Hygiene:** move `syscall.Mkfifo`/`Flock` tests into `*_unix_test.go` files with `//go:build unix`; fix stale comments (`link.go:2`, `cli.go:56`, `clean.go` home-spelling comment); deduplicate the XDG data-home lookup (one shared helper used by `store.DefaultPath`, `cli.defaultStatePath`, `ingest.LedgerPath`) and the name-segment validation ONLY where it is a pure move guarded by the existing tests (skip any item that cannot be done safely and say so in the report).
- [ ] **19.9 Verify and commit:** `make test`, `make build`, `make lint` green; commit `fix: source identity pinning, handle-based deletion, SKILL.md symlink policy, overlap guard, terminal-safe messages`.

### Task 20: Documentation after the final wave

**Files:** `README.md`, `docs/DESIGN.md`, `VISION.md`, `docs/plans/2026-10-20-review-fixes.md` (banner), `docs/plans/2026-10-20-ms-ingest.md`.

- [ ] **20.1 Apply every DOC item (DOC-1..DOC-7) and the security/residual-risk items recorded in `progress.md` (the `FINAL` entries) and the behavior changes of Tasks 18-19:** empty flag values and stray arguments are usage errors; `ms` takes the `sm` state lock as well as its own (so `sync`/`clean`/`ms` serialize; message names the tool); the SKILL.md symlink policy (sm dereferences, ms refuses); source identity pinning and the world-writable-store refusal; deletion through handles (a group writer can no longer cause deletion of foreign directories; the README wording about group-writable target directories must be corrected to what is now true); the owned-marker requirement; control characters in warnings; per-skill failure isolation and exit 1; the `provisioned` label under a symlinked home; `clean` adopting alias records; the new empty-store guard rule; state key and `targets` strictness; copy-mode dry-run limits (it does not read file contents, so it cannot predict skipped-entry warnings or read errors) and that local edits inside a copy are discarded by the next `--copy` sync; the `--agent-home` conventional-spelling caveat and orphaned label after respelling; the lock wording; the missing `merged ...` and `clean --orphans` messages; VISION's state sentence; Go 1.26.2; `make vuln` row; both missing-store forms; `probe` absolute paths; `ms` leaves stage leftovers in the store; the backup `mv` glob note; macOS wording (Linux tested; macOS builds, untested); help text wording; the plugin-note-with-dry-run note; a short "Day to day" section; alias adoption moved to Advanced/Troubleshooting; NEEDED piece 3 stated plainly as out of scope for this repo; the clone URL (use the repository's `origin` remote URL and mark it for the maintainer to confirm); a completed banner on the review-fixes plan and a historical banner note on the ms plan.
- [ ] **20.2 Verify every statement by running the commands** in a hermetic temp HOME; every relative link and anchor must resolve; commit `docs: final accuracy pass after the verification reviews`.
