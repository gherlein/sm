package cli_test

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestRun_HelpFlagsExitZeroToStdout(t *testing.T) {
	for _, argument := range []string{"-h", "--help", "help"} {
		t.Run(argument, func(t *testing.T) {
			newEnv(t)
			r := run(argument)
			expectCode(t, r, exitOK)
			if r.stderr != "" {
				t.Fatalf("help must not write stderr\n%s", r)
			}
			for _, want := range []string{"usage", "sync", "init", "status", "clean", "probe", "--allow-empty", "--orphans", "--dry-run", "--yes", "--agent-home"} {
				if !strings.Contains(r.stdout, want) {
					t.Errorf("usage lacks %q\n%s", want, r)
				}
			}
		})
	}
}

func TestInit_HelpDoesNotWriteConfig(t *testing.T) {
	environment := newEnv(t)
	r := run("init", "-h")
	expectCode(t, r, exitOK)
	if !strings.Contains(r.stdout, "usage: sm init") {
		t.Fatalf("expected init usage on stdout\n%s", r)
	}
	if _, err := os.Stat(environment.configPath); !os.IsNotExist(err) {
		t.Fatalf("init -h wrote the config, stat err = %v", err)
	}
	if _, err := os.Stat(environment.configHome); !os.IsNotExist(err) {
		t.Fatalf("init -h created the config dir, stat err = %v", err)
	}
}

func TestInit_UnknownFlagExits2(t *testing.T) {
	environment := newEnv(t)
	r := run("init", "--bogus")
	expectCode(t, r, exitUsage)
	if !strings.Contains(r.stderr, "bogus") {
		t.Fatalf("error should name the flag\n%s", r)
	}
	if _, err := os.Stat(environment.configPath); !os.IsNotExist(err) {
		t.Fatalf("rejected init wrote the config, stat err = %v", err)
	}
}

func TestInit_StillWritesConfigWithoutFlags(t *testing.T) {
	environment := newEnv(t)
	expectCode(t, run("init"), exitOK)
	if _, err := os.Stat(environment.configPath); err != nil {
		t.Fatal(err)
	}
}

// helpFlagNames extracts the flag names PrintDefaults printed: those lines
// begin with two spaces and a single dash. Continuation lines of a usage
// string are indented further and never match.
func helpFlagNames(helpOutput string) map[string]bool {
	names := make(map[string]bool)
	for _, line := range strings.Split(helpOutput, "\n") {
		if !strings.HasPrefix(line, "  -") || strings.HasPrefix(line, "  --") {
			continue
		}
		fields := strings.Fields(line[len("  -"):])
		if len(fields) == 0 {
			continue
		}
		names[fields[0]] = true
	}
	return names
}

// summaryFlagNames extracts the --name tokens of a flagSummary line.
func summaryFlagNames(summary string) map[string]bool {
	names := make(map[string]bool)
	for _, token := range strings.FieldsFunc(summary, func(r rune) bool { return strings.ContainsRune(" []|", r) }) {
		if strings.HasPrefix(token, "--") {
			names[token[len("--"):]] = true
		}
	}
	return names
}

func sortedNames(names map[string]bool) []string {
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	return sorted
}

// diffFlagSets reports flags printed by -h but absent from the usage summary,
// and summary flags that -h does not define.
func diffFlagSets(printed, summarized []string) (missingFromSummary, staleInSummary []string) {
	printedSet := make(map[string]bool, len(printed))
	for _, name := range printed {
		printedSet[name] = true
	}
	summarizedSet := make(map[string]bool, len(summarized))
	for _, name := range summarized {
		summarizedSet[name] = true
	}
	for _, name := range printed {
		if !summarizedSet[name] {
			missingFromSummary = append(missingFromSummary, name)
		}
	}
	for _, name := range summarized {
		if !printedSet[name] {
			staleInSummary = append(staleInSummary, name)
		}
	}
	return missingFromSummary, staleInSummary
}

func TestDiffFlagSets(t *testing.T) {
	cases := []struct {
		name                   string
		printed, summarized    []string
		wantMissing, wantStale []string
	}{
		{"both empty", nil, nil, nil, nil},
		{"equal", []string{"a", "b"}, []string{"b", "a"}, nil, nil},
		{"flag missing from summary", []string{"a", "b"}, []string{"a"}, []string{"b"}, nil},
		{"stale summary flag", []string{"a"}, []string{"a", "gone"}, nil, []string{"gone"}},
		{"both directions", []string{"a", "new"}, []string{"a", "old"}, []string{"new"}, []string{"old"}},
		{"parser returned empty printed set", nil, []string{"a"}, nil, []string{"a"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			missing, stale := diffFlagSets(testCase.printed, testCase.summarized)
			if !reflect.DeepEqual(missing, testCase.wantMissing) || !reflect.DeepEqual(stale, testCase.wantStale) {
				t.Errorf("got missing %v stale %v, want missing %v stale %v", missing, stale, testCase.wantMissing, testCase.wantStale)
			}
		})
	}
}

