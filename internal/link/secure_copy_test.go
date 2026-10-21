package link

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// oversizedMarkerPadding pushes a marker past the read cap so a marker that
// merely starts with the recorded path is not trusted.
const oversizedMarkerPadding = 8192

// requireNonRoot fails loudly when the test runs as root, because root ignores
// permission bits and ownership, which would make these tests pass vacuously.
func requireNonRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Fatal("this test needs a non-root user: root bypasses permission and ownership checks, so the assertion would be meaningless")
	}
}

// treeSnapshot records every entry under dir (type, mode bits, content or link
// target) so a test can prove a directory was not modified.
func treeSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			snapshot[rel] = "link:" + target
		case entry.IsDir():
			snapshot[rel] = "dir:" + info.Mode().String()
		default:
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot[rel] = "file:" + info.Mode().String() + ":" + string(content)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dir, err)
	}
	return snapshot
}

// contentSnapshot is treeSnapshot without mode bits, for comparing a copy
// (whose modes are deliberately masked) with its source.
func contentSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	for rel, value := range treeSnapshot(t, dir) {
		switch {
		case strings.HasPrefix(value, "dir:"):
			snapshot[rel] = "dir"
		case strings.HasPrefix(value, "file:"):
			parts := strings.SplitN(value, ":", 3)
			snapshot[rel] = "file:" + parts[2]
		default:
			snapshot[rel] = value
		}
	}
	return snapshot
}

func assertSameSnapshot(t *testing.T, label string, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: %d entries, want %d\n got: %v\nwant: %v", label, len(got), len(want), got, want)
	}
	for rel, value := range want {
		if got[rel] != value {
			t.Errorf("%s: %s = %q, want %q", label, rel, got[rel], value)
		}
	}
}

// assertNoScratchDirs proves no staging or set-aside directory was left behind.
func assertNoScratchDirs(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".sm-stage-") || strings.HasPrefix(entry.Name(), ".sm-old-") {
			t.Errorf("leftover scratch directory %s in %s", entry.Name(), dir)
		}
	}
}

func assertReported(t *testing.T, report CopyReport, fragment string) {
	t.Helper()
	for _, line := range report.Skipped {
		if strings.Contains(line, fragment) {
			return
		}
	}
	t.Errorf("CopyReport.Skipped does not mention %q: %v", fragment, report.Skipped)
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s must not exist, lstat err = %v", path, err)
	}
}

func TestCopyDir_StagingRaceCannotEscape(t *testing.T) {
	root := t.TempDir()
	src := makeStoreSkill(t, root, "alpha")
	parent := filepath.Join(root, "agent")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(parent, "alpha")
	planted := dst + ".sm-stage"
	if err := os.Symlink(outside, planted); err != nil {
		t.Fatal(err)
	}

	if _, err := CopyDir(src, identityOf(t, src), dst, CopyOptions{}); err != nil {
		t.Fatalf("CopyDir: %v", err)
	}

	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("CopyDir wrote outside the target through the planted link: %v", entries)
	}
	if got, err := os.ReadFile(filepath.Join(dst, "sub", "ref.txt")); err != nil || string(got) != "deep\n" {
		t.Errorf("copy incomplete: %q, %v", got, err)
	}
	if target, err := os.Readlink(planted); err != nil || target != outside {
		t.Errorf("planted entry was modified: %q, %v", target, err)
	}
	assertNoScratchDirs(t, parent)
}

func TestCopyDir_SymlinkedSourceRootResolved(t *testing.T) {
	for _, form := range []string{"absolute", "relative"} {
		t.Run(form, func(t *testing.T) {
			root := t.TempDir()
			real := makeStoreSkill(t, root, "real")
			linkDir := filepath.Join(root, "links")
			if err := os.MkdirAll(linkDir, 0o755); err != nil {
				t.Fatal(err)
			}
			linkTarget := real
			if form == "relative" {
				linkTarget = filepath.Join("..", "real")
			}
			linkPath := filepath.Join(linkDir, "alpha")
			if err := os.Symlink(linkTarget, linkPath); err != nil {
				t.Fatal(err)
			}
			before := treeSnapshot(t, real)

			dst := filepath.Join(root, "dst")
			if _, err := CopyDir(linkPath, identityOf(t, linkPath), dst, CopyOptions{}); err != nil {
				t.Fatalf("CopyDir: %v", err)
			}
			info, err := os.Lstat(dst)
			if err != nil {
				t.Fatal(err)
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				t.Fatalf("dst mode %v, want a real directory", info.Mode())
			}
			assertSameSnapshot(t, "copy", contentSnapshot(t, dst), contentSnapshot(t, real))

			agentDir := filepath.Join(root, "agent")
			if _, err := CreateCopy(agentDir, "alpha", linkPath, pinOf(t, linkPath)); err != nil {
				t.Fatalf("CreateCopy: %v", err)
			}
			marker, err := os.ReadFile(filepath.Join(agentDir, "alpha", copyMarker))
			if err != nil || string(marker) != linkPath+"\n" {
				t.Errorf("marker = %q, %v; want %q", marker, err, linkPath+"\n")
			}
			assertSameSnapshot(t, "real source after copies", treeSnapshot(t, real), before)
			assertAbsent(t, filepath.Join(real, copyMarker))
			assertNoScratchDirs(t, root)
			assertNoScratchDirs(t, agentDir)
		})
	}
}

