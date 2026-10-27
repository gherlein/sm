// Package carried implements the install-skill and print-skill subcommands of
// the Skill Self-Installation Specification, part of the Embedded Skills
// Standard (github.com/gherlein/embedded-skills-standard). A tool carries one
// skill embedded in its binary; this package prints that skill to stdout and
// installs it into the canonical store, crash-safely and without needing the
// tool's normal configuration, network, or target resolution.
package carried

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gherlein/sm/internal/store"
)

// Exit statuses. install-skill maps success, refused, and failure to three
// distinct values so a non-interactive caller can tell "already present but
// different" apart from both success and a real error (Skill Self-Installation
// Specification, Section 6). Usage errors reuse the tools' own code 2.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
	exitRefused = 3
	skillFile   = "SKILL.md"
	dirMode     = 0o755
	fileMode    = 0o644
	stageInfix  = ".install-skill-"
	stageDotSep = "."
)

// Skill is a carried skill: the embedded filesystem and the name of the single
// directory it contains, which is also the store entry's name.
type Skill struct {
	FS   fs.FS
	Name string
}

// manifestPath is the embedded path of the carried skill's SKILL.md.
func (s Skill) manifestPath() string {
	return path.Join(s.Name, skillFile)
}

// RunPrint executes `<tool> print-skill`: it writes the carried skill's
// SKILL.md verbatim to stdout and nothing else, and returns the process exit
// code. It takes no flags other than -h/--help; any other argument is a usage
// error so stdout is never polluted with anything but the manifest.
func RunPrint(s Skill, toolName string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(toolName+" print-skill", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	if code, done := parse(flags, args, stdout, stderr, printUsage(toolName)); done {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "%s: print-skill: unexpected argument %q\n", toolName, flags.Arg(0))
		fmt.Fprintln(stderr, printUsage(toolName))
		return exitUsage
	}
	// Written to a bytes.Buffer first so a mid-read failure never leaves a
	// partial, corrupted SKILL.md on stdout (Section 5.3).
	content, err := fs.ReadFile(s.FS, s.manifestPath())
	if err != nil {
		fmt.Fprintf(stderr, "%s: print-skill: cannot read embedded skill: %v\n", toolName, err)
		return exitFailure
	}
	if _, err := stdout.Write(content); err != nil {
		fmt.Fprintf(stderr, "%s: print-skill: %v\n", toolName, err)
		return exitFailure
	}
	return exitOK
}

// RunInstall executes `<tool> install-skill`: it installs the carried skill
// into the store and returns the process exit code (0 success or no-op, 3
// refused, 1 failure, 2 usage error).
func RunInstall(s Skill, toolName string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(toolName+" install-skill", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	force := flags.Bool("force", false, "replace an existing, differing skill of the same name")
	flags.BoolVar(force, "f", false, "replace an existing, differing skill of the same name")
	if code, done := parse(flags, args, stdout, stderr, installUsage(toolName)); done {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "%s: install-skill: unexpected argument %q\n", toolName, flags.Arg(0))
		fmt.Fprintln(stderr, installUsage(toolName))
		return exitUsage
	}

	outcome, target, err := s.install(*force)
	if err != nil {
		fmt.Fprintf(stderr, "%s: install-skill: %v\n", toolName, err)
		return exitFailure
	}
	switch outcome {
	case installed:
		fmt.Fprintf(stdout, "%s: installed %s to %s\n", toolName, s.Name, target)
	case replaced:
		fmt.Fprintf(stdout, "%s: replaced %s at %s\n", toolName, s.Name, target)
	case unchanged:
		fmt.Fprintf(stdout, "%s: %s already installed at %s; nothing changed\n", toolName, s.Name, target)
	case refused:
		fmt.Fprintf(stderr, "%s: install-skill: %s already exists at %s and differs; use --force to replace it\n", toolName, s.Name, target)
		return exitRefused
	}
	return exitOK
}

