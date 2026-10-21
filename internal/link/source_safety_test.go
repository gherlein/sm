package link

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gherlein/sm/internal/skillmeta"
)

// assertTerminalSafe fails when text could drive a terminal: it must be valid
// UTF-8 (a stray byte such as 0x9b is a C1 control on some terminals) and
// free of control characters.
func assertTerminalSafe(t *testing.T, text string) {
	t.Helper()
	if !utf8.ValidString(text) {
		t.Fatalf("text is not valid UTF-8: %q", text)
	}
	for _, character := range text {
		if unicode.IsControl(character) {
			t.Fatalf("text carries raw control character %U: %q", character, text)
		}
	}
}

// assertNoStages fails when dir holds any staging directory.
func assertNoStages(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".sm-stage-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("staging directories left in %s: %v", dir, matches)
	}
}

func assertOverlapRefusal(t *testing.T, err error, paths ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an overlap refusal")
	}
	if !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("error %q does not say the paths overlap", err)
	}
	for _, path := range paths {
		if !strings.Contains(err.Error(), path) {
			t.Fatalf("error %q does not name %s", err, path)
		}
	}
}

func TestCopyFunctions_RefuseSourceOverlappingDestination(t *testing.T) {
	t.Run("ReplaceDir onto its own source", func(t *testing.T) {
		root := t.TempDir()
		src := makeStoreSkill(t, root, "alpha")
		before := treeSnapshot(t, src)
		err := finishWithin(t, "ReplaceDir", func() error {
			_, err := testReplaceDir(t, src, src)
			return err
		})
		assertOverlapRefusal(t, err, src)
		assertSameSnapshot(t, "source", treeSnapshot(t, src), before)
		assertNoScratchDirs(t, root)
	})
	t.Run("CopyDir into its own source", func(t *testing.T) {
		root := t.TempDir()
		src := makeStoreSkill(t, root, "alpha")
		dst := filepath.Join(src, "sub", "copy")
		err := finishWithin(t, "CopyDir", func() error {
			_, err := testCopyDir(t, src, dst)
			return err
		})
		assertOverlapRefusal(t, err, src, dst)
		assertAbsent(t, dst)
		assertNoStages(t, filepath.Join(src, "sub"))
	})
	t.Run("CreateCopy of a store skill linked to the agent dir", func(t *testing.T) {
		root := t.TempDir()
		agentDir := makeStoreSkill(t, root, "foo")
		storeLink := filepath.Join(root, "store", "foo")
		if err := os.MkdirAll(filepath.Dir(storeLink), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(agentDir, storeLink); err != nil {
			t.Fatal(err)
		}
		err := finishWithin(t, "CreateCopy", func() error {
			_, err := testCreateCopy(t, agentDir, "foo", storeLink)
			return err
		})
		assertOverlapRefusal(t, err, agentDir)
		assertAbsent(t, filepath.Join(agentDir, "foo"))
		assertNoStages(t, agentDir)
	})
	t.Run("RefreshCopy from a store skill linked to a parent of the agent dir", func(t *testing.T) {
		root := t.TempDir()
		storeA := makeStoreSkill(t, filepath.Join(root, "a"), "alpha")
		agentParent := filepath.Join(root, "home")
		agentDir := filepath.Join(agentParent, "skills")
		if _, err := testCreateCopy(t, agentDir, "alpha", storeA); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(agentParent, "SKILL.md"), "---\nname: alpha\n---\n")
		storeLink := filepath.Join(root, "store", "alpha")
		if err := os.MkdirAll(filepath.Dir(storeLink), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(agentParent, storeLink); err != nil {
			t.Fatal(err)
		}
		before := treeSnapshot(t, filepath.Join(agentDir, "alpha"))
		err := finishWithin(t, "RefreshCopy", func() error {
			_, err := testRefreshCopy(t, agentDir, "alpha", storeLink, storeA)
			return err
		})
		assertOverlapRefusal(t, err, agentParent)
		assertSameSnapshot(t, "previous copy", treeSnapshot(t, filepath.Join(agentDir, "alpha")), before)
		assertNoScratchDirs(t, agentDir)
	})
}

// makeSkillWithLinkedManifest writes a store skill whose SKILL.md is an
// absolute symlink to a manifest kept outside the skill, and returns the
// skill directory and the manifest's bytes.
func makeSkillWithLinkedManifest(t *testing.T, root, name string) (string, []byte) {
	t.Helper()
	dir := makeStoreSkill(t, root, name)
	manifest := filepath.Join(root, "manifests", name+".md")
	content := []byte("---\nname: " + name + "\n---\nlinked manifest body\n")
	writeTestFile(t, manifest, string(content))
	skillFile := filepath.Join(dir, "SKILL.md")
	if err := os.Remove(skillFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(manifest, skillFile); err != nil {
		t.Fatal(err)
	}
	return dir, content
}

func assertRegularSkillFile(t *testing.T, dir string, want []byte) {
	t.Helper()
	skillFile := filepath.Join(dir, "SKILL.md")
	info, err := os.Lstat(skillFile)
	if err != nil {
		t.Fatalf("copy has no SKILL.md: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("copied SKILL.md mode = %v, want a regular file", info.Mode())
	}
	got, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("copied SKILL.md = %q, want %q", got, want)
	}
}

func TestCreateCopy_DereferencesSymlinkedSkillFile(t *testing.T) {
	root := t.TempDir()
	store, manifest := makeSkillWithLinkedManifest(t, root, "alpha")
	agentDir := filepath.Join(root, "agent")

	report, err := testCreateCopy(t, agentDir, "alpha", store)
	if err != nil {
		t.Fatalf("CreateCopy: %v", err)
	}
	if len(report.Skipped) != 0 {
		t.Fatalf("nothing should be skipped: %v", report.Skipped)
	}
	assertRegularSkillFile(t, filepath.Join(agentDir, "alpha"), manifest)
}

func TestRefreshCopy_DereferencesSymlinkedSkillFile(t *testing.T) {
	root := t.TempDir()
	storeA := makeStoreSkill(t, filepath.Join(root, "a"), "alpha")
	storeB, manifest := makeSkillWithLinkedManifest(t, filepath.Join(root, "b"), "alpha")
	agentDir := filepath.Join(root, "agent")
	if _, err := testCreateCopy(t, agentDir, "alpha", storeA); err != nil {
		t.Fatal(err)
	}
	if _, err := testRefreshCopy(t, agentDir, "alpha", storeB, storeA); err != nil {
		t.Fatalf("RefreshCopy: %v", err)
	}
	assertRegularSkillFile(t, filepath.Join(agentDir, "alpha"), manifest)
}

func TestCopyReport_HasNoRawControlCharacters(t *testing.T) {
	root := t.TempDir()
	src := makeStoreSkill(t, root, "alpha")
	hostileName := "evil\x1b[2J\x07\r" + "name"
	hostileTarget := "/tmp/\x1b]0;owned\x07\x9b31m"
	if err := os.Symlink(hostileTarget, filepath.Join(src, hostileName)); err != nil {
		t.Fatal(err)
	}

	report, err := testCopyDir(t, src, filepath.Join(root, "dst"))
	if err != nil {
		t.Fatalf("CopyDir: %v", err)
	}
	if len(report.Skipped) != 1 {
		t.Fatalf("want the absolute link reported once, got %v", report.Skipped)
	}
	line := report.Skipped[0]
	assertTerminalSafe(t, line)
	if !strings.Contains(line, `\x1b`) {
		t.Fatalf("report line %q does not show the escaped control character", line)
	}
}

// Every copy function opens the source once and requires it to be the
// directory the caller validated: a store skill replaced by a link to another
// directory in between (a private key directory, say) is refused, and so is a
// call without an identity.
func TestCopyFunctions_RefuseSourceChangedSinceValidated(t *testing.T) {
	root := t.TempDir()
	src := makeStoreSkill(t, root, "alpha")
	validated := identityOf(t, src)
	validatedManifest := identityOf(t, filepath.Join(src, "SKILL.md"))
	secret := filepath.Join(root, "secret")
	writeTestFile(t, filepath.Join(secret, "id_ed25519"), "private key")
	writeTestFile(t, filepath.Join(secret, "SKILL.md"), "---\nname: alpha\n---\n")
	if err := os.Rename(src, filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, src); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(root, "agent")
	recorded := makeStoreSkill(t, filepath.Join(root, "recorded"), "alpha")
	if _, err := testCreateCopy(t, agentDir, "alpha", recorded); err != nil {
		t.Fatal(err)
	}
	before := treeSnapshot(t, filepath.Join(agentDir, "alpha"))
	newDst := filepath.Join(root, "dst")

	calls := map[string]func(identity fs.FileInfo) error{
		"CopyDir": func(identity fs.FileInfo) error {
			_, err := CopyDir(src, identity, newDst, CopyOptions{})
			return err
		},
		"ReplaceDir": func(identity fs.FileInfo) error {
			_, err := ReplaceDir(src, identity, newDst, CopyOptions{})
			return err
		},
		"CreateCopy": func(identity fs.FileInfo) error {
			_, err := CreateCopy(agentDir, "beta", src, StorePin{Dir: identity, Manifest: validatedManifest})
			return err
		},
		"RefreshCopy": func(identity fs.FileInfo) error {
			_, err := RefreshCopy(agentDir, "alpha", src, StorePin{Dir: identity, Manifest: validatedManifest}, recorded)
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call(validated)
			if err == nil || !strings.Contains(err.Error(), "source changed since it was validated: "+src) {
				t.Fatalf("err = %v, want a source-changed refusal naming %s", err, src)
			}
			if err := call(nil); err == nil || !strings.Contains(err.Error(), "identity is required") {
				t.Fatalf("nil identity: err = %v, want a refusal", err)
			}
			assertAbsent(t, newDst)
			assertAbsent(t, filepath.Join(agentDir, "beta"))
			assertSameSnapshot(t, "existing copy", treeSnapshot(t, filepath.Join(agentDir, "alpha")), before)
			assertNoScratchDirs(t, agentDir)
			assertNoScratchDirs(t, root)
		})
	}
}

func TestCreateCopy_DereferencesInSkillSkillFileLink(t *testing.T) {
	root := t.TempDir()
	store := makeStoreSkill(t, root, "alpha")
	content := []byte("---\nname: alpha\n---\nfrom docs\n")
	writeTestFile(t, filepath.Join(store, "docs", "manifest.md"), string(content))
	skillFile := filepath.Join(store, "SKILL.md")
	if err := os.Remove(skillFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("docs", "manifest.md"), skillFile); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(root, "agent")

	if _, err := testCreateCopy(t, agentDir, "alpha", store); err != nil {
		t.Fatalf("CreateCopy: %v", err)
	}
	assertRegularSkillFile(t, filepath.Join(agentDir, "alpha"), content)
}

func TestCreateCopy_RefusesOversizedLinkedSkillFile(t *testing.T) {
	root := t.TempDir()
	store, _ := makeSkillWithLinkedManifest(t, root, "alpha")
	manifest, err := os.Readlink(filepath.Join(store, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, manifest, strings.Repeat("x", skillmeta.MaxSkillFileBytes+1))
	agentDir := filepath.Join(root, "agent")

	if _, err := testCreateCopy(t, agentDir, "alpha", store); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v, want a size-limit refusal", err)
	}
	assertAbsent(t, filepath.Join(agentDir, "alpha"))
	assertNoScratchDirs(t, agentDir)
}

// A sweep that cannot inspect a possible stage reports it by name; the name
// is whatever another process put in the agent directory, so the line must
// be terminal-safe.
func TestSweepStages_SkipLinesAreTerminalSafe(t *testing.T) {
	requireNonRoot(t)
	targetDir := t.TempDir()
	hostile := filepath.Join(targetDir, ".sm-stage-\x1b[2J\x07")
	if err := os.Mkdir(hostile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hostile, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(hostile, 0o755); err != nil {
			t.Error(err)
		}
	})

	removed, skipped, err := SweepStages(targetDir)
	if err != nil || removed != 0 {
		t.Fatalf("SweepStages = %d, %v", removed, err)
	}
	if len(skipped) != 1 {
		t.Fatalf("skipped = %q, want one line", skipped)
	}
	assertTerminalSafe(t, skipped[0])
	if !strings.Contains(skipped[0], `\x1b`) {
		t.Fatalf("skip line %q does not show the escaped control character", skipped[0])
	}
}

