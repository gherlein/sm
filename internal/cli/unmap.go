package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/gherlein/sm/internal/agents"
	"github.com/gherlein/sm/internal/pathutil"
	"github.com/gherlein/sm/internal/state"
)

const unmapUsage = "usage: sm unmap <target-dir>"

// runUnmap undoes `sm map` for one folder: it removes the entries sm placed
// there (identity-checked, as clean does, so hand-placed entries survive) and
// drops the folder's state record. The folder itself is left in place, since
// it may hold other files. It refuses a folder that is not a mapped target, so
// it can never strip a configured agent's directory.
func runUnmap(ctx commandContext) int {
	flags := flag.NewFlagSet("unmap", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	positionals, parseErr := parsePositionals(flags, ctx.args)
	if errors.Is(parseErr, flag.ErrHelp) {
		fmt.Fprintln(ctx.stdout, unmapUsage)
		return exitOK
	}
	if parseErr != nil || len(positionals) != 1 {
		fmt.Fprintln(ctx.stderr, unmapUsage)
		return exitUsage
	}

	removedCount, err := doUnmap(positionals[0], ctx.stderr)
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm unmap: %s\n", pathutil.PrintableLines(err.Error()))
		return exitError
	}
	fmt.Fprintf(ctx.stdout, "unmap: -%d\n", removedCount)
	return exitOK
}

func doUnmap(targetDir string, out io.Writer) (removedCount int, err error) {
	// Resolve the folder exactly as map does, so the state key matches.
	resolvedTarget, err := pathutil.Abs(targetDir)
	if err == nil {
		resolvedTarget, err = pathutil.ResolveExisting(resolvedTarget)
	}
	if err != nil {
		return 0, err
	}

	statePath, err := state.DefaultPath()
	if err != nil {
		return 0, err
	}
	if err := tightenStateDir(statePath); err != nil {
		return 0, err
	}
	unlock, err := state.Lock(statePath, stateLockHolder)
	if err != nil {
		return 0, err
	}
	defer func() {
		if unlockErr := unlock(); unlockErr != nil {
			err = errors.Join(err, unlockErr)
		}
	}()

	current, err := state.Load(statePath)
	if err != nil {
		return 0, err
	}
	target, recorded := current.Targets[resolvedTarget]
	if !recorded {
		return 0, fmt.Errorf("%s is not a mapped target (nothing recorded for it)", resolvedTarget)
	}
	if !target.Mapped {
		return 0, fmt.Errorf("%s is not a mapped target; it was placed by sm sync (use sm clean)", resolvedTarget)
	}

	agent := agents.Agent{Name: resolvedTarget, Dir: resolvedTarget}
	removedCount, err = cleanTarget(&current, agent, statePath, out)
	if err != nil {
		return removedCount, err
	}
	return removedCount, current.Save(statePath)
}

// mappedFolderGone reports whether a mapped target's folder no longer exists,
// which makes its record safe to drop: there is nothing on disk left to clean.
func mappedFolderGone(dir string) bool {
	_, err := os.Lstat(dir)
	return errors.Is(err, os.ErrNotExist)
}