func TestCopyDir_SymlinkPolicy(t *testing.T) {
	root := t.TempDir()
	src := makeStoreSkill(t, root, "alpha")
	outsideFile := filepath.Join(root, "outside.txt")
	writeTestFile(t, outsideFile, "secret")
	links := map[string]string{
		"inlink":     filepath.Join("sub", "ref.txt"),
		"sub/up":     filepath.Join("..", "SKILL.md"),
		"self":       ".",
		"abs":        outsideFile,
		"escape":     filepath.Join("..", "outside.txt"),
		"sub/escape": filepath.Join("..", "..", "outside.txt"),
		// Lexically "self/.." is the skill root, but the kernel resolves self
		// first (to the root) and then "..", which is the parent.
		// filepath.Join would clean this to ".", so it is spelled literally.
		"tricky": "self/..",
	}
	for rel, target := range links {
		if err := os.Symlink(target, filepath.Join(src, rel)); err != nil {
			t.Fatal(err)
		}
	}
	dst := filepath.Join(root, "dst")

	report, err := CopyDir(src, identityOf(t, src), dst, CopyOptions{})
	if err != nil {
		t.Fatalf("CopyDir: %v", err)
	}
	for _, rel := range []string{"inlink", "sub/up", "self"} {
		got, err := os.Readlink(filepath.Join(dst, rel))
		if err != nil || got != links[rel] {
			t.Errorf("%s: readlink = %q, %v; want %q", rel, got, err, links[rel])
		}
	}
	for _, rel := range []string{"abs", "escape", "sub/escape", "tricky"} {
		assertAbsent(t, filepath.Join(dst, rel))
		assertReported(t, report, filepath.Join(src, rel))
	}
	if len(report.Skipped) != 4 {
		t.Errorf("Skipped = %v, want exactly 4 entries", report.Skipped)
	}
}

func TestCopyDir_NeverCopiesRootMarker(t *testing.T) {
	root := t.TempDir()
	src := makeStoreSkill(t, root, "alpha")
	writeTestFile(t, filepath.Join(src, copyMarker), "/forged/source\n")
	writeTestFile(t, filepath.Join(src, stageSentinel), "forged sentinel\n")
	writeTestFile(t, filepath.Join(src, "sub", copyMarker), "nested is ordinary content\n")
	dst := filepath.Join(root, "dst")

	report, err := CopyDir(src, identityOf(t, src), dst, CopyOptions{})
	if err != nil {
		t.Fatalf("CopyDir: %v", err)
	}
	assertAbsent(t, filepath.Join(dst, copyMarker))
	assertAbsent(t, filepath.Join(dst, stageSentinel))
	assertReported(t, report, filepath.Join(src, copyMarker))
	assertReported(t, report, filepath.Join(src, stageSentinel))
	if got, err := os.ReadFile(filepath.Join(dst, "sub", copyMarker)); err != nil || string(got) != "nested is ordinary content\n" {
		t.Errorf("nested marker-named file = %q, %v", got, err)
	}

	agentDir := filepath.Join(root, "agent")
	if _, err := CreateCopy(agentDir, "alpha", src, pinOf(t, src)); err != nil {
		t.Fatalf("CreateCopy: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(agentDir, "alpha", copyMarker))
	if err != nil || string(marker) != src+"\n" {
		t.Errorf("CreateCopy marker = %q, %v; want the store path, not the forged one", marker, err)
	}
}

func TestCopyDir_StripsGroupOtherWriteAndSetuid(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	writeTestFile(t, filepath.Join(src, "setuid"), "a")
	writeTestFile(t, filepath.Join(src, "open"), "b")
	writeTestFile(t, filepath.Join(src, "setgid-dir", "inner"), "c")
	modes := map[string]os.FileMode{
		"setuid":     0o777 | os.ModeSetuid,
		"open":       0o777,
		"setgid-dir": 0o775 | os.ModeSetgid,
	}
	for rel, mode := range modes {
		if err := os.Chmod(filepath.Join(src, rel), mode); err != nil {
			t.Fatal(err)
		}
	}
	dst := filepath.Join(root, "dst")

	if _, err := CopyDir(src, identityOf(t, src), dst, CopyOptions{}); err != nil {
		t.Fatalf("CopyDir: %v", err)
	}
	for rel := range modes {
		info, err := os.Lstat(filepath.Join(dst, rel))
		if err != nil {
			t.Fatal(err)
		}
		mode := info.Mode()
		if mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			t.Errorf("%s: special bits carried: %v", rel, mode)
		}
		if mode.Perm()&0o022 != 0 {
			t.Errorf("%s: group/other write carried: %v", rel, mode)
		}
		if mode.Perm()&0o700 != 0o700 {
			t.Errorf("%s: owner bits lost: %v", rel, mode)
		}
	}
}

// caseInsensitive reports whether dir's filesystem folds case.
func caseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "caseprobe")
	writeTestFile(t, probe, "x")
	_, err := os.Lstat(filepath.Join(dir, "CASEPROBE"))
	if removeErr := os.Remove(probe); removeErr != nil {
		t.Fatal(removeErr)
	}
	if err == nil {
		return true
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return false
}

