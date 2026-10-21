// Package mscli implements the ms command line, which ingests a proprietary
// agent's skills tree into the canonical store.
package mscli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gherlein/sm/internal/ingest"
	"github.com/gherlein/sm/internal/link"
	"github.com/gherlein/sm/internal/pathutil"
	"github.com/gherlein/sm/internal/state"
	"github.com/gherlein/sm/internal/store"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const (
	defaultFormat = "claude-code"
	// flagTerminator ends flag parsing; everything after it is positional.
	flagTerminator = "--"

	// stateLockHolder and ledgerLockHolder name the tools each lock belongs to
	// in its contention message: the state lock is shared by sm and ms (ms
	// takes it so an sm sync never sees a store skill mid-swap), the ledger
	// lock is ms's own.
	stateLockHolder  = state.StateLockHolder
	ledgerLockHolder = "ms"
)

// afterDiscover runs once the source tree has been discovered and validated,
// before any skill is written. It is a test seam: tests swap a source skill
// here to prove a skill replaced after validation is never ingested.
// Production code never reassigns it.
var afterDiscover = func(discovered ingest.DiscoverResult) {}

// positionalNames names each positional argument by position for messages.
var positionalNames = [...]string{"source", "destination"}

// Run executes ms with args and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) (exitCode int) {
	flags := flag.NewFlagSet("ms", flag.ContinueOnError)
	flags.SetOutput(stderr)
	// The stdlib calls Usage before returning ErrHelp; usage is printed by Run
	// itself so -h goes to stdout and real errors go to stderr.
	flags.Usage = func() {}
	format := flags.String("format", defaultFormat, "source format of the skills tree")
	force := flags.Bool("force", false, "overwrite skills that already exist in the destination")
	dryRun := flags.Bool("dry-run", false, "report what would be written without writing")
	verbose := flags.Bool("v", false, "print one line per skill")
	flags.BoolVar(verbose, "verbose", false, "print one line per skill")
	followSymlinks := flags.Bool("follow-symlinks", false, "ingest skills reached through symlinks")

	positionals, err := parseInterspersed(flags, args)
	if errors.Is(err, flag.ErrHelp) {
		printUsage(stdout)
		return exitOK
	}
	if err != nil {
		printUsage(stderr)
		return exitUsage
	}
	if len(positionals) < 1 || len(positionals) > 2 {
		printUsage(stderr)
		return exitUsage
	}
	// An empty path (typically an unset shell variable) would resolve to the
	// current directory and ingest from or into it; it is never what was meant.
	for index, positional := range positionals {
		if positional == "" {
			fmt.Fprintf(stderr, "ms: %s must not be empty\n", positionalNames[index])
			printUsage(stderr)
			return exitUsage
		}
	}

	source, err := pathutil.Abs(positionals[0])
	if err != nil {
		fmt.Fprintf(stderr, "ms: %v\n", err)
		return exitError
	}
	info, err := os.Stat(source)
	if err != nil {
		fmt.Fprintf(stderr, "ms: source: %v\n", err)
		return exitError
	}
	if !info.IsDir() {
		fmt.Fprintf(stderr, "ms: source %s is not a directory\n", source)
		return exitError
	}

	destination, err := resolveDestination(positionals[1:])
	if err != nil {
		fmt.Fprintf(stderr, "ms: %v\n", err)
		return exitError
	}
	// Symlinks are resolved first so a destination that links into the source
	// cannot slip past the lexical comparison and overwrite skills with themselves.
	resolvedSource, err := pathutil.ResolveExisting(source)
	if err != nil {
		fmt.Fprintf(stderr, "ms: source: %v\n", err)
		return exitError
	}
	resolvedDestination, err := pathutil.ResolveExisting(destination)
	if err != nil {
		fmt.Fprintf(stderr, "ms: destination: %v\n", err)
		return exitError
	}
	if pathutil.Overlaps(resolvedSource, resolvedDestination) {
		fmt.Fprintf(stderr, "ms: destination %s overlaps the source %s (it is the source, inside it, or contains it); refusing to ingest into the tree being read\n", destination, source)
		return exitError
	}

	var ledger ingest.Ledger
	var ledgerPath string
	if !*dryRun {
		// The sm state lock comes first, then the ledger lock, always in this
		// order; sm never takes the ledger lock, so the two cannot deadlock.
		// Holding sm's lock keeps a concurrent sm sync from reconciling while
		// a --force swap has a store skill moved aside.
		statePath, err := state.DefaultPath()
		if err != nil {
			fmt.Fprintf(stderr, "ms: %v\n", err)
			return exitError
		}
		ledgerPath, err = ingest.LedgerPath()
		if err != nil {
			fmt.Fprintf(stderr, "ms: %v\n", err)
			return exitError
		}
		for _, lock := range []struct{ path, holder string }{
			{statePath, stateLockHolder},
			{ledgerPath, ledgerLockHolder},
		} {
			unlock, lockErr := state.Lock(lock.path, lock.holder)
			if lockErr != nil {
				fmt.Fprintf(stderr, "ms: %v\n", lockErr)
				return exitError
			}
			// Deferred calls run last-in first-out, so the locks are
			// released in the reverse order they were taken.
			defer func() {
				if unlockErr := unlock(); unlockErr != nil {
					fmt.Fprintf(stderr, "ms: %v\n", unlockErr)
					exitCode = exitError
				}
			}()
		}
		ledger, err = ingest.LoadLedger(ledgerPath)
		if err != nil {
			fmt.Fprintf(stderr, "ms: %v\n", err)
			return exitError
		}
	}

	discovered, err := ingest.Discover(source, *format, ingest.DiscoverOptions{FollowSymlinks: *followSymlinks})
	if err != nil {
		fmt.Fprintf(stderr, "ms: %s\n", pathutil.PrintableLines(err.Error()))
		return exitError
	}
	afterDiscover(discovered)
	for _, warning := range discovered.Warnings {
		fmt.Fprintf(stderr, "ms: warning: %s\n", warning)
	}
	// The destination itself is checked once, up front, so an unsafe one
	// stops the run instead of being reported as a failure of every skill by
	// the per-skill isolation below. WriteSkill checks it again before each
	// write; this only decides how a refusal is reported.
	if !*dryRun && len(discovered.Skills) > 0 {
		if err := link.CheckTargetDir(resolvedDestination); err != nil {
			fmt.Fprintf(stderr, "ms: %v\n", err)
			return exitError
		}
	}

	written, skipped, pluginWritten, failed := 0, discovered.Skipped, 0, 0
	// A skill that fails to write (an unreadable source file, say) is
	// reported and the rest are still ingested; the summary is always printed
	// and the exit status says something failed. A skill whose target overlaps
	// its own source (a followed link back into the destination) is skipped
	// like any other unwritable skill, since writing it would delete it.
	for _, skill := range discovered.Skills {
		result, err := ingest.WriteSkill(skill, destination, *force, *dryRun)
		var overlap *ingest.OverlapError
		if errors.As(err, &overlap) {
			skipped++
			fmt.Fprintf(stderr, "ms: warning: skipped %s: %s\n", skill.Name, pathutil.PrintableLines(err.Error()))
			continue
		}
		if err != nil {
			failed++
			// Copy errors name files inside the source tree, which may be
			// third-party plugin content, so they are printed terminal-safe.
			fmt.Fprintf(stderr, "ms: %s: %s\n", skill.Name, pathutil.PrintableLines(err.Error()))
			continue
		}
		if result.Skipped != "" {
			skipped++
			fmt.Fprintf(stderr, "ms: warning: skipped %s: %s\n", skill.Name, result.Skipped)
			continue
		}
		for _, line := range result.Report.Skipped {
			fmt.Fprintf(stderr, "ms: warning: %s: %s\n", skill.Name, line)
		}
		for _, line := range result.Report.Leftovers {
			fmt.Fprintf(stderr, "ms: warning: %s: %s\n", skill.Name, line)
		}
		written++
		if skill.FromPlugin {
			pluginWritten++
		}
		if !*dryRun {
			ledger.Add(ingest.Record{
				Name:       skill.Name,
				SourceDir:  skill.Dir,
				SourceRoot: resolvedSource,
				FromPlugin: skill.FromPlugin,
				At:         time.Now().UTC(),
			})
		}
		if *verbose {
			verb := "copied"
			if *dryRun {
				verb = "would-copy"
			}
			suffix := ""
			if skill.FromPlugin {
				suffix = " (plugin)"
			}
			fmt.Fprintf(stdout, "%s %s -> %s%s\n", verb, skill.Name, filepath.Join(destination, skill.Name), suffix)
		}
	}
	// Records of the skills written are saved even when others failed: those
	// skills are in the store and their provenance must not be lost.
	ledgerSaved := true
	if !*dryRun && written > 0 {
		if err := ledger.Save(ledgerPath); err != nil {
			fmt.Fprintf(stderr, "ms: skills were written to %s but the ingest log could not be saved: %v\n", destination, err)
			ledgerSaved = false
		}
	}

	if pluginWritten > 0 && !*dryRun {
		fmt.Fprintf(stderr, "note: %d ingested skill(s) came from plugin directories; review third-party skills before enabling them for your agents\n", pluginWritten)
	}
	fmt.Fprintf(stdout, "ms: +%d (skipped %d)\n", written, skipped)
	if failed > 0 {
		fmt.Fprintf(stderr, "ms: %d skill(s) could not be written\n", failed)
		return exitError
	}
	if !ledgerSaved {
		return exitError
	}
	return exitOK
}

