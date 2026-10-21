# Skills Directory Specification

- **Version:** 0.1 (draft)
- **Status:** proposed
- **Date:** 2026-10-20

This document defines a single, agent-neutral filesystem location where AI
coding agents and the tools that serve them look for **skills**, and the
contract each skill directory must satisfy. It defines *where skills live* and
*what a skill directory is* — nothing else. It does not define any tool,
transport, registry, or how an agent is configured.

The key words **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHALL NOT**,
**SHOULD**, **SHOULD NOT**, **MAY**, and **OPTIONAL** in this document are to be
interpreted as described in RFC 2119.

## 1. Rationale

Agents have converged on a skill *format* — a `SKILL.md` file with YAML
frontmatter and a markdown body — but not on a *location*. Each agent reads
only its own proprietary directory (`~/.claude/skills`, `~/.copilot/skills`,
and so on). As a result, a skill a user wants in every agent must be copied or
linked into each agent's directory individually, and every tool that ships
skills must know about every agent.

This specification removes that fan-out from the producer's concern by naming
one canonical directory. A producer writes a skill once, to the canonical
store. A consumer reads skills from the canonical store. Any per-agent
placement that is still required during the transition is a separate concern,
outside this specification.

## 2. Terminology

| Term | Meaning |
| --- | --- |
| **Store** | The canonical skills directory defined in [Section 3](#3-location). |
| **Skill** | A single directory that conforms to [Section 5](#5-skill-directory-contract). |
| **Producer** | Anything that writes skills into the store (a package, an installer, a person running `git clone`, a binary writing an embedded skill). |
| **Consumer** | Anything that reads skills from the store (an agent, or a tool acting on an agent's behalf). |

## 3. Location

The store is the `skills/` subdirectory of the user's XDG data home:

```
$XDG_DATA_HOME/skills/
```

`$XDG_DATA_HOME` is defined by the XDG Base Directory Specification. When it is
unset or empty, or holds a relative path, consumers and producers **MUST** fall
back to the default:

```
$HOME/.local/share/skills/
```

- There is exactly **one** store per user. This specification defines a
  per-user, user-global location only. It does not define a project-local or
  system-wide store.
- The store **MUST** be treated as owned by the user. A consumer that is not
  acting as a producer **MUST** treat the store as read-only.
- A producer **MAY** create the store directory if it does not exist.

## 4. Store structure

The store is **flat**. Each immediate subdirectory of the store is exactly one
skill; its directory name is the skill's name.

```
$XDG_DATA_HOME/skills/
├── git-ops/
│   ├── SKILL.md
│   └── ...bundled files...
├── clean-comments/
│   └── SKILL.md
└── documentation/
    ├── SKILL.md
    └── templates/
```

- A consumer **MUST** derive the set of skills from the store's **immediate**
  subdirectories only. A consumer **MUST NOT** recurse into subdirectories to
  discover further skills; any directory nested inside a skill is that skill's
  own bundled content, never a separate skill.
- A consumer **MUST** ignore regular files placed directly in the store root
  (for example a `README`), and **MUST** ignore any immediate subdirectory that
  does not conform to [Section 5](#5-skill-directory-contract). A consumer
  **SHOULD** report non-conforming subdirectories rather than failing.
- A consumer **SHOULD** silently ignore any entry in the store root whose name
  begins with `.` (for example `.git`). Such a name can never be a skill name
  (see [Section 5.3](#53-name-and-identity)), and ignoring it without a report
  lets the store be a version-control checkout, and keeps a leftover from an
  interrupted write from being mistaken for a skill.
- The store has no index, manifest, or lock file. The set of skills **is**
  exactly the set of conforming immediate subdirectories. A producer **MUST
  NOT** write, and a consumer **MUST NOT** require, any such index.

## 5. Skill directory contract

A directory is a conforming skill if and only if all of the following hold.

### 5.1 The `SKILL.md` file

- The directory **MUST** contain a file named exactly `SKILL.md` at its root.
- `SKILL.md` **MUST** begin with a YAML frontmatter block delimited by a line
  containing exactly `---`, the YAML mapping, and a closing line containing
  exactly `---`. The remainder of the file is the skill's markdown body.

### 5.2 Frontmatter fields

| Field | Required | Type | Rule |
| --- | --- | --- | --- |
| `name` | **REQUIRED** | string | **MUST** equal the skill's directory name (see 5.3). |
| `description` | OPTIONAL | string | A short, single-line summary used by agents to decide relevance. |

- A producer **MAY** include additional frontmatter keys (for agent-specific
  metadata). A consumer **MUST** ignore keys it does not recognize rather than
  rejecting the skill.
- `description`, when present, **SHOULD** be a single short line — a one-sentence
  summary an agent can scan, not a paragraph. A multi-line YAML block that folds
  into one long line satisfies the type but defeats the purpose; keep it brief.

### 5.3 Name and identity

The skill's **directory name is authoritative**. It is the identifier every
consumer sees without opening a file.

- The directory name **MUST** match `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`:
  lowercase ASCII letters, digits, and hyphens; no leading or trailing hyphen;
  non-empty.
- The frontmatter `name` **MUST** equal the directory name exactly. A consumer
  that finds `name` present and unequal to the directory name **MUST** treat
  the directory as non-conforming (and **SHOULD** report the mismatch).
- Because the store is a flat directory, the filesystem guarantees that each
  skill name is unique within the store. There is no cross-source collision to
  resolve.

### 5.4 Bundled content

The **whole directory** is the unit of a skill. Any files or subdirectories
beside `SKILL.md` (scripts, templates, reference material) are the skill's
resources and belong to it.

- A producer or consumer that copies, mirrors, or moves a skill **MUST** carry
  the entire directory, not `SKILL.md` alone.
- A skill **SHOULD** be **self-contained**: its `SKILL.md` body and bundled
  files **SHOULD NOT** reference files outside the skill directory, because the
  skill may be installed or copied with no surrounding tree present. A reference
  the reader cannot resolve from the skill directory alone is a content defect,
  not a portable skill.
- A symbolic link inside a skill **SHOULD** be relative and **SHOULD** resolve
  to a path inside the skill directory. An absolute link, or one that escapes
  the skill, points at something the skill does not carry: it breaks when the
  skill is copied, and it can expose files outside the skill to whoever reads
  it. As an exception to carrying the entire directory, a tool that copies a
  skill **MAY** omit such links, and **SHOULD** report each one it omits.

### 5.5 Skill-directory equivalence

Some producers and consumers need to decide whether two skill directories are
"the same" — for example, to skip re-writing an identical skill, or to treat an
already-installed skill as a no-op. This specification defines that comparison
once, so every tool answers it the same way.

Two skill directories are **equivalent** when:

- they contain the **same set of relative paths** (every file and subdirectory
  under one appears under the other, and no more), and
- each pair of files at the same relative path has **identical byte content**.

File **permissions**, **ownership**, and **timestamps** are **not** compared. A
file present in one directory and absent in the other, or any difference in
content, makes the two **non-equivalent**. A tool comparing against a directory
it manages **MUST** ignore any private provenance marker it added itself (a file
that is not part of the skill) when judging equivalence.

## 6. Conformance

### 6.1 Conforming skill

A directory that satisfies [Section 5](#5-skill-directory-contract) in full.

### 6.2 Conforming producer

A producer that:

- writes only whole skill directories as immediate children of the store;
- sets each skill's frontmatter `name` equal to its directory name;
- writes no index, lock, or manifest file into the store.

### 6.3 Conforming consumer

A consumer that:

- enumerates skills from the store's immediate subdirectories only;
- ignores regular files and non-conforming subdirectories in the store root;
- requires no index file to discover skills;
- treats the store as read-only unless it is acting as a producer.

A consumer **SHOULD** read skills from the store directly. Until an agent reads
the store natively, an external tool **MAY** mirror each skill into the agent's
own location; such a mirror **MUST** preserve both the directory name and the
entire directory content. The mechanism of mirroring is outside this
specification.

## 7. Non-goals

This specification deliberately does **not** cover:

- **Skill versioning.** No version field is defined or interpreted here.
- **Dependencies between skills.**
- **A registry, marketplace, or network protocol.** How a skill reaches the
  store (git, a package, a download, a binary writing an embedded copy) is a
  producer's concern.
- **Nested namespaces or organization prefixes.** The store is flat.
- **Agent configuration or the per-agent skills locations.** Each agent owns
  its own configuration.
- **Project-local or system-wide stores.** This version defines the per-user
  store only.

## 8. Relationship to other standards

- **`SKILL.md` format** — the converging skill format this specification builds
  on. This document adopts it and adds only the directory and location
  contract.
- **`AGENTS.md`** — a repo-root instructions file. Orthogonal to this
  specification, which concerns a user-global skills directory, not repository
  instructions.
- **XDG Base Directory Specification** — supplies `$XDG_DATA_HOME` and its
  default, which this document uses to name the store.

## 9. Trust

This section is informative.

A skill's content is instructions that an agent may follow, with the agent's
own permissions, and its bundled files may be scripts an agent runs. Placing a
skill in the store makes it available to every consumer that reads the store.
This specification defines no signature, checksum, or provenance record:
deciding whether a skill is trustworthy, and recording where it came from, is
the producer's concern (and the user's, before letting a producer write into
the store). A skill's presence in the store is not evidence that it is safe.