func TestCopyDir_CaseCollisionFailsLoudly(t *testing.T) {
	root := t.TempDir()

	// Whatever the filesystem, a write must never go through an entry that
	// already exists in the stage: O_EXCL / Mkdir refuse instead.
	t.Run("existing entry is never written through", func(t *testing.T) {
		dirSource := filepath.Join(root, "excl-dir-src")
		writeTestFile(t, filepath.Join(dirSource, "dir", "x.txt"), "payload")
		fileSource := filepath.Join(root, "excl-file-src")
		writeTestFile(t, filepath.Join(fileSource, "file.txt"), "payload")
		stage := filepath.Join(root, "excl-stage")
		if err := os.MkdirAll(filepath.Join(stage, "victim"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(stage, "victim-file"), "original")
		for _, pair := range [][2]string{{"dir", "victim"}, {"file.txt", "victim-file"}} {
			if err := os.Symlink(pair[1], filepath.Join(stage, pair[0])); err != nil {
				t.Fatal(err)
			}
		}
		if err := copyIntoStage(t, dirSource, stage); err == nil {
			t.Error("copying a directory over an existing link must fail")
		}
		if err := copyIntoStage(t, fileSource, stage); err == nil {
			t.Error("copying a file over an existing link must fail")
		}
		victimEntries, err := os.ReadDir(filepath.Join(stage, "victim"))
		if err != nil || len(victimEntries) != 0 {
			t.Errorf("write went through the dir link: %v, %v", victimEntries, err)
		}
		if got, err := os.ReadFile(filepath.Join(stage, "victim-file")); err != nil || string(got) != "original" {
			t.Errorf("write went through the file link: %q, %v", got, err)
		}
	})

	t.Run("names differing only in case", func(t *testing.T) {
		src := filepath.Join(root, "case-src")
		if err := os.MkdirAll(filepath.Join(src, "bar"), 0o755); err != nil {
			t.Fatal(err)
		}
		if caseInsensitive(t, src) {
			// The colliding source cannot exist on this filesystem, so prove the
			// copy refuses "foo/x.txt" when "FOO" already exists in the stage.
			writeTestFile(t, filepath.Join(src, "foo", "x.txt"), "payload")
			stage := filepath.Join(root, "case-stage")
			if err := os.MkdirAll(filepath.Join(stage, "bar"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("bar", filepath.Join(stage, "FOO")); err != nil {
				t.Fatal(err)
			}
			if err := copyIntoStage(t, src, stage); err == nil {
				t.Fatal("case-colliding write must fail")
			}
			assertAbsent(t, filepath.Join(stage, "bar", "x.txt"))
			return
		}
		if err := os.Symlink("bar", filepath.Join(src, "Foo")); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(src, "foo", "x.txt"), "payload")
		dst := filepath.Join(root, "case-dst")
		if _, err := CopyDir(src, identityOf(t, src), dst, CopyOptions{}); err != nil {
			t.Fatalf("CopyDir on a case-sensitive filesystem: %v", err)
		}
		if got, err := os.Readlink(filepath.Join(dst, "Foo")); err != nil || got != "bar" {
			t.Errorf("Foo = %q, %v", got, err)
		}
		if got, err := os.ReadFile(filepath.Join(dst, "foo", "x.txt")); err != nil || string(got) != "payload" {
			t.Errorf("foo/x.txt = %q, %v", got, err)
		}
		assertAbsent(t, filepath.Join(dst, "bar", "x.txt"))
	})
}

// copyIntoStage runs the tree copy of src into an existing stage directory,
// which lets a test pre-populate the stage with a colliding entry.
func copyIntoStage(t *testing.T, src, stage string) error {
	t.Helper()
	stageRoot, err := os.OpenRoot(stage)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := stageRoot.Close(); err != nil {
			t.Error(err)
		}
	}()
	srcRoot, err := os.OpenRoot(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := srcRoot.Close(); err != nil {
			t.Error(err)
		}
	}()
	var report CopyReport
	return copyTree(srcRoot, src, stageRoot, copyPolicy{}, &report)
}

func TestCheckTargetDir(t *testing.T) {
	requireNonRoot(t)
	root := t.TempDir()

	t.Run("creates a missing directory", func(t *testing.T) {
		dir := filepath.Join(root, "new", "nested")
		if err := CheckTargetDir(dir); err != nil {
			t.Fatalf("CheckTargetDir: %v", err)
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("dir not created: %v", err)
		}
	})
	t.Run("world-writable refused", func(t *testing.T) {
		dir := filepath.Join(root, "open")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		err := CheckTargetDir(dir)
		if err == nil || !strings.Contains(err.Error(), "world-writable") || !strings.Contains(err.Error(), dir) {
			t.Errorf("err = %v, want a world-writable refusal naming %s", err, dir)
		}
	})
	t.Run("group-writable allowed", func(t *testing.T) {
		dir := filepath.Join(root, "group")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o775); err != nil {
			t.Fatal(err)
		}
		if err := CheckTargetDir(dir); err != nil {
			t.Errorf("CheckTargetDir: %v", err)
		}
	})
	t.Run("symlinked directory allowed", func(t *testing.T) {
		real := filepath.Join(root, "realdir")
		if err := os.Mkdir(real, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "linkdir")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		if err := CheckTargetDir(link); err != nil {
			t.Errorf("CheckTargetDir: %v", err)
		}
	})
	t.Run("not owned refused", func(t *testing.T) {
		info, err := os.Stat("/")
		if err != nil {
			t.Fatal(err)
		}
		if owned, err := ownedByCurrentUser(info); err != nil || owned {
			t.Fatalf("test precondition: / must be owned by another user (owned=%v, err=%v)", owned, err)
		}
		err = CheckTargetDir("/")
		if err == nil || !strings.Contains(err.Error(), "not owned") {
			t.Errorf("err = %v, want a not-owned refusal", err)
		}
	})
	t.Run("regular file refused", func(t *testing.T) {
		file := filepath.Join(root, "plain")
		writeTestFile(t, file, "x")
		err := CheckTargetDir(file)
		if err == nil {
			t.Error("CheckTargetDir on a regular file must fail")
		}
	})
}

