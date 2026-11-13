package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/gherlein/sm/internal/agents"
	"github.com/gherlein/sm/internal/link"
	"github.com/gherlein/sm/internal/mapsel"
	"github.com/gherlein/sm/internal/pathutil"
	"github.com/gherlein/sm/internal/profile"
	"github.com/gherlein/sm/internal/state"
	"github.com/gherlein/sm/internal/store"
)

const mapUsage = "usage: sm map [--profile NAME | --from PATH] [--copy] [--dry-run] [--store DIR] <target-dir> [skill ...]"

// runMap projects a profile-named subset of the store into one caller-named
// folder as reconciled symlinks (or copies with --copy). The target is taken
// literally. It reuses sync's reconcile primitives against a synthetic agent
// whose directory is the target, and marks that state target Mapped so
// clean --orphans leaves it alone.
func runMap(ctx commandContext) int {
	flags := flag.NewFlagSet("map", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	profileName := flags.String("profile", "", "named profile in the profiles dir")
	fromPath := flags.String("from", "", "profile file path")
	dryRun := flags.Bool("dry-run", false, "report changes without making them")
	copyMode := flags.Bool("copy", false, "materialize real copied directories instead of symlinks")
	storeFlag := flags.String("store", "", "override the canonical skills store (default $XDG_DATA_HOME/skills)")

	// map is the one verb with positionals, so it parses them itself: the
	// shared parseFlags rejects positionals and stdlib flag stops at the first.
	positionals, ok := parsePositionals(flags, ctx.args)
	if !ok || len(positionals) == 0 {
		fmt.Fprintln(ctx.stderr, mapUsage)
		return exitUsage
	}
	targetDir := positionals[0]
	adhoc := positionals[1:]

	sources := 0
	if *profileName != "" {
		sources++
	}
	if *fromPath != "" {
		sources++
	}
	if len(adhoc) > 0 {
		sources++
	}
	if sources != 1 {
		fmt.Fprintln(ctx.stderr, mapUsage)
		return exitUsage
	}

	names, err := mapNames(*profileName, *fromPath, adhoc)
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm map: %v\n", err)
		return exitError
	}

	storeDir, err := resolveStore(*storeFlag)
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm map: %v\n", err)
		return exitError
	}
	listing, err := store.Enumerate(storeDir)
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm map: %v\n", err)
		return exitError
	}
	if err := store.RequireNotWorldWritable(storeDir); err != nil {
		fmt.Fprintf(ctx.stderr, "sm map: %v\n", err)
		return exitError
	}
	for _, warning := range listing.Warnings {
		fmt.Fprintf(ctx.stderr, "sm map: warning: %s\n", warning)
	}

	selected, err := mapsel.Select(listing, names)
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm map: %v\n", err)
		return exitError
	}

	resolvedTarget, err := pathutil.Abs(targetDir)
	if err == nil {
		resolvedTarget, err = pathutil.ResolveExisting(resolvedTarget)
	}
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm map: %v\n", err)
		return exitError
	}

	desired := make(map[string]string, len(selected))
	pins := make(map[string]link.StorePin, len(selected))
	for _, skill := range selected {
		desired[skill.Name] = skill.Dir
		pins[skill.Name] = link.StorePin{Dir: skill.Identity, Manifest: skill.ManifestIdentity}
	}
	unusable := make(map[string]bool, len(listing.Unusable))
	for _, name := range listing.Unusable {
		unusable[name] = true
	}

	statePath, err := state.DefaultPath()
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm map: %v\n", err)
		return exitError
	}

	agent := agents.Agent{Name: resolvedTarget, Dir: resolvedTarget}
	opts := syncOptions{CopyMode: *copyMode, DryRun: *dryRun, Verbose: ctx.verbose}

	if !*dryRun {
		if err := tightenStateDir(statePath); err != nil {
			fmt.Fprintf(ctx.stderr, "sm map: %v\n", err)
			return exitError
		}
		unlock, lockErr := state.Lock(statePath, stateLockHolder)
		if lockErr != nil {
			fmt.Fprintf(ctx.stderr, "sm map: %v\n", lockErr)
			return exitError
		}
		defer func() {
			if unlockErr := unlock(); unlockErr != nil {
				fmt.Fprintf(ctx.stderr, "sm map: %v\n", unlockErr)
			}
		}()
	}

	current, err := state.Load(statePath)
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm map: %v\n", err)
		return exitError
	}
	// Mark the target before applying; setLinks/setCopies preserve it.
	marked := current.Targets[resolvedTarget]
	marked.Mapped = true
	current.Targets[resolvedTarget] = marked

	var counts syncCounts
	var skillErrors []error
	if *copyMode {
		counts, skillErrors, err = applyCopyTarget(&current, agent, desired, pins, unusable, statePath, opts, ctx.stderr)
	} else {
		counts, err = applyLinkTarget(&current, agent, desired, unusable, statePath, opts, ctx.stderr)
	}
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm map: %s\n", pathutil.PrintableLines(errors.Join(append(skillErrors, err)...).Error()))
		return exitError
	}

	if !*dryRun {
		if saveErr := current.Save(statePath); saveErr != nil {
			fmt.Fprintf(ctx.stderr, "sm map: %v\n", saveErr)
			return exitError
		}
	}

	if *copyMode {
		fmt.Fprintf(ctx.stdout, "map: +%d ~%d -%d\n", counts.created, counts.refreshed, counts.removed)
	} else {
		fmt.Fprintf(ctx.stdout, "map: +%d -%d\n", counts.created, counts.removed)
	}
	if len(skillErrors) > 0 {
		fmt.Fprintf(ctx.stderr, "sm map: %s\n", pathutil.PrintableLines(errors.Join(skillErrors...).Error()))
		return exitError
	}
	return exitOK
}

// mapNames resolves the selection to a list of skill names from exactly one of
// a named profile, a profile file path, or an ad-hoc command-line list.
func mapNames(profileName, fromPath string, adhoc []string) ([]string, error) {
	switch {
	case profileName != "":
		dir, err := profile.DefaultDir()
		if err != nil {
			return nil, err
		}
		return readProfile(profile.NamePath(dir, profileName))
	case fromPath != "":
		return readProfile(fromPath)
	default:
		return adhoc, nil
	}
}

func readProfile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	names, _, err := profile.Parse(data)
	return names, err
}

// resolveStore returns the canonical store dir, honoring a --store override,
// symlink-resolved so it keys state like the other verbs.
func resolveStore(storeFlag string) (string, error) {
	storeDir, err := store.DefaultPath()
	if err != nil {
		return "", err
	}
	if storeFlag != "" {
		storeDir, err = pathutil.Abs(storeFlag)
		if err != nil {
			return "", err
		}
	}
	return pathutil.ResolveExisting(storeDir)
}

// parsePositionals parses flags that may appear anywhere among positional
// arguments: it re-parses after each positional so a flag following the target
// is still honored.
func parsePositionals(flags *flag.FlagSet, args []string) (positionals []string, ok bool) {
	rem := args
	for {
		if flags.Parse(rem) != nil {
			return nil, false
		}
		rem = flags.Args()
		if len(rem) == 0 {
			return positionals, true
		}
		positionals = append(positionals, rem[0])
		rem = rem[1:]
	}
}