func TestFlagParsersOnKnownInput(t *testing.T) {
	help := "usage: sm sync [flags]\n  -dry-run\n    \tdescription -not-a-flag\n  -store string\n  -\n  --double\n"
	if got, want := sortedNames(helpFlagNames(help)), []string{"dry-run", "store"}; !reflect.DeepEqual(got, want) {
		t.Errorf("helpFlagNames = %v, want %v", got, want)
	}
	summary := "           flags: [--dry-run] [--store DIR] [--yes|--no]"
	if got, want := sortedNames(summaryFlagNames(summary)), []string{"dry-run", "no", "store", "yes"}; !reflect.DeepEqual(got, want) {
		t.Errorf("summaryFlagNames = %v, want %v", got, want)
	}
}

func TestSubcommandHelpExitsZero(t *testing.T) {
	flagsByCommand := map[string][]string{
		"sync":   {"dry-run", "copy", "store", "agent-home", "allow-empty"},
		"init":   {},
		"status": {"store", "agent-home"},
		"clean":  {"agent", "agent-home", "orphans"},
		"probe":  {"yes", "dry-run"},
	}
	for command, wantFlags := range flagsByCommand {
		for _, helpFlag := range []string{"-h", "--help"} {
			t.Run(command+" "+helpFlag, func(t *testing.T) {
				newEnv(t)
				r := run(command, helpFlag)
				expectCode(t, r, exitOK)
				if !strings.HasPrefix(r.stdout, "usage: sm "+command) {
					t.Fatalf("expected command usage on stdout\n%s", r)
				}
				printed := helpFlagNames(r.stdout)
				for _, want := range wantFlags {
					if !printed[want] {
						t.Errorf("help lacks flag %q\n%s", want, r)
					}
				}
			})
		}
	}
}

func TestInit_HelpHeaderHasNoFlagsPlaceholder(t *testing.T) {
	newEnv(t)
	r := run("init", "-h")
	expectCode(t, r, exitOK)
	if first := strings.SplitN(r.stdout, "\n", 2)[0]; first != "usage: sm init" {
		t.Errorf("header = %q, want %q", first, "usage: sm init")
	}
}

// TestUsageSummaryMatchesPrintedFlags catches drift between the top-level
// usage text and each command's real flag definitions, in both directions.
func TestUsageSummaryMatchesPrintedFlags(t *testing.T) {
	top := run("-h")
	expectCode(t, top, exitOK)
	for _, command := range []string{"sync", "init", "status", "clean", "probe"} {
		t.Run(command, func(t *testing.T) {
			newEnv(t)
			r := run(command, "-h")
			expectCode(t, r, exitOK)
			printed := helpFlagNames(r.stdout)

			var summaryLine string
			lines := strings.Split(top.stdout, "\n")
			for index, line := range lines {
				if strings.HasPrefix(line, "  "+command+" ") && index+1 < len(lines) {
					summaryLine = lines[index+1]
				}
			}
			if !strings.Contains(summaryLine, "flags:") {
				t.Fatalf("no flags line found for %s in usage\n%s", command, top.stdout)
			}
			summarized := summaryFlagNames(summaryLine)

			missing, stale := diffFlagSets(sortedNames(printed), sortedNames(summarized))
			for _, name := range missing {
				t.Errorf("flag -%s is defined but missing from the usage summary: %s", name, summaryLine)
			}
			for _, name := range stale {
				t.Errorf("usage summary lists --%s but %s -h does not define it", name, command)
			}
		})
	}
}

func TestSync_InvalidConfigDoesNotSuggestInit(t *testing.T) {
	environment := newEnv(t)
	writeFile(t, environment.configPath, "this is [[ not toml ===")
	for _, command := range []string{"sync", "status", "clean"} {
		r := run(command)
		expectCode(t, r, exitError)
		if strings.Contains(r.stderr, "sm init") {
			t.Errorf("%s: invalid existing config must not suggest init\n%s", command, r)
		}
	}
}

func TestSync_MissingConfigSuggestsInit(t *testing.T) {
	newEnv(t)
	for _, command := range []string{"sync", "status", "clean"} {
		r := run(command)
		expectCode(t, r, exitError)
		if !strings.Contains(r.stderr, "run 'sm init' to create one") {
			t.Errorf("%s: missing config should suggest init\n%s", command, r)
		}
	}
}
