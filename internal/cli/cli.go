// Package cli implements the sm command line.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"sort"

	"github.com/gherlein/sm/internal/agents"
	"github.com/gherlein/sm/internal/pathutil"
	"github.com/gherlein/sm/internal/state"
	"github.com/gherlein/sm/internal/store"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// commandContext carries the parsed global options to a subcommand.
type commandContext struct {
	args    []string
	verbose bool
	stdout  io.Writer
	stderr  io.Writer
}

type command struct {
	summary string
	// flagSummary is the one-line flag listing shown in the top-level usage;
	// each run function owns the authoritative flag definitions.
	flagSummary string
	run         func(commandContext) int
}

// commands is the subcommand registry; new verbs register here.
var commands = map[string]command{
	"sync": {
		summary:     "link every store skill into each agent directory",
		flagSummary: "[--dry-run] [--copy] [--store DIR] [--agent-home DIR] [--allow-empty]",
		run:         runSync,
	},
	"init": {
		summary:     "write a starter agents.toml",
		flagSummary: "(no flags)",
		run:         runInit,
	},
	"status": {
		summary:     "report linked, missing, unmanaged and dangling skills",
		flagSummary: "[--store DIR] [--agent-home DIR]",
		run:         runStatus,
	},
	"clean": {
		summary:     "remove every sm-created link and copy",
		flagSummary: "[--agent NAME] [--agent-home DIR] [--orphans]",
		run:         runClean,
	},
	"probe": {
		summary:     "detect installed agents and add them to agents.toml",
		flagSummary: "[--yes] [--dry-run]",
		run:         runProbe,
	},
}

// Run dispatches args and returns the process exit code. -v/--verbose is
// global and accepted anywhere, so it is stripped before dispatch.
func Run(args []string, stdout, stderr io.Writer) int {
	verbose := false
	var remaining []string
	for _, arg := range args {
		if arg == "-v" || arg == "--verbose" {
			verbose = true
			continue
		}
		remaining = append(remaining, arg)
	}

	if len(remaining) == 0 {
		printUsage(stderr)
		return exitUsage
	}
	switch remaining[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return exitOK
	}
	selected, known := commands[remaining[0]]
	if !known {
		fmt.Fprintf(stderr, "sm: unknown command %q\n", remaining[0])
		printUsage(stderr)
		return exitUsage
	}
	return selected.run(commandContext{args: remaining[1:], verbose: verbose, stdout: stdout, stderr: stderr})
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: sm [-v] <command> [flags]")
	fmt.Fprintln(w, "       sm -h | --help | help")
	fmt.Fprintln(w, "commands:")
	for _, name := range sortedCommandNames() {
		fmt.Fprintf(w, "  %-8s %s\n", name, commands[name].summary)
		fmt.Fprintf(w, "  %-8s   flags: %s\n", "", commands[name].flagSummary)
	}
	fmt.Fprintln(w, "run 'sm <command> -h' for per-command flag help")
}

// parseFlags parses a subcommand's arguments. The second result is true when
// the caller must return the first immediately: flag.ErrHelp is a successful
// request for help (exit 0, usage on stdout), any other parse failure, a
// positional argument, or an explicitly empty flag value is a usage error
// (exit 2, diagnostics on stderr).
//
// No subcommand takes positional arguments, and the stdlib stops parsing at
// the first one, so "sm sync now --dry-run" would silently drop --dry-run and
// "sm clean a" would clean every agent; both are refused. An empty value (for
// example an unset shell variable) would otherwise read as "flag not given"
// and widen the scope to the defaults, such as the real agent directories.
func parseFlags(flags *flag.FlagSet, ctx commandContext) (int, bool) {
	flags.SetOutput(io.Discard)
	err := flags.Parse(ctx.args)
	if errors.Is(err, flag.ErrHelp) {
		flags.SetOutput(ctx.stdout)
		fmt.Fprintln(ctx.stdout, usageLine(flags))
		flags.PrintDefaults()
		return exitOK, true
	}
	if err == nil {
		err = argumentError(flags)
	}
	if err == nil {
		return exitOK, false
	}
	fmt.Fprintf(ctx.stderr, "sm %s: %v\n", flags.Name(), err)
	fmt.Fprintln(ctx.stderr, usageLine(flags))
	flags.PrintDefaults()
	return exitUsage, true
}

// argumentError reports a positional argument or an explicitly provided flag
// whose value is empty. flag.Visit sees only flags that were set, so an empty
// default is never mistaken for an empty value; a boolean flag's value is
// never empty.
func argumentError(flags *flag.FlagSet) error {
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	var emptyFlag string
	flags.Visit(func(set *flag.Flag) {
		if emptyFlag == "" && set.Value.String() == "" {
			emptyFlag = set.Name
		}
	})
	if emptyFlag != "" {
		return fmt.Errorf("--%s must not be empty", emptyFlag)
	}
	return nil
}