// outcome is the result of an install attempt.
type outcome int

const (
	installed outcome = iota + 1
	replaced
	unchanged
	refused
)

// install writes the carried skill into the store and reports what it did and
// the target path. The store is resolved through symlinks and the write is
// staged in the parent of the resolved store (same filesystem, outside the
// store's skills/ directory) then renamed into place, so a crash never leaves a
// partial skill directory in the store (Section 6).
func (s Skill) install(force bool) (outcome, string, error) {
	storeDir, err := store.DefaultPath()
	if err != nil {
		return 0, "", err
	}
	if err := os.MkdirAll(storeDir, dirMode); err != nil {
		return 0, "", fmt.Errorf("create store %s: %w", storeDir, err)
	}
	resolvedStore, err := filepath.EvalSymlinks(storeDir)
	if err != nil {
		return 0, "", fmt.Errorf("resolve store %s: %w", storeDir, err)
	}
	target := filepath.Join(resolvedStore, s.Name)

	_, lstatErr := os.Lstat(target)
	exists := lstatErr == nil
	if lstatErr != nil && !errors.Is(lstatErr, fs.ErrNotExist) {
		return 0, target, fmt.Errorf("inspect %s: %w", target, lstatErr)
	}
	if exists {
		same, err := s.equivalent(target)
		if err != nil {
			return 0, target, err
		}
		if same {
			return unchanged, target, nil
		}
		if !force {
			return refused, target, nil
		}
	}

	parent := filepath.Dir(resolvedStore)
	s.sweepLeftovers(parent)

	stageDir, err := os.MkdirTemp(parent, s.stagePrefix())
	if err != nil {
		return 0, target, fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(stageDir)
	if err := s.writeTree(stageDir); err != nil {
		return 0, target, err
	}

	if !exists {
		if err := os.Rename(stageDir, target); err != nil {
			return 0, target, fmt.Errorf("install %s: %w", target, err)
		}
		return installed, target, nil
	}

	// Replace: move the old directory into a private aside, move the new one
	// in, then delete the old. The old is restored if placing the new one
	// fails, so a failure never loses it (Section 6).
	aside, err := os.MkdirTemp(parent, s.asidePrefix())
	if err != nil {
		return 0, target, fmt.Errorf("create set-aside directory: %w", err)
	}
	defer os.RemoveAll(aside)
	held := filepath.Join(aside, s.Name)
	if err := os.Rename(target, held); err != nil {
		return 0, target, fmt.Errorf("set aside %s: %w", target, err)
	}
	if err := os.Rename(stageDir, target); err != nil {
		restoreErr := os.Rename(held, target)
		return 0, target, errors.Join(fmt.Errorf("install %s: %w", target, err), restoreErr)
	}
	return replaced, target, nil
}

// equivalent reports whether the directory at target is already identical to
// the carried skill: a real directory (not a symlink) with the same set of
// relative file paths and identical file contents (Section 4.3; permissions
// and timestamps are not compared). Any symlink or non-regular entry makes it
// differ.
func (s Skill) equivalent(target string) (bool, error) {
	info, err := os.Lstat(target)
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", target, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return false, nil
	}
	embedded, err := s.embeddedFiles()
	if err != nil {
		return false, err
	}
	onDisk, regular, err := diskFiles(target)
	if err != nil {
		return false, err
	}
	if !regular || len(embedded) != len(onDisk) {
		return false, nil
	}
	for rel, want := range embedded {
		got, present := onDisk[rel]
		if !present || !bytes.Equal(want, got) {
			return false, nil
		}
	}
	return true, nil
}