// parseInterspersed lets flags appear before or after the positional
// arguments, since the stdlib flag package stops at the first positional. A
// bare "--" ends flag parsing for good: everything after it is positional even
// when it starts with a dash.
func parseInterspersed(flags *flag.FlagSet, args []string) ([]string, error) {
	flagArgs, literalArgs := splitAtTerminator(args)
	var positionals []string
	remaining := flagArgs
	for {
		if err := flags.Parse(remaining); err != nil {
			return nil, err
		}
		remaining = flags.Args()
		if len(remaining) == 0 {
			return append(positionals, literalArgs...), nil
		}
		positionals = append(positionals, remaining[0])
		remaining = remaining[1:]
	}
}

// splitAtTerminator splits args at the first "--". The terminator always wins,
// even directly after a value-taking flag such as --format: "--" is never
// accepted as a flag value, so `ms --format -- src` is a missing-value usage
// error instead of silently becoming a bogus format name.
func splitAtTerminator(args []string) (flagArgs, literalArgs []string) {
	for index, argument := range args {
		if argument == flagTerminator {
			return args[:index], args[index+1:]
		}
	}
	return args, nil
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: ms [flags] [--] <source> [dest]")
	fmt.Fprintln(w, "  source  a proprietary agent skills folder")
	fmt.Fprintln(w, "  dest    destination store (default: the canonical skills store)")
	fmt.Fprintln(w, "flags:")
	fmt.Fprintf(w, "  --format string     source format of the skills tree (default %q)\n", defaultFormat)
	fmt.Fprintln(w, "  --force             overwrite skills that already exist in the destination")
	fmt.Fprintln(w, "  --dry-run           report what would be written without writing")
	fmt.Fprintln(w, "  -v, --verbose       print one line per skill")
	fmt.Fprintln(w, "  --follow-symlinks   ingest skills reached through symlinks, including ones outside the source tree")
	fmt.Fprintln(w, "  -h, --help          show this help")
	fmt.Fprintln(w, "  --                  end of flags; everything after it is a path, even if it starts with '-'")
}

func resolveDestination(optional []string) (string, error) {
	if len(optional) == 0 {
		defaultPath, err := store.DefaultPath()
		if err != nil {
			return "", fmt.Errorf("cannot determine default store: %w", err)
		}
		return defaultPath, nil
	}
	return pathutil.Abs(optional[0])
}