// usageLine omits the "[flags]" placeholder for a command that has none, so
// the header does not advertise flags that do not exist.
func usageLine(flags *flag.FlagSet) string {
	hasFlags := false
	flags.VisitAll(func(*flag.Flag) { hasFlags = true })
	if !hasFlags {
		return fmt.Sprintf("usage: sm %s", flags.Name())
	}
	return fmt.Sprintf("usage: sm %s [flags]", flags.Name())
}

// loadFailure formats an agents.toml load error. The init hint is only true
// when the file is absent: 'sm init' refuses to overwrite an existing file.
func loadFailure(err error) string {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Sprintf("sm: %v (run 'sm init' to create one)\n", err)
	}
	return fmt.Sprintf("sm: %v\n", err)
}

func runSync(ctx commandContext) int {
	flags := flag.NewFlagSet("sync", flag.ContinueOnError)
	dryRun := flags.Bool("dry-run", false, "report changes without making them")
	copyMode := flags.Bool("copy", false, "materialize real copied directories instead of symlinks (for sandboxed/container homes)")
	storeFlag := flags.String("store", "", "override the canonical skills store (default $XDG_DATA_HOME/skills)")
	agentHomeFlag := flags.String("agent-home", "", "re-root every agent's skills dir from $HOME into this directory")
	allowEmpty := flags.Bool("allow-empty", false, "let a store with no skills remove every entry sm recorded (refused by default)")
	if code, done := parseFlags(flags, ctx); done {
		return code
	}

	configPath, err := agents.DefaultConfigPath()
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm: %v\n", err)
		return exitError
	}
	cfg, err := agents.Load(configPath)
	if err != nil {
		fmt.Fprint(ctx.stderr, loadFailure(err))
		return exitError
	}
	storeDir, err := store.DefaultPath()
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm: %v\n", err)
		return exitError
	}
	statePath, err := state.DefaultPath()
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm: %v\n", err)
		return exitError
	}

	storeDir, cfg, warnings, err := applyOverrides(storeDir, cfg, *storeFlag, *agentHomeFlag)
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm: %v\n", err)
		return exitError
	}
	for _, warning := range warnings {
		fmt.Fprintf(ctx.stderr, "sm: warning: %s\n", warning)
	}

	if !*dryRun {
		if err := tightenStateDir(statePath); err != nil {
			fmt.Fprintf(ctx.stderr, "sm: %v\n", err)
			return exitError
		}
	}
	options := syncOptions{CopyMode: *copyMode, DryRun: *dryRun, Verbose: ctx.verbose, AllowEmpty: *allowEmpty}
	summary, err := doSync(cfg, storeDir, statePath, options, ctx.stderr)
	// A run whose only failures were isolated per-skill copy errors still
	// returns its summary, so what did happen is reported before the errors.
	if summary != "" {
		fmt.Fprintln(ctx.stdout, summary)
	}
	if err != nil {
		// Copy errors name files inside store skills, which can be
		// third-party content ingested by ms; they are printed terminal-safe.
		fmt.Fprintf(ctx.stderr, "sm: sync failed: %s\n", pathutil.PrintableLines(err.Error()))
		return exitError
	}
	return exitOK
}

// resolvedPaths bundles the defaults the read/remove commands share with sync.
type resolvedPaths struct {
	cfg       agents.Config
	storeDir  string
	statePath string
}

// resolvePaths loads the agent config and resolves the store and state
// locations, reporting any failure on stderr.
func resolvePaths(ctx commandContext) (resolvedPaths, bool) {
	configPath, err := agents.DefaultConfigPath()
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm: %v\n", err)
		return resolvedPaths{}, false
	}
	cfg, err := agents.Load(configPath)
	if err != nil {
		fmt.Fprint(ctx.stderr, loadFailure(err))
		return resolvedPaths{}, false
	}
	storeDir, err := store.DefaultPath()
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm: %v\n", err)
		return resolvedPaths{}, false
	}
	statePath, err := state.DefaultPath()
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm: %v\n", err)
		return resolvedPaths{}, false
	}
	return resolvedPaths{cfg: cfg, storeDir: storeDir, statePath: statePath}, true
}

func runInit(ctx commandContext) int {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	if code, done := parseFlags(flags, ctx); done {
		return code
	}
	configPath, err := agents.DefaultConfigPath()
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm: %v\n", err)
		return exitError
	}
	if err := agents.WriteStarter(configPath); err != nil {
		fmt.Fprintf(ctx.stderr, "sm: init failed: %v\n", err)
		return exitError
	}
	fmt.Fprintf(ctx.stdout, "wrote starter config to %s\n", configPath)
	return exitOK
}

func sortedCommandNames() []string {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