// The target-directory refusals say how to fix them.
func TestCheckTargetDir_RefusalsCarryHints(t *testing.T) {
	requireNonRoot(t)
	open := worldWritableDir(t)
	err := CheckTargetDir(open)
	var unsafe *UnsafeDirError
	if !errors.As(err, &unsafe) || unsafe.Dir != open {
		t.Fatalf("err = %v, want an *UnsafeDirError for %s", err, open)
	}
	want := "refusing target dir " + open + ": world-writable (fix with: chmod o-w '" + open + "')"
	if err.Error() != want {
		t.Fatalf("err = %q, want %q", err, want)
	}
	// The hint is pasted into a shell, so a path with spaces or quotes must
	// come out as one correctly quoted word.
	awkward := filepath.Join(worldWritableDir(t), "it's a dir")
	if err := os.Mkdir(awkward, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(awkward, 0o777); err != nil {
		t.Fatal(err)
	}
	quoted := `'` + strings.ReplaceAll(awkward, `'`, `'\''`) + `'`
	if err := CheckTargetDir(awkward); err == nil || !strings.HasSuffix(err.Error(), "(fix with: chmod o-w "+quoted+")") {
		t.Fatalf("err = %v, want the hint to quote %s as %s", err, awkward, quoted)
	}
	err = CheckTargetDir("/")
	want = "refusing target dir /: not owned by the current user (the directory must be owned by you)"
	if !errors.As(err, &unsafe) || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

// The dereferenced manifest is opened by path, so whatever sits at its target
// now must be the file validation read: a target replaced by a link to a
// different file (a private key, say) is refused by both copy functions and
// nothing is placed; the unchanged manifest copies (see the dereference tests).
func TestAgentCopies_RefuseManifestChangedSinceValidated(t *testing.T) {
	root := t.TempDir()
	store, _ := makeSkillWithLinkedManifest(t, root, "alpha")
	pin := pinOf(t, store)
	manifest, err := os.Readlink(filepath.Join(store, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret", "id_ed25519")
	writeTestFile(t, secret, "private key")
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, manifest); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(root, "agent")
	recorded := makeStoreSkill(t, filepath.Join(root, "recorded"), "alpha")
	if _, err := testCreateCopy(t, agentDir, "alpha", recorded); err != nil {
		t.Fatal(err)
	}
	before := treeSnapshot(t, filepath.Join(agentDir, "alpha"))
	want := "SKILL.md changed since it was validated: " + filepath.Join(store, "SKILL.md")

	if _, err := CreateCopy(agentDir, "beta", store, pin); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("CreateCopy err = %v, want %q", err, want)
	}
	assertAbsent(t, filepath.Join(agentDir, "beta"))
	if _, err := RefreshCopy(agentDir, "alpha", store, pin, recorded); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("RefreshCopy err = %v, want %q", err, want)
	}
	assertSameSnapshot(t, "existing copy", treeSnapshot(t, filepath.Join(agentDir, "alpha")), before)
	assertNoScratchDirs(t, agentDir)
}

// SameFile alone trusts a recycled inode; the pinned manifest's size and
// modification time must also be unchanged. Both are edited in place here
// (same inode) so only those checks can catch it.
func TestAgentCopies_RefuseManifestEditedInPlace(t *testing.T) {
	for _, edit := range []string{"size", "mtime"} {
		t.Run(edit, func(t *testing.T) {
			root := t.TempDir()
			store, _ := makeSkillWithLinkedManifest(t, root, "alpha")
			pin := pinOf(t, store)
			manifest, err := os.Readlink(filepath.Join(store, "SKILL.md"))
			if err != nil {
				t.Fatal(err)
			}
			switch edit {
			case "size":
				file, err := os.OpenFile(manifest, os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.WriteString("appended\n"); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				// Writing moved the modification time; putting it back
				// leaves the size as the only difference.
				pinned := pin.Manifest.ModTime()
				if err := os.Chtimes(manifest, pinned, pinned); err != nil {
					t.Fatal(err)
				}
			case "mtime":
				later := pin.Manifest.ModTime().Add(time.Hour)
				if err := os.Chtimes(manifest, later, later); err != nil {
					t.Fatal(err)
				}
			}
			edited := identityOf(t, manifest)
			if !os.SameFile(edited, pin.Manifest) {
				t.Fatal("test precondition: the manifest must keep its inode")
			}
			sizeChanged := edited.Size() != pin.Manifest.Size()
			timeChanged := !edited.ModTime().Equal(pin.Manifest.ModTime())
			if sizeChanged == timeChanged || sizeChanged != (edit == "size") {
				t.Fatalf("test precondition: exactly the %s must differ (size %v, mtime %v)", edit, sizeChanged, timeChanged)
			}
			agentDir := filepath.Join(root, "agent")
			want := "SKILL.md changed since it was validated"
			if _, err := CreateCopy(agentDir, "alpha", store, pin); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("CreateCopy err = %v, want %q", err, want)
			}
			assertAbsent(t, filepath.Join(agentDir, "alpha"))

			// Re-validated, the same manifest copies with its new content.
			current, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := CreateCopy(agentDir, "alpha", store, pinOf(t, store)); err != nil {
				t.Fatalf("CreateCopy with a fresh pin: %v", err)
			}
			assertRegularSkillFile(t, filepath.Join(agentDir, "alpha"), current)
		})
	}
}