// makeUnreadableSkill writes a source tree whose copy fails part-way: one file
// cannot be opened.
func makeUnreadableSkill(t *testing.T, root, name string) string {
	t.Helper()
	requireNonRoot(t)
	dir := makeStoreSkill(t, root, name)
	locked := filepath.Join(dir, "sub", "zzz-locked.txt")
	writeTestFile(t, locked, "unreadable")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(locked, 0o644); err != nil {
			t.Error(err)
		}
	})
	return dir
}

func makeOldDir(t *testing.T, dst string) map[string]string {
	t.Helper()
	writeTestFile(t, filepath.Join(dst, "old.txt"), "old content")
	writeTestFile(t, filepath.Join(dst, "nested", "keep.txt"), "keep")
	return treeSnapshot(t, dst)
}

func TestReplaceDir_SwapsWhenSuccessful(t *testing.T) {
	root := t.TempDir()
	src := makeStoreSkill(t, root, "alpha")
	parent := filepath.Join(root, "store")
	dst := filepath.Join(parent, "alpha")
	makeOldDir(t, dst)

	if _, err := ReplaceDir(src, identityOf(t, src), dst, CopyOptions{}); err != nil {
		t.Fatalf("ReplaceDir: %v", err)
	}
	assertSameSnapshot(t, "replaced", contentSnapshot(t, dst), contentSnapshot(t, src))
	assertNoScratchDirs(t, parent)

	fresh := filepath.Join(parent, "fresh")
	if _, err := ReplaceDir(src, identityOf(t, src), fresh, CopyOptions{}); err != nil {
		t.Fatalf("ReplaceDir into an absent dst: %v", err)
	}
	assertSameSnapshot(t, "fresh", contentSnapshot(t, fresh), contentSnapshot(t, src))
	assertNoScratchDirs(t, parent)
}

