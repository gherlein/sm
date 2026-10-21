# Skill Self-Installation Specification (Go)

- **Version:** 0.4 (draft)
- **Status:** proposed
- **Date:** 2026-10-20

This document defines how a command-line **tool** ships the skill that teaches an
agent to use it, and installs that skill on demand. It is the companion to the
[Skills Directory Specification](./SKILLS-DIRECTORY-SPEC.md): that document
defines *where skills live and what a skill directory is*; this document defines
*how a tool carries its own skill, places it in that store, and prints it on
demand*. It defines the tool-side subcommand contracts and the behavior behind
them; how an agent then discovers an already-installed skill in the store is the
directory specification's concern, not this document's.

The model is a familiar one: installing a tool's skill is to an **agent** what
installing a man page is to a person. The tool knows how to describe itself; the
install step makes that description available where the reader looks for it.

The key words **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHALL NOT**,
**SHOULD**, **SHOULD NOT**, **MAY**, and **OPTIONAL** in this document are to be
interpreted as described in RFC 2119.

## 1. Rationale

A skill and the tool it documents are two artifacts that must be installed
together to be useful, yet they are usually shipped and placed separately: the
binary lands on `$PATH`, and the skill must be fetched, unpacked, and copied
into a skills directory by some other step. A user or agent who obtains only the
binary has a working tool that no agent knows how to drive.

This specification removes the second step. A conforming tool **carries its own
skill inside the executable** and installs it with a single, fixed subcommand.
Whoever has the binary has everything: they run one command and the skill is in
the canonical store, ready for any agent. The tool becomes the distribution
channel for its own documentation.

Because the subcommand is fixed by this specification, the install step is also
*discoverable*. An agent that has a conforming binary and has never seen it
before can install its skill without being told how, the same way a person can
reach for `man` without knowing which package provided a program.

A second need is lighter still. An agent does not always want the skill written
to disk — sometimes it just needs the instructions **in context, now**, for a
tool it may touch only once. A conforming tool therefore also **prints** its
carried skill on demand, to standard output, so an agent can read it directly:
nothing installed, nothing to clean up, no store required. Reading the skill and
installing it are the two ways to obtain it, and a conforming tool offers both.

## 2. Terminology

This document uses the terms **Store**, **Skill**, **Producer**, and
**Consumer** as defined in the [Skills Directory
Specification](./SKILLS-DIRECTORY-SPEC.md). In addition:

| Term | Meaning |
| --- | --- |
| **Tool** | A single executable that conforms to this specification. A tool is a *producer* in the sense of the directory specification. |
| **Carried skill** | The one skill a tool embeds within itself and installs. |
| **Install subcommand** | The fixed subcommand, defined in [Section 4](#4-the-install-skill-subcommand), by which a tool installs its carried skill. |
| **Print subcommand** | The fixed subcommand, defined in [Section 5](#5-the-print-skill-subcommand), by which a tool writes its carried skill's `SKILL.md` to stdout. |

## 3. The carried skill

- A conforming tool **MUST** carry **exactly one** skill. This specification
  does not define a tool that installs more than one skill, nor a way to select
  among several.
- The carried skill **MUST** be contained **within the executable itself**, such
  that a tool obtained as a single binary file, with no accompanying files, can
  install its skill. A tool **MUST NOT** depend on reading its skill from a path
  beside the binary, a network location, or any source external to the
  executable.
- The carried skill **MUST** be a conforming skill as defined in [Section 5 of
  the directory specification](./SKILLS-DIRECTORY-SPEC.md#5-skill-directory-contract):
  a directory containing `SKILL.md` with valid frontmatter, whose `name` equals
  the directory name, carried whole with all of its bundled files.
- The carried skill **MUST** be **self-contained**: its `SKILL.md` body and
  bundled files **MUST NOT** reference files outside the skill directory — a
  repository path, a sibling document, a build artifact. The skill is installed
  from a lone binary with no surrounding tree, so everything it needs to be
  useful **MUST** travel inside the skill directory. A reference the reader
  cannot resolve from the installed directory alone (for example a link to
  `docs/…` or `REQUIREMENTS.md` in the tool's source repo) is a conformance
  defect. (This strengthens, for a carried skill, the self-containment the
  directory specification recommends for every skill.)
- The *mechanism* by which the skill is carried is **not** mandated. See
  [Section 8](#8-carrying-the-skill-in-a-go-binary) for the common Go method.

## 4. The `install-skill` subcommand

### 4.1 Name

A conforming tool **MUST** expose a subcommand spelled exactly:

```
install-skill
```

The spelling is fixed by this specification and is not configurable. A conforming
tool invoked as `<tool> install-skill` **MUST** install its carried skill as
described below. This fixed spelling is what makes the install step discoverable:
`<any conforming tool> install-skill` always means the same thing.

A tool **MAY** also surface the same action under additional names or flags, but
**MUST** support `install-skill` regardless.

### 4.2 Behavior

Invoked with no conflicting target (see 4.3), `install-skill` **MUST**:

- resolve the store location exactly as the directory specification requires —
  `$XDG_DATA_HOME/skills/`, falling back to `$HOME/.local/share/skills/` when
  `$XDG_DATA_HOME` is unset, empty, or relative;
- create the store directory if it does not exist (a producer **MAY** do so);
- write the carried skill as an immediate child of the store, as a single
  conforming skill directory whose name is the skill's `name`, carrying the
  entire directory content faithfully (every bundled file, preserving the
  directory tree);
- leave every other entry in the store untouched.

A tool acting under `install-skill` is acting as a *producer* and is therefore
exempt from the directory specification's read-only rule for the store; it
**MUST** still write only its own single skill directory and **MUST NOT** write
any index, lock, or manifest file.

`install-skill` **MUST NOT** require any of the tool's normal startup — its
configuration files, network connections, credentials, or target resolution.
Installing a skill only resolves the store and writes a directory; it **MUST**
succeed on a tool that has never been configured and has no service to reach. A
tool whose other subcommands load a config or open a connection first **MUST**
bypass that path for `install-skill`, so a freshly obtained binary can install
its skill before it is set up for anything else.

Installing into the store makes the skill available to any agent that reads the
store directly and to a fan-out tool (such as `sm`) that mirrors the store into
agents' own proprietary directories. An agent that reads **only** its own
directory sees the installed skill once that mirror step runs. Populating the
store is this specification's job; mirroring it into agent directories is **not**
— that is the directory specification's concern and the fan-out tool's. A user
provisioning such an agent runs `<tool> install-skill` *and* a mirror step; the
two are deliberately separate.

### 4.3 Conflict and overwrite

The target is the skill directory named after the carried skill, inside the
store.

- If no directory of that name exists, the tool **MUST** write the skill
  ([Section 4.2](#42-behavior)) and report success.
- If a directory of that name **already exists**:
  - By default the tool **MUST NOT** overwrite it. It **MUST** leave the existing
    directory unchanged, **MUST** write an error to stderr that identifies the
    conflict (the skill name and the store path), and **MUST** exit with the
    conflict exit status ([Section 6](#6-exit-status-and-output)).
  - When invoked with `-f` or `--force`, the tool **MUST** replace the existing
    directory with the carried skill unconditionally and report success.
  - As an exception, if the existing directory is already **equivalent** to the
    carried skill — by the
    [skill-directory equivalence](./SKILLS-DIRECTORY-SPEC.md#55-skill-directory-equivalence)
    defined in the directory specification (same set of relative paths and
    identical file contents; permissions and timestamps are not compared, and a
    differing, extra, or missing file makes them non-equivalent) — the tool
    **MUST** treat the invocation as a successful no-op (whether or not
    `--force` is given) and **SHOULD** report that nothing changed. There is
    nothing to overwrite.

The tool **MUST NOT** prompt interactively. The choice is expressed entirely by
the presence or absence of `--force`. This keeps `install-skill` safe to run
from an agent, a script, or a CI job with no controlling terminal: it either
succeeds, no-ops, or fails with a clear status and message, and never blocks
waiting for input.

A tool **MAY** offer `--force` under additional spellings, but **MUST** accept
both `-f` and `--force`.

## 5. The `print-skill` subcommand

### 5.1 Name

A conforming tool **MUST** expose a second subcommand spelled exactly:

```
print-skill
```

As with `install-skill` ([Section 4.1](#41-name)), the spelling is fixed by this
specification and is not configurable, so `<any conforming tool> print-skill`
always means the same thing. A tool **MAY** surface the same action under
additional names or flags, but **MUST** support `print-skill` regardless.

### 5.2 Behavior

`print-skill` writes the carried skill's instructions to standard output so that
an agent can read the skill **directly into its context without installing it** —
the skill need not be, and need not become, present anywhere on the filesystem.
It is the read-into-context counterpart to `install-skill`'s write-to-disk.

A conforming tool invoked as `<tool> print-skill` **MUST**:

- write the **exact, unmodified bytes** of the carried skill's `SKILL.md` —
  frontmatter and body — to **stdout**, and write **nothing else** to stdout: no
  banner, no file path, no added heading, no trailing decoration. A caller
  **MUST** be able to capture stdout verbatim as the skill's `SKILL.md`.
- take the content **from within the executable**. It **MUST NOT** read the
  store, the installed copy, or any filesystem skill, and **MUST** produce
  identical output whether or not the skill has ever been installed. Like
  `install-skill` ([Section 4.2](#42-behavior)), it **MUST NOT** require the
  tool's normal configuration, network, credentials, or target resolution, and
  **MUST** succeed on a freshly obtained, unconfigured binary.
- make **no change to the filesystem**. `print-skill` is a pure read with no
  side effects; it **MUST NOT** create, modify, or remove any file or directory,
  in the store or anywhere else.

`print-skill` emits the `SKILL.md` instructions only — the one artifact an agent
reads into context. It does **not** emit the skill's bundled files (scripts,
templates, reference material); those are filesystem resources, and a caller that
needs them on disk uses `install-skill`. Because a carried skill is
self-contained ([Section 3](#3-the-carried-skill)), its `SKILL.md` body is
meaningful on its own; any bundled file it points to becomes reachable only once
the skill is installed.

### 5.3 Exit status and output

- On success the tool **MUST** write `SKILL.md` to stdout and exit `0`.
- `print-skill` has **no conflict or refusal case** — it never consults or writes
  the store, so the install outcomes of [Section 6](#6-exit-status-and-output)
  do not apply to it. Its only possible failure is internal (the embedded skill
  cannot be read), which for a correctly built tool is unreachable; should it
  occur, the tool **MUST** write a human-readable message to **stderr**, exit
  with a non-zero status, and **MUST NOT** write a partial or corrupted
  `SKILL.md` to stdout.

## 6. Exit status and output

A conforming tool **MUST** communicate the outcome through its process exit
status and streams, so that a non-interactive caller can act on the result
without parsing prose.

- Normal, expected output (for example a confirmation of what was installed and
  where) **MUST** go to **stdout**.
- Every error condition **MUST** be reported on **stderr** with a
  human-readable message that identifies the condition.
- The tool **MUST** use **distinct, documented exit statuses**. At minimum,
  **success**, **refused** (target exists and differs, no `--force`), and
  **failure** **MUST** map to three different values, so a non-interactive caller
  can tell "already present but different" apart from both success and a real
  error — that distinction is the one that changes what a caller does next. A
  tool **SHOULD** further split **failure** into a write error and an
  environment error (store location unresolvable) with separate values, but
  **MAY** fold them into one failure status: the environment case is nearly
  unreachable in practice (it needs `$XDG_DATA_HOME` unusable *and* `$HOME`
  unset), and a tool with a pre-existing fixed exit-code contract need only
  reserve a code for *refused*.

| Outcome | Exit status | Stream |
| --- | --- | --- |
| Success — skill installed, or equivalent no-op | `0` | stdout |
| Refused — target exists and differs, no `--force` | non-zero, distinct from success and failure | stderr |
| Failure — store unwritable, I/O error, partial write, or store location unresolvable | non-zero (one value, or split per the SHOULD above) | stderr |

- The specific non-zero numeric values are the tool's own choice, but the
  required outcomes **MUST** map to different values as above, and the tool
  **MUST** document them (for example in `install-skill --help`).
- On any failure the store **MUST** be left in a conforming state: a failed or
  partial write **MUST NOT** leave a malformed or partial skill directory in the
  store. The write **SHOULD** be **crash-safe**, built as follows:
  - **Stage beside the store's real location.** Build the skill directory in a
    temporary location on the **same filesystem** as the store — so the final
    move is a rename, not a cross-device copy — but **outside** the store itself.
    Resolve the store path first, following symlinks: `$XDG_DATA_HOME/skills` may
    itself be a symlink into another filesystem (a dotfiles checkout, for
    example), and staging under the literal `$XDG_DATA_HOME/` would then make the
    final rename cross-device and fail with `EXDEV`. Stage in the **parent of the
    resolved store path** instead, which is same-filesystem in both cases. Do
    **not** stage inside the store's `skills/` directory, which
    [Section 4.2](#42-behavior) reserves for finished skill directories.
  - **Name staging and set-aside directories predictably.** A crash can only be
    cleaned up if its leftovers are findable, so give every staging and set-aside
    directory a reserved, discoverable name beside the resolved store (for example
    `.install-skill-<name>.<unique>`, where `<unique>` is unpredictable, as
    `os.MkdirTemp` makes it, so another user cannot create the name first). A
    tool **MUST** remove its own staging and set-aside directories on every exit
    path, and **SHOULD** sweep any matching that reserved pattern left over from
    an earlier crash before it begins.
  - **Move into place by rename.** When no directory of the name exists, a single
    `rename` of the staged directory into the store is the whole write; a crash
    before it leaves the store untouched.
  - **Replace carefully under `--force`.** A portable, truly *atomic* directory
    **replacement** does not exist: `rename(2)` cannot replace a non-empty
    directory, and `RENAME_EXCHANGE` is Linux-only. Replacing an existing
    directory therefore passes through a brief instant with no directory at the
    name, which is unavoidable off Linux. What the tool **MUST** guarantee is that
    it never leaves a **partial or malformed** skill directory in the store, and
    never deletes the old directory before the replacement is fully staged and
    ready — for example: stage the new directory completely, move the old one
    aside (into the staging area beside the resolved store), move the new one in,
    then delete the old.

## 7. Delivery of the tool

How the binary itself reaches the user is outside the normative contract above —
a conforming tool installs its skill the same way regardless of how it arrived.
This section is informative, and exists to make one advantage concrete.

A tool may be delivered by any channel, including:

- a system package manager — a Homebrew formula, a Debian/Ubuntu `.deb`
  installed with `apt`, an RPM;
- a language ecosystem installer — an npm package or other Node-style installer,
  `go install`, and the like;
- a container image;
- a plain download of the binary from a release page, placed on `$PATH` by hand.

In every case the pattern is identical: obtain the binary, then run
`<tool> install-skill` once. The skill is not a second artifact to locate,
version, and place — it travels inside the executable.

**The Go advantage.** A Go program compiles to a single, statically linked
executable with no runtime to install alongside it. When that one binary also
contains its own skill, *the entire tool and its agent-facing documentation are a
single file*. Installing the tool and installing the skill collapse into one
artifact and one command — no separate skill package, no post-install hook to
copy files into a skills directory, nothing to keep in sync between the binary's
version and a separately shipped skill. For users and for agents provisioning a
machine, this is a substantial simplification over any scheme that ships the
skill as a second, independently placed file. This is the primary reason this
specification is framed around Go, though its normative requirements are defined
by *outcome* and bind any tool that chooses to meet them.

## 8. Carrying the skill in a Go binary

This section is **informative**. It describes the common way to satisfy the
"carried within the executable" requirement of [Section 3](#3-the-carried-skill)
in Go; it is a reference, not a mandate, and a tool that meets the outcome by any
other means is equally conforming.

The idiomatic Go mechanism is the standard library's `embed` package. The skill
directory is kept in the tool's own source tree and compiled into the binary with
a `//go:embed` directive, producing an `embed.FS` the tool reads at install time:

```go
import "embed"

//go:embed all:skill/<name>
var skillFS embed.FS
```

At `install-skill` time the tool walks `skillFS` and writes each embedded file to
the resolved store path, re-creating the directory tree faithfully — the whole
skill directory, `SKILL.md` and every bundled file, with the directory named for
the skill's `name`.

Notes for an implementer taking this route:

- **Placement of the embed directive.** `//go:embed` is resolved relative to the
  Go package that declares it and **cannot** use `..` or reach a sibling or
  parent directory, so the `.go` file carrying the directive must live **at or
  above** the skill directory. When the tool's `main` package is elsewhere in the
  repository (a common layout — `cmd/<tool>/` or `utilities/<tool>/` with the
  skill under a top-level `skill/`), the embed will not reach the skill. Put a
  small embedding package in the directory that **contains** the skill —
  `skill/embed.go` declaring `//go:embed all:<name>` — exporting the `embed.FS`.
  Do **not** put the embedding file *inside* the skill directory: a directive
  cannot name its own package directory (`//go:embed .` is rejected as invalid
  pattern syntax), so it would have to embed `all:*`, which pulls `embed.go` into
  the skill itself. The installed skill would then carry a stray `.go` file and
  no longer match the store entry under the
  [equivalence](./SKILLS-DIRECTORY-SPEC.md#55-skill-directory-equivalence)
  check that [Section 4.3](#43-conflict-and-overwrite) applies. Embedding from one level up
  keeps the skill directory clean. The embedded directory's name becomes the
  installed skill's name, so it must equal the frontmatter `name`
  (`skill/<name>/SKILL.md`, not `skill/SKILL.md`).
- Use the `all:` prefix so that bundled files an ordinary embed would skip (for
  example dotfiles) are included; the skill directory must be carried whole.
- `embed.FS` presents files read-only and with fixed metadata; the tool chooses
  the permissions of the files it writes. Written files **SHOULD** be readable by
  the user and **SHOULD NOT** be world-writable.
- Write crash-safely — stage on the store's filesystem but outside the store,
  then rename into place — to honor the store-integrity requirement of
  [Section 6](#6-exit-status-and-output).

## 9. Conformance

### 9.1 Conforming tool

An executable that:

- carries exactly one skill, contained within the executable itself, that is a
  conforming, self-contained skill per the directory specification;
- exposes the `install-skill` subcommand spelled exactly as in
  [Section 4.1](#41-name), and installs without requiring the tool's normal
  configuration, connection, or target resolution;
- on that subcommand, installs the carried skill into the store resolved per the
  directory specification, writing only its own single skill directory and no
  index, lock, or manifest;
- refuses to overwrite a differing existing directory of the same name unless
  `-f`/`--force` is given, treats an existing directory equivalent to the carried
  skill as a successful no-op, and never prompts interactively;
- reports outcomes with distinct, documented exit statuses (success, refused, and
  failure at least distinct), normal output on stdout and errors on stderr, and
  never leaves a malformed or partial skill directory in the store.
- exposes the `print-skill` subcommand spelled exactly as in
  [Section 5.1](#51-name), writes the carried skill's `SKILL.md` verbatim to
  stdout and nothing else, takes it from within the executable rather than the
  filesystem, succeeds on an unconfigured binary, and changes nothing on the
  filesystem.

### 9.2 Relationship to the directory specification

A conforming tool is, by construction, a *conforming producer* as defined in
[Section 6.2 of the directory specification](./SKILLS-DIRECTORY-SPEC.md#62-conforming-producer).
This specification adds only the carrying-and-installing contract; everything
about the store's shape and the skill's format is inherited unchanged.

## 10. Non-goals

This specification deliberately does **not** cover:

- **Uninstalling or updating skills** beyond the overwrite behavior of
  [Section 4.3](#43-conflict-and-overwrite). There is no `uninstall` subcommand
  defined here, and no version comparison — a skill is installed, refused, or
  force-replaced, nothing more.
- **Multiple skills per tool.** A conforming tool carries exactly one.
- **Skill versioning.** Inherited as a non-goal from the directory
  specification; no version field is defined or interpreted.
- **The agent-side read path.** How an agent discovers and uses an installed
  skill is the directory specification's concern and the agent's own, not this
  document's.
- **Distribution of the binary.** How the tool reaches the user
  ([Section 7](#7-delivery-of-the-tool)) is informative context, not a
  requirement.

## 11. Relationship to other standards

- **[Skills Directory Specification](./SKILLS-DIRECTORY-SPEC.md)** — defines the
  store and the skill directory contract this document installs into. This
  document is a producer-side companion to it. The directory specification also
  defines
  [skill-directory equivalence](./SKILLS-DIRECTORY-SPEC.md#55-skill-directory-equivalence),
  which §4.3 above uses for its no-op rule.
- **`SKILL.md` format** — the skill format both documents build on, unchanged.
- **XDG Base Directory Specification** — supplies the store location, by way of
  the directory specification.

Placing a skill *directory* safely is a recurring problem. `install-skill`
places one **into the store**; a fan-out tool such as `sm` (in its copy mode)
places one **from the store into an agent's directory**. Both abandon "is it a
symlink?" as a provenance signal and must answer the same two questions — *is
this existing directory equivalent to what I would write?* and *how do I replace
a directory crash-safely?* The first is answered once, for every tool, by the
[skill-directory equivalence](./SKILLS-DIRECTORY-SPEC.md#55-skill-directory-equivalence)
of the directory specification. The second is answered for the store side by
the staging-and-rename pattern in [Section 6](#6-exit-status-and-output) of this
document: stage completely, move the old directory aside, rename the new one in,
then delete the old.

`sm` and its ingest companion `ms` (which also writes into the store, but is
not an `install-skill` tool) follow the same pattern with one difference in
placement: they build each copy in a randomly named, dot-prefixed staging
directory (`.sm-stage-*`) **inside** the directory that will hold the result,
move a replaced entry aside into a private `.sm-old-*` directory there, and
check that the stage is still the directory they created before the final
rename. Consumers ignore such dot-prefixed entries
([directory specification, Section 4](./SKILLS-DIRECTORY-SPEC.md#4-store-structure)).
This informative note does not relax Section 6 for a conforming
`install-skill` tool.
