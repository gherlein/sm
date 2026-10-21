package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/gherlein/sm/internal/agents"
	"github.com/gherlein/sm/internal/probe"
)

// doProbe detects installed agents and appends the confirmed ones to the
// config. A missing config means "no agents yet", not a failure, because
// probe is the way a first config gets created.
func doProbe(cfgPath string, home string, onPath func(string) bool, confirm func(probe.Found) bool, out io.Writer) ([]string, error) {
	found, err := probe.Detect(agents.Registry(), home, onPath)
	if err != nil {
		return nil, err
	}

	existing, err := loadExistingAgents(cfgPath)
	if err != nil {
		return nil, err
	}

	added, err := probe.AddMissing(cfgPath, found, existing, confirm)
	if err != nil {
		return nil, err
	}
	if len(added) == 0 {
		fmt.Fprintln(out, "no new agents added")
		return added, nil
	}
	fmt.Fprintf(out, "added to %s: %s\n", cfgPath, strings.Join(added, ", "))
	return added, nil
}

// loadExistingAgents treats a missing config as no agents; a real read or
// parse failure propagates.
func loadExistingAgents(cfgPath string) ([]agents.Agent, error) {
	if _, err := os.Stat(cfgPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat %s: %w", cfgPath, err)
	}
	cfg, err := agents.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	return cfg.Agents, nil
}

// doProbeDryRun reports the agents probe would add without touching cfgPath.
// It has its own path so the output stays coherent instead of running the
// write path with a confirm that always declines.
func doProbeDryRun(cfgPath, home string, onPath func(string) bool, out io.Writer) ([]string, error) {
	found, err := probe.Detect(agents.Registry(), home, onPath)
	if err != nil {
		return nil, err
	}
	existing, err := loadExistingAgents(cfgPath)
	if err != nil {
		return nil, err
	}
	configured := make(map[string]bool, len(existing))
	for _, agent := range existing {
		configured[agent.Name] = true
	}
	var wouldAdd []string
	for _, candidate := range found {
		if configured[candidate.Name] {
			continue
		}
		fmt.Fprintf(out, "would add %s (%s)\n", candidate.Name, candidate.Dir)
		wouldAdd = append(wouldAdd, candidate.Name)
	}
	if len(wouldAdd) == 0 {
		fmt.Fprintln(out, "dry run: no new agents to add")
		return wouldAdd, nil
	}
	fmt.Fprintf(out, "dry run: %d agent(s) would be added; nothing written\n", len(wouldAdd))
	return wouldAdd, nil
}

func runProbe(ctx commandContext) int {
	flags := flag.NewFlagSet("probe", flag.ContinueOnError)
	assumeYes := flags.Bool("yes", false, "add every detected agent without prompting")
	dryRun := flags.Bool("dry-run", false, "report detected agents without writing")
	if code, done := parseFlags(flags, ctx); done {
		return code
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm: %v\n", err)
		return exitError
	}
	configPath, err := agents.DefaultConfigPath()
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm: %v\n", err)
		return exitError
	}

	stdin := bufio.NewReader(os.Stdin)
	confirm := func(candidate probe.Found) bool {
		fmt.Fprintf(ctx.stdout, "add %s (%s)? [y/N] ", candidate.Name, candidate.Dir)
		// EOF or a read error means no answer, which is the safe default of "no".
		answer, readErr := stdin.ReadString('\n')
		if readErr != nil && answer == "" {
			return false
		}
		return isYes(answer)
	}
	if *assumeYes {
		confirm = func(probe.Found) bool { return true }
	}
	if *dryRun {
		if _, err := doProbeDryRun(configPath, home, lookPathFound, ctx.stdout); err != nil {
			fmt.Fprintf(ctx.stderr, "sm: probe failed: %v\n", err)
			return exitError
		}
		return exitOK
	}

	if _, err := doProbe(configPath, home, lookPathFound, confirm, ctx.stdout); err != nil {
		fmt.Fprintf(ctx.stderr, "sm: probe failed: %v\n", err)
		return exitError
	}
	return exitOK
}

func lookPathFound(binary string) bool {
	_, err := exec.LookPath(binary)
	return err == nil
}

func isYes(answer string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(answer))
	return trimmed == "y" || trimmed == "yes"
}