func TestReplaceDir_RestoresOldOnFailure(t *testing.T) {
	t.Run("copy fails mid-way", func(t *testing.T) {
		root := t.TempDir()
		src := makeUnreadableSkill(t, root, "alpha")
		parent := filepath.Join(root, "store")
		dst := filepath.Join(parent, "alpha")
		before := makeOldDir(t, dst)

		if _, err := ReplaceDir(src, identityOf(t, src), dst, CopyOptions{}); err == nil {
			t.Fatal("ReplaceDir with an unreadable source file must fail")
		}
		assertSameSnapshot(t, "old dir", treeSnapshot(t, dst), before)
		assertNoScratchDirs(t, parent)
	})
	t.Run("placing the stage fails", func(t *testing.T) {
		root := t.TempDir()
		src := makeStoreSkill(t, root, "alpha")
		parent := filepath.Join(root, "store")
		dst := filepath.Join(parent, "alpha")
		before := makeOldDir(t, dst)

		original := renameStageIntoPlace
		renameStageIntoPlace = func(stage, target string) error {
			return errors.New("injected rename failure")
		}
		t.Cleanup(func() { renameStageIntoPlace = original })

		_, err := ReplaceDir(src, identityOf(t, src), dst, CopyOptions{})
		if err == nil || !strings.Contains(err.Error(), "injected rename failure") {
			t.Fatalf("err = %v, want the injected failure", err)
		}
		assertSameSnapshot(t, "old dir", treeSnapshot(t, dst), before)
		assertNoScratchDirs(t, parent)
	})
}