// embeddedFiles reads every file of the carried skill, keyed by its path
// relative to the skill directory (slash-separated).
func (s Skill) embeddedFiles() (map[string][]byte, error) {
	files := make(map[string][]byte)
	err := fs.WalkDir(s.FS, s.Name, func(entry string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		content, err := fs.ReadFile(s.FS, entry)
		if err != nil {
			return err
		}
		files[strings.TrimPrefix(entry, s.Name+"/")] = content
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read embedded skill: %w", err)
	}
	return files, nil
}

// diskFiles reads every regular file under dir, keyed by its slash-separated
// path relative to dir. regular is false when any non-directory, non-regular
// entry (a symlink, a device) is present, which makes the directory differ
// from any carried skill.
func diskFiles(dir string) (files map[string][]byte, regular bool, err error) {
	files = make(map[string][]byte)
	regular = true
	walkErr := filepath.WalkDir(dir, func(entryPath string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !info.Type().IsRegular() {
			regular = false
			return nil
		}
		content, err := os.ReadFile(entryPath)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, entryPath)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = content
		return nil
	})
	if walkErr != nil {
		return nil, false, fmt.Errorf("read %s: %w", dir, walkErr)
	}
	return files, regular, nil
}

// writeTree materializes the carried skill under dst, re-creating the directory
// tree faithfully. The embedded directory's own name is stripped so dst becomes
// the skill directory itself.
func (s Skill) writeTree(dst string) error {
	return fs.WalkDir(s.FS, s.Name, func(entry string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(entry, s.Name)
		rel = strings.TrimPrefix(rel, "/")
		destination := filepath.Join(dst, filepath.FromSlash(rel))
		if info.IsDir() {
			if err := os.MkdirAll(destination, dirMode); err != nil {
				return fmt.Errorf("create %s: %w", destination, err)
			}
			return nil
		}
		content, err := fs.ReadFile(s.FS, entry)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", entry, err)
		}
		if err := os.WriteFile(destination, content, fileMode); err != nil {
			return fmt.Errorf("write %s: %w", destination, err)
		}
		return nil
	})
}

// stagePrefix and asidePrefix are the reserved, discoverable name prefixes for
// this skill's staging and set-aside directories beside the resolved store, so
// a crash's leftovers can be swept (Section 6). os.MkdirTemp appends an
// unpredictable suffix, so another user cannot create the name first.
func (s Skill) stagePrefix() string {
	return stageInfix + s.Name + stageDotSep
}

func (s Skill) asidePrefix() string {
	return stageInfix + s.Name + ".old" + stageDotSep
}

// sweepLeftovers removes any staging or set-aside directories for this skill
// left in parent by an earlier crashed run. It is best-effort: a leftover it
// cannot remove is left for the next run.
func (s Skill) sweepLeftovers(parent string) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	prefix := stageInfix + s.Name + stageDotSep
	oldPrefix := stageInfix + s.Name + ".old" + stageDotSep
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, prefix) || strings.HasPrefix(name, oldPrefix) {
			os.RemoveAll(filepath.Join(parent, name))
		}
	}
}

// parse runs flags and maps help and parse failures to exit codes: ErrHelp is
// a successful request (usage to stdout, exit 0), any other failure is a usage
// error (message and usage to stderr, exit 2). done is true when the caller
// must return the first result immediately.
func parse(flags *flag.FlagSet, args []string, stdout, stderr io.Writer, usage string) (int, bool) {
	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(stdout, usage)
		return exitOK, true
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", flags.Name(), err)
		fmt.Fprintln(stderr, usage)
		return exitUsage, true
	}
	return exitOK, false
}

func printUsage(toolName string) string {
	return fmt.Sprintf("usage: %s print-skill\n  write the carried skill's SKILL.md to stdout", toolName)
}

func installUsage(toolName string) string {
	return strings.Join([]string{
		fmt.Sprintf("usage: %s install-skill [-f|--force]", toolName),
		"  install the carried skill into the store ($XDG_DATA_HOME/skills)",
		"  -f, --force   replace an existing skill of the same name that differs",
		"exit status: 0 installed or already present, 3 refused (exists and differs; use --force), 1 failure, 2 usage",
	}, "\n")
}
