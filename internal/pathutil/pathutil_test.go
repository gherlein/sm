package pathutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"bare tilde", "~", home},
		{"tilde slash", "~/x/y", filepath.Join(home, "x", "y")},
		{"absolute untouched", "/a/b", "/a/b"},
		{"relative untouched", "a/b", "a/b"},
		{"env var untouched", "$HOME/x", "$HOME/x"},
		{"tilde mid path untouched", "a/~/b", "a/~/b"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ExpandTilde(testCase.input)
			if err != nil {
				t.Fatalf("ExpandTilde(%q): %v", testCase.input, err)
			}
			if got != testCase.want {
				t.Errorf("ExpandTilde(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

func TestExpandTilde_UserFormIsError(t *testing.T) {
	for _, input := range []string{"~root", "~bob/x"} {
		if _, err := ExpandTilde(input); err == nil {
			t.Errorf("ExpandTilde(%q): expected an error", input)
		}
	}
}

func TestAbs(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	cases := []struct {
		input string
		want  string
	}{
		{"~/a/../b", filepath.Join(home, "b")},
		{"rel/x", filepath.Join(workingDirectory, "rel", "x")},
		{"/a/b/../c/", "/a/c"},
		{"$HOME/x", filepath.Join(workingDirectory, "$HOME", "x")},
	}
	for _, testCase := range cases {
		got, err := Abs(testCase.input)
		if err != nil {
			t.Fatalf("Abs(%q): %v", testCase.input, err)
		}
		if got != testCase.want {
			t.Errorf("Abs(%q) = %q, want %q", testCase.input, got, testCase.want)
		}
	}
}

func TestAbs_UserFormIsError(t *testing.T) {
	if _, err := Abs("~root/x"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestResolveExisting_ThroughSymlinkWithMissingTail(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realDirectory, alias); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveExisting(filepath.Join(alias, "missing", "deeper"))
	if err != nil {
		t.Fatalf("ResolveExisting: %v", err)
	}
	if want := filepath.Join(realDirectory, "missing", "deeper"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveExisting_FullyExisting(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := ResolveExisting(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Errorf("got %q, want %q", got, root)
	}
}

func TestResolveExisting_NonNotExistErrorPropagates(t *testing.T) {
	root := t.TempDir()
	loopA := filepath.Join(root, "a")
	loopB := filepath.Join(root, "b")
	if err := os.Symlink(loopB, loopA); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(loopA, loopB); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveExisting(filepath.Join(loopA, "x")); err == nil {
		t.Fatal("expected a symlink-loop error to propagate")
	}
}

func TestResolveExisting_RejectsRelative(t *testing.T) {
	if _, err := ResolveExisting("rel/x"); err == nil {
		t.Fatal("expected an error for a relative path")
	}
}

func TestWithinAndOverlaps(t *testing.T) {
	cases := []struct {
		name         string
		base, target string
		within       bool
		overlaps     bool
	}{
		{"equal", "/a/b", "/a/b", true, true},
		{"inside", "/a", "/a/b/c", true, true},
		{"parent of base", "/a/b", "/a", false, true},
		{"sibling", "/a/b", "/a/c", false, false},
		{"prefix sibling", "/a/b", "/a/bc", false, false},
		{"dot dot prefixed name", "/a", "/a/..foo", true, true},
		{"root contains all", "/", "/x", true, true},
		{"trailing slash", "/a/b/", "/a/b/c", true, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := Within(testCase.base, testCase.target); got != testCase.within {
				t.Errorf("Within(%q, %q) = %v, want %v", testCase.base, testCase.target, got, testCase.within)
			}
			if got := Overlaps(testCase.base, testCase.target); got != testCase.overlaps {
				t.Errorf("Overlaps(%q, %q) = %v, want %v", testCase.base, testCase.target, got, testCase.overlaps)
			}
			if got := Overlaps(testCase.target, testCase.base); got != testCase.overlaps {
				t.Errorf("Overlaps symmetric (%q, %q) = %v, want %v", testCase.target, testCase.base, got, testCase.overlaps)
			}
		})
	}
}

func TestResolveExisting_AncestorIsAFilePropagatesError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveExisting(filepath.Join(file, "child")); err == nil {
		t.Fatal("expected ENOTDIR to propagate, not be treated as a missing path")
	}
}

func realTempRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	return root
}

func TestResolveExisting_DanglingSymlinkResolvesToMissingTarget(t *testing.T) {
	root := realTempRoot(t)
	missing := filepath.Join(root, "real", "missing")
	alias := filepath.Join(root, "alias")
	if err := os.Mkdir(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(missing, alias); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveExisting(alias)
	if err != nil {
		t.Fatalf("ResolveExisting: %v", err)
	}
	if got != missing {
		t.Errorf("got %q, want %q", got, missing)
	}
}

func TestResolveExisting_ChainOfDanglingSymlinks(t *testing.T) {
	root := realTempRoot(t)
	missing := filepath.Join(root, "final")
	middle := filepath.Join(root, "middle")
	first := filepath.Join(root, "first")
	if err := os.Symlink(missing, middle); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(middle, first); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveExisting(first)
	if err != nil {
		t.Fatalf("ResolveExisting: %v", err)
	}
	if got != missing {
		t.Errorf("got %q, want %q", got, missing)
	}
}

func TestResolveExisting_DanglingSymlinkLoopIsAnError(t *testing.T) {
	root := realTempRoot(t)
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	// A mutually referencing pair never settles; each link reads as dangling.
	if err := os.Symlink("second", first); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("first", second); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveExisting(first); err == nil {
		t.Fatal("expected a loop error")
	}
}

func TestResolveExisting_RelativeDanglingTarget(t *testing.T) {
	root := realTempRoot(t)
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "dir", "alias")
	if err := os.Symlink("../elsewhere/missing", alias); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveExisting(alias)
	if err != nil {
		t.Fatalf("ResolveExisting: %v", err)
	}
	if want := filepath.Join(root, "elsewhere", "missing"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveExisting_DanglingSymlinkWithRemainder(t *testing.T) {
	root := realTempRoot(t)
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Join(root, "target"), alias); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveExisting(filepath.Join(alias, "sub", "leaf"))
	if err != nil {
		t.Fatalf("ResolveExisting: %v", err)
	}
	if want := filepath.Join(root, "target", "sub", "leaf"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// danglingChain creates linkCount symlinks in dir where each points at the
// next and the last points at a missing target; it returns the first link and
// the missing target.
func danglingChain(t *testing.T, dir string, linkCount int) (first, missing string) {
	t.Helper()
	missing = filepath.Join(dir, "missing-target")
	for index := linkCount - 1; index >= 0; index-- {
		next := missing
		if index < linkCount-1 {
			next = filepath.Join(dir, fmt.Sprintf("link%03d", index+1))
		}
		if err := os.Symlink(next, filepath.Join(dir, fmt.Sprintf("link%03d", index))); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "link000"), missing
}

func TestResolveExisting_DanglingChainLongerThanHopLimitIsAnError(t *testing.T) {
	first, _ := danglingChain(t, realTempRoot(t), maxSymlinkHops+1)
	_, err := ResolveExisting(first)
	if err == nil {
		t.Fatal("expected an error for a chain longer than the hop limit")
	}
	if !strings.Contains(err.Error(), "too many levels of symbolic links") {
		t.Errorf("error %q does not name the hop limit", err)
	}
}

func TestResolveExisting_DanglingChainOfExactlyTheHopLimitResolves(t *testing.T) {
	first, missing := danglingChain(t, realTempRoot(t), maxSymlinkHops)
	got, err := ResolveExisting(first)
	if err != nil {
		t.Fatalf("ResolveExisting: %v", err)
	}
	if got != missing {
		t.Errorf("got %q, want %q", got, missing)
	}
}

func TestResolveExisting_DanglingRelativeTargetThroughSymlinkedDirectory(t *testing.T) {
	root := realTempRoot(t)
	realB := filepath.Join(root, "b", "c")
	if err := os.MkdirAll(realB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realB, filepath.Join(root, "a", "L")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../x", filepath.Join(realB, "alias")); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveExisting(filepath.Join(root, "a", "L", "alias"))
	if err != nil {
		t.Fatalf("ResolveExisting: %v", err)
	}
	if want := filepath.Join(root, "b", "x"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPrintable(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"ordinary path unchanged", "/home/user/skills/git-ops", "/home/user/skills/git-ops"},
		{"non-ASCII letters unchanged", "/data/café/日本", "/data/café/日本"},
		{"escape and bell", "evil\x1b]0;owned\x07", `evil\x1b]0;owned\x07`},
		{"carriage return and newline", "a\rb\nc", `a\x0db\x0ac`},
		{"C1 control as UTF-8", "a\u009bb", `a\x9bb`},
		{"stray byte", "a\x9bb", `a\x9bb`},
		{"bidirectional override", "a\u202eb", `a\u202eb`},
		{"delete", "a\x7fb", `a\x7fb`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := Printable(testCase.input); got != testCase.want {
				t.Fatalf("Printable(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

func TestPrintableLines(t *testing.T) {
	got := PrintableLines("first \x1b[2J\nsecond\x07\r\nthird")
	want := `first \x1b[2J` + "\n" + `second\x07\x0d` + "\n" + "third"
	if got != want {
		t.Fatalf("PrintableLines = %q, want %q", got, want)
	}
}