func TestRefreshCopy_RequiresOwnership(t *testing.T) {
	root := t.TempDir()
	storeA := makeStoreSkill(t, filepath.Join(root, "a"), "alpha")
	storeB := makeStoreSkill(t, filepath.Join(root, "b"), "alpha")
	writeTestFile(t, filepath.Join(storeB, "new.txt"), "new")
	agentDir := filepath.Join(root, "agent")

	hand := filepath.Join(agentDir, "hand")
	handBefore := makeOldDir(t, hand)
	if _, err := RefreshCopy(agentDir, "hand", storeB, pinOf(t, storeB), storeA); err == nil {
		t.Error("RefreshCopy of a marker-less directory must fail")
	}
	assertSameSnapshot(t, "hand", treeSnapshot(t, hand), handBefore)

	if _, err := CreateCopy(agentDir, "alpha", storeA, pinOf(t, storeA)); err != nil {
		t.Fatal(err)
	}
	ownedBefore := treeSnapshot(t, filepath.Join(agentDir, "alpha"))
	if _, err := RefreshCopy(agentDir, "alpha", storeB, pinOf(t, storeB), "/some/other/store"); err == nil {
		t.Error("RefreshCopy with a non-matching recorded source must fail")
	}
	assertSameSnapshot(t, "mismatched", treeSnapshot(t, filepath.Join(agentDir, "alpha")), ownedBefore)

	if _, err := RefreshCopy(agentDir, "alpha", storeB, pinOf(t, storeB), storeA); err != nil {
		t.Fatalf("RefreshCopy: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(agentDir, "alpha", "new.txt")); err != nil || string(got) != "new" {
		t.Errorf("refreshed content = %q, %v", got, err)
	}
	marker, err := os.ReadFile(filepath.Join(agentDir, "alpha", copyMarker))
	if err != nil || string(marker) != storeB+"\n" {
		t.Errorf("marker = %q, %v; want %q", marker, err, storeB+"\n")
	}
	owned, err := IsOwnedCopy(agentDir, "alpha", storeB)
	if err != nil || !owned {
		t.Errorf("IsOwnedCopy after refresh = %v, %v", owned, err)
	}
	assertNoScratchDirs(t, agentDir)
}

func TestRefreshCopy_KeepsOldIfCopyFails(t *testing.T) {
	root := t.TempDir()
	storeA := makeStoreSkill(t, filepath.Join(root, "a"), "alpha")
	storeB := makeUnreadableSkill(t, filepath.Join(root, "b"), "alpha")
	agentDir := filepath.Join(root, "agent")
	if _, err := CreateCopy(agentDir, "alpha", storeA, pinOf(t, storeA)); err != nil {
		t.Fatal(err)
	}
	before := treeSnapshot(t, filepath.Join(agentDir, "alpha"))

	if _, err := RefreshCopy(agentDir, "alpha", storeB, pinOf(t, storeB), storeA); err == nil {
		t.Fatal("RefreshCopy from an unreadable source must fail")
	}
	assertSameSnapshot(t, "old copy", treeSnapshot(t, filepath.Join(agentDir, "alpha")), before)
	owned, err := IsOwnedCopy(agentDir, "alpha", storeA)
	if err != nil || !owned {
		t.Errorf("old copy lost ownership: %v, %v", owned, err)
	}
	assertNoScratchDirs(t, agentDir)
}

func TestIsOwnedCopy_MarkerMustMatchRecordedSource(t *testing.T) {
	root := t.TempDir()
	store := makeStoreSkill(t, root, "alpha")
	agentDir := filepath.Join(root, "agent")
	if _, err := CreateCopy(agentDir, "alpha", store, pinOf(t, store)); err != nil {
		t.Fatal(err)
	}

	check := func(name, recorded string, want bool) {
		t.Helper()
		owned, err := IsOwnedCopy(agentDir, name, recorded)
		if err != nil {
			t.Fatalf("IsOwnedCopy(%s, %s): %v", name, recorded, err)
		}
		if owned != want {
			t.Errorf("IsOwnedCopy(%s, %s) = %v, want %v", name, recorded, owned, want)
		}
	}
	check("alpha", store, true)
	check("alpha", "/some/other/store", false)
	check("alpha", store+"/", false)

	// A previous release wrote exactly storePath + "\n"; it must still count.
	legacy := filepath.Join(agentDir, "legacy")
	writeTestFile(t, filepath.Join(legacy, copyMarker), store+"\n")
	check("legacy", store, true)

	linkMarker := filepath.Join(agentDir, "linkmarker")
	if err := os.MkdirAll(linkMarker, 0o755); err != nil {
		t.Fatal(err)
	}
	genuine := filepath.Join(root, "genuine-marker")
	writeTestFile(t, genuine, store+"\n")
	if err := os.Symlink(genuine, filepath.Join(linkMarker, copyMarker)); err != nil {
		t.Fatal(err)
	}
	check("linkmarker", store, false)

	dirMarker := filepath.Join(agentDir, "dirmarker")
	if err := os.MkdirAll(filepath.Join(dirMarker, copyMarker), 0o755); err != nil {
		t.Fatal(err)
	}
	check("dirmarker", store, false)

	oversized := filepath.Join(agentDir, "oversized")
	writeTestFile(t, filepath.Join(oversized, copyMarker), store+strings.Repeat(" ", oversizedMarkerPadding))
	check("oversized", store, false)

	if _, err := IsOwnedCopy(agentDir, "alpha", ""); err == nil {
		t.Error("an empty recorded source must be rejected, not matched")
	}
}

func TestRemoveCopy_RefusesSpoofedMarker(t *testing.T) {
	root := t.TempDir()
	store := makeStoreSkill(t, root, "alpha")
	agentDir := filepath.Join(root, "agent")

	spoofed := filepath.Join(agentDir, "alpha")
	writeTestFile(t, filepath.Join(spoofed, copyMarker), "/attacker/chosen\n")
	writeTestFile(t, filepath.Join(spoofed, "precious.txt"), "user data")
	before := treeSnapshot(t, spoofed)

	removed, leftovers, err := RemoveCopy(agentDir, "alpha", store)
	if err != nil {
		t.Fatalf("RemoveCopy: %v", err)
	}
	if len(leftovers) != 0 {
		t.Errorf("unexpected leftovers: %v", leftovers)
	}
	if removed {
		t.Error("RemoveCopy removed a directory whose marker names another source")
	}
	assertSameSnapshot(t, "spoofed", treeSnapshot(t, spoofed), before)

	legacy := filepath.Join(agentDir, "legacy")
	writeTestFile(t, filepath.Join(legacy, copyMarker), store+"\n")
	removed, leftovers, err = RemoveCopy(agentDir, "legacy", store)
	if err != nil || !removed || len(leftovers) != 0 {
		t.Fatalf("RemoveCopy(legacy) = %v, %v; want true", removed, err)
	}
	assertAbsent(t, legacy)
	assertNoScratchDirs(t, agentDir)
}

// makeOrphanStage builds what a crash between staging and rename leaves
// behind: a random .sm-stage-* directory carrying the sentinel.
func makeOrphanStage(t *testing.T, targetDir string) string {
	t.Helper()
	stage, err := os.MkdirTemp(targetDir, stagePattern)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(stage, stageSentinel), "")
	writeTestFile(t, filepath.Join(stage, "SKILL.md"), "partial\n")
	return stage
}

func TestSweepStages_OnlyOwnedSentinelStages(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "agent")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	survivors := []string{
		filepath.Join(targetDir, "x.sm-stage"),
		filepath.Join(targetDir, ".sm-stage-foo"),
		filepath.Join(targetDir, ".sm-old-abc"),
		filepath.Join(targetDir, ".sm-stage-dirsentinel"),
	}
	writeTestFile(t, filepath.Join(survivors[0], stageSentinel), "")
	writeTestFile(t, filepath.Join(survivors[1], "SKILL.md"), "user data\n")
	writeTestFile(t, filepath.Join(survivors[2], stageSentinel), "")
	if err := os.MkdirAll(filepath.Join(survivors[3], stageSentinel), 0o755); err != nil {
		t.Fatal(err)
	}
	linkedStage := filepath.Join(root, "elsewhere")
	writeTestFile(t, filepath.Join(linkedStage, stageSentinel), "")
	linkEntry := filepath.Join(targetDir, ".sm-stage-link")
	if err := os.Symlink(linkedStage, linkEntry); err != nil {
		t.Fatal(err)
	}
	fileEntry := filepath.Join(targetDir, ".sm-stage-file")
	writeTestFile(t, fileEntry, "a file")
	survivors = append(survivors, linkEntry, fileEntry, linkedStage)
	orphan := makeOrphanStage(t, targetDir)

	removed, skipped, err := SweepStages(targetDir)
	if err != nil {
		t.Fatalf("SweepStages: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if len(skipped) != 0 {
		t.Errorf("unexpected skipped stages: %v", skipped)
	}
	assertAbsent(t, orphan)
	for _, path := range survivors {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("%s was removed: %v", path, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(linkedStage, stageSentinel)); err != nil {
		t.Errorf("sweep followed a symlinked stage: %v", err)
	}
}

func TestOwnedByCurrentUser(t *testing.T) {
	requireNonRoot(t)
	mine, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if owned, err := ownedByCurrentUser(mine); err != nil || !owned {
		t.Errorf("own temp dir: owned=%v err=%v", owned, err)
	}
	rootDir, err := os.Stat("/")
	if err != nil {
		t.Fatal(err)
	}
	if owned, err := ownedByCurrentUser(rootDir); err != nil || owned {
		t.Errorf("/: owned=%v err=%v, want false", owned, err)
	}
}

// lockSubdirectory puts a read-only subdirectory holding a file inside dir, so
// a later RemoveAll of the tree fails with a permission error. The cleanup
// restores write permission everywhere under unlockRoot, wherever the locked
// directory has been moved to by then.
func lockSubdirectory(t *testing.T, dir, unlockRoot string) {
	t.Helper()
	requireNonRoot(t)
	locked := filepath.Join(dir, "locked")
	writeTestFile(t, filepath.Join(locked, "pinned.txt"), "cannot be unlinked")
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		err := filepath.WalkDir(unlockRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return os.Chmod(path, 0o755)
			}
			return nil
		})
		if err != nil {
			t.Error(err)
		}
	})
}

// singleAsideDir returns the one .sm-old-* directory in dir.
func singleAsideDir(t *testing.T, dir string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".sm-old-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("want exactly one .sm-old-* directory in %s, got %v", dir, matches)
	}
	return matches[0]
}

func assertLeftoverReported(t *testing.T, leftovers []string, asideDir string) {
	t.Helper()
	if len(leftovers) != 1 || !strings.Contains(leftovers[0], asideDir) {
		t.Errorf("leftovers = %v, want one line naming %s", leftovers, asideDir)
	}
}

func TestRefreshCopy_AsideCleanupFailureStillSucceedsAndReportsLeftover(t *testing.T) {
	root := t.TempDir()
	storeA := makeStoreSkill(t, filepath.Join(root, "a"), "alpha")
	storeB := makeStoreSkill(t, filepath.Join(root, "b"), "alpha")
	writeTestFile(t, filepath.Join(storeB, "new.txt"), "new")
	agentDir := filepath.Join(root, "agent")
	if _, err := CreateCopy(agentDir, "alpha", storeA, pinOf(t, storeA)); err != nil {
		t.Fatal(err)
	}
	lockSubdirectory(t, filepath.Join(agentDir, "alpha"), agentDir)

	report, err := RefreshCopy(agentDir, "alpha", storeB, pinOf(t, storeB), storeA)
	if err != nil {
		t.Fatalf("RefreshCopy must succeed once the new copy is in place: %v", err)
	}
	owned, err := IsOwnedCopy(agentDir, "alpha", storeB)
	if err != nil || !owned {
		t.Errorf("new copy not owned for the new source: %v, %v", owned, err)
	}
	if got, err := os.ReadFile(filepath.Join(agentDir, "alpha", "new.txt")); err != nil || string(got) != "new" {
		t.Errorf("new content = %q, %v", got, err)
	}
	assertLeftoverReported(t, report.Leftovers, singleAsideDir(t, agentDir))
}

func TestReplaceDir_AsideCleanupFailureStillSucceedsAndReportsLeftover(t *testing.T) {
	root := t.TempDir()
	src := makeStoreSkill(t, root, "alpha")
	parent := filepath.Join(root, "store")
	dst := filepath.Join(parent, "alpha")
	makeOldDir(t, dst)
	lockSubdirectory(t, dst, parent)

	report, err := ReplaceDir(src, identityOf(t, src), dst, CopyOptions{})
	if err != nil {
		t.Fatalf("ReplaceDir must succeed once the new tree is in place: %v", err)
	}
	assertSameSnapshot(t, "replaced", contentSnapshot(t, dst), contentSnapshot(t, src))
	assertLeftoverReported(t, report.Leftovers, singleAsideDir(t, parent))
}

func TestRemoveCopy_AsideCleanupFailureStillSucceedsAndReportsLeftover(t *testing.T) {
	root := t.TempDir()
	store := makeStoreSkill(t, root, "alpha")
	agentDir := filepath.Join(root, "agent")
	if _, err := CreateCopy(agentDir, "alpha", store, pinOf(t, store)); err != nil {
		t.Fatal(err)
	}
	lockSubdirectory(t, filepath.Join(agentDir, "alpha"), agentDir)

	removed, leftovers, err := RemoveCopy(agentDir, "alpha", store)
	if err != nil || !removed {
		t.Fatalf("RemoveCopy = %v, %v; want removed once the entry left its name", removed, err)
	}
	assertAbsent(t, filepath.Join(agentDir, "alpha"))
	assertLeftoverReported(t, leftovers, singleAsideDir(t, agentDir))
}

func TestSweepStages_VanishedAndUnreadableStageDoNotAbort(t *testing.T) {
	requireNonRoot(t)
	targetDir := t.TempDir()

	stage, reason, err := orphanedStage(filepath.Join(targetDir, ".sm-stage-vanished"))
	if err != nil || stage != nil || reason != "" {
		t.Errorf("vanished entry = %v, %q, %v; want nil, \"\", nil", stage, reason, err)
	}

	unreadable := makeOrphanStage(t, targetDir)
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(unreadable, 0o700); err != nil {
			t.Error(err)
		}
	})
	orphan := makeOrphanStage(t, targetDir)

	removed, skipped, err := SweepStages(targetDir)
	if err != nil {
		t.Fatalf("SweepStages must not abort on an unreadable stage: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	assertAbsent(t, orphan)
	if _, err := os.Lstat(unreadable); err != nil {
		t.Errorf("unreadable stage was removed: %v", err)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], unreadable) {
		t.Errorf("skipped = %v, want one line naming %s", skipped, unreadable)
	}
}

func TestCopyDir_SkipsReservedNamesCaseInsensitively(t *testing.T) {
	root := t.TempDir()
	src := makeStoreSkill(t, root, "alpha")
	upperMarker := filepath.Join(src, ".SM-COPY")
	mixedSentinel := filepath.Join(src, ".Sm-Stage-Sentinel")
	writeTestFile(t, upperMarker, "/forged\n")
	writeTestFile(t, mixedSentinel, "forged\n")
	dst := filepath.Join(root, "dst")

	report, err := CopyDir(src, identityOf(t, src), dst, CopyOptions{})
	if err != nil {
		t.Fatalf("CopyDir: %v", err)
	}
	assertAbsent(t, filepath.Join(dst, ".SM-COPY"))
	assertAbsent(t, filepath.Join(dst, ".Sm-Stage-Sentinel"))
	assertReported(t, report, upperMarker)
	assertReported(t, report, mixedSentinel)

	agentDir := filepath.Join(root, "agent")
	if _, err := CreateCopy(agentDir, "alpha", src, pinOf(t, src)); err != nil {
		t.Fatalf("CreateCopy: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(agentDir, "alpha", copyMarker))
	if err != nil || string(marker) != src+"\n" {
		t.Errorf("marker = %q, %v; want %q", marker, err, src+"\n")
	}
}

// Without root no directory owned by another user can be created where sm
// could create a stage, so the not-owned branch is proven on the predicate
// newPrivateDir applies, using "/" as a genuinely foreign-owned directory.
func TestNewPrivateDir_RequiresOwnership(t *testing.T) {
	requireNonRoot(t)
	dir, err := newPrivateDir(t.TempDir(), stagePattern)
	if err != nil {
		t.Fatalf("newPrivateDir in an owned directory: %v", err)
	}
	if err := requireOwnedDirectory(dir.path, dir.info); err != nil {
		t.Errorf("own stage refused: %v", err)
	}
	rootInfo, err := os.Lstat("/")
	if err != nil {
		t.Fatal(err)
	}
	err = requireOwnedDirectory("/", rootInfo)
	if err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Errorf("foreign directory: err = %v, want a not-owned refusal", err)
	}
	file := filepath.Join(t.TempDir(), "plain")
	writeTestFile(t, file, "x")
	fileInfo, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := requireOwnedDirectory(file, fileInfo); err == nil {
		t.Error("a regular file must be refused as a private directory")
	}
}
