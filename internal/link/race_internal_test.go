package link

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every test here performs, through a test seam, the swap a concurrent writer
// would race to make between a check and the action that relies on it. Each
// one fails if the corresponding re-check is removed from the code.

const (
	recordedLinkTarget = "/store/alpha"
	userLinkTarget     = "/user/own/alpha"
)

func TestRemoveIfTarget_EntrySwappedBeforeMoveAsideIsRestored(t *testing.T) {
	targetDir := t.TempDir()
	linkPath := filepath.Join(targetDir, "alpha")
	if err := os.Symlink(recordedLinkTarget, linkPath); err != nil {
		t.Fatal(err)
	}
	userLink := filepath.Join(t.TempDir(), "user-link")
	if err := os.Symlink(userLinkTarget, userLink); err != nil {
		t.Fatal(err)
	}
	replaceSeam(t, &beforeMoveAside, swapOnce(t, userLink))

	result, err := RemoveIfTarget(targetDir, "alpha", recordedLinkTarget)
	if err != nil {
		t.Fatalf("RemoveIfTarget: %v", err)
	}
	if result != NotOurs {
		t.Fatalf("result = %v, want NotOurs for an entry swapped in before the move", result)
	}
	if got, err := os.Readlink(linkPath); err != nil || got != userLinkTarget {
		t.Fatalf("user link not restored: %q, %v", got, err)
	}
	assertNoScratchDirs(t, targetDir)
}

func TestRemoveCopy_EntrySwappedBeforeMoveAsideIsRestored(t *testing.T) {
	root := t.TempDir()
	store := makeStoreSkill(t, root, "alpha")
	agentDir := filepath.Join(root, "agent")
	if _, err := testCreateCopy(t, agentDir, "alpha", store); err != nil {
		t.Fatal(err)
	}
	userDir, before := makeUserDir(t)
	replaceSeam(t, &beforeMoveAside, swapOnce(t, userDir))

	removed, leftovers, err := RemoveCopy(agentDir, "alpha", store)
	if err == nil || removed {
		t.Fatalf("RemoveCopy = %v, %v, %v; want an error for an entry swapped in before the move", removed, leftovers, err)
	}
	assertSameSnapshot(t, "restored user dir", treeSnapshot(t, filepath.Join(agentDir, "alpha")), before)
	assertNoScratchDirs(t, agentDir)
}

func TestRefreshCopy_EntrySwappedBeforeMoveAsideIsRestored(t *testing.T) {
	root := t.TempDir()
	storeA := makeStoreSkill(t, filepath.Join(root, "a"), "alpha")
	storeB := makeStoreSkill(t, filepath.Join(root, "b"), "alpha")
	agentDir := filepath.Join(root, "agent")
	if _, err := testCreateCopy(t, agentDir, "alpha", storeA); err != nil {
		t.Fatal(err)
	}
	userDir, before := makeUserDir(t)
	replaceSeam(t, &beforeMoveAside, swapOnce(t, userDir))

	if _, err := testRefreshCopy(t, agentDir, "alpha", storeB, storeA); err == nil {
		t.Fatal("RefreshCopy must fail for an entry swapped in before the move")
	}
	assertSameSnapshot(t, "restored user dir", treeSnapshot(t, filepath.Join(agentDir, "alpha")), before)
	assertNoScratchDirs(t, agentDir)
}

func TestStageAndSwap_StageSwappedAfterBuildIsNeitherPlacedNorDeleted(t *testing.T) {
	root := t.TempDir()
	src := makeStoreSkill(t, root, "alpha")
	parent := filepath.Join(root, "dest")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(parent, "alpha")
	userDir, before := makeUserDir(t)
	var stagePath string
	swap := swapOnce(t, userDir)
	replaceSeam(t, &afterStageBuilt, func(path string) {
		stagePath = path
		swap(path)
	})

	if _, err := testCopyDir(t, src, dst); err == nil {
		t.Fatal("CopyDir must fail when the stage was swapped before the rename")
	}
	assertAbsent(t, dst)
	assertSameSnapshot(t, "user dir at the stage name", treeSnapshot(t, stagePath), before)
}

func TestStageAndSwap_StageSwappedBeforeOpenIsNeverWritten(t *testing.T) {
	root := t.TempDir()
	src := makeStoreSkill(t, root, "alpha")
	parent := filepath.Join(root, "dest")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(parent, "alpha")
	userDir, before := makeUserDir(t)
	var stagePath string
	swap := swapOnce(t, userDir)
	replaceSeam(t, &beforeStageOpen, func(path string) {
		stagePath = path
		swap(path)
	})

	if _, err := testCopyDir(t, src, dst); err == nil {
		t.Fatal("CopyDir must fail when the stage was swapped before it was opened")
	}
	assertAbsent(t, dst)
	assertSameSnapshot(t, "user dir at the stage name", treeSnapshot(t, stagePath), before)
}

func TestIsOwnedCopy_EntrySwappedBeforeOpenIsNotOwned(t *testing.T) {
	root := t.TempDir()
	store := makeStoreSkill(t, root, "alpha")
	agentDir := filepath.Join(root, "agent")
	if err := os.MkdirAll(filepath.Join(agentDir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	otherAgent := filepath.Join(root, "other")
	if _, err := testCreateCopy(t, otherAgent, "alpha", store); err != nil {
		t.Fatal(err)
	}
	replaceSeam(t, &beforeOwnedCopyOpen, swapOnce(t, filepath.Join(otherAgent, "alpha")))

	owned, err := IsOwnedCopy(agentDir, "alpha", store)
	if err != nil {
		t.Fatalf("IsOwnedCopy: %v", err)
	}
	if owned {
		t.Fatal("a directory swapped in after inspection must not be judged by its marker")
	}
}

func TestPrivateDirRemoveAll_SwappedInDirectoryIsNotDeleted(t *testing.T) {
	parent := t.TempDir()
	private, err := newPrivateDir(parent, asidePattern)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(private.path, "held", "file.txt"), "sm content")
	userDir, before := makeUserDir(t)
	replaceSeam(t, &beforePrivateDirDelete, swapOnce(t, userDir))

	if err := private.removeAll(); err == nil {
		t.Error("removeAll must report that its directory was replaced")
	}
	assertSameSnapshot(t, "user dir at the private dir's name", treeSnapshot(t, private.path), before)
}

func TestRemoveCopy_AsideSwappedBeforeDeleteKeepsUserDirectory(t *testing.T) {
	root := t.TempDir()
	store := makeStoreSkill(t, root, "alpha")
	agentDir := filepath.Join(root, "agent")
	if _, err := testCreateCopy(t, agentDir, "alpha", store); err != nil {
		t.Fatal(err)
	}
	userDir, before := makeUserDir(t)
	var asidePath string
	swap := swapOnce(t, userDir)
	replaceSeam(t, &beforePrivateDirDelete, func(path string) {
		asidePath = path
		swap(path)
	})

	removed, leftovers, err := RemoveCopy(agentDir, "alpha", store)
	if err != nil || !removed {
		t.Fatalf("RemoveCopy = %v, %v; the copy left its name, so it counts as removed", removed, err)
	}
	assertAbsent(t, filepath.Join(agentDir, "alpha"))
	assertLeftoverReported(t, leftovers, asidePath)
	assertSameSnapshot(t, "user dir at the aside name", treeSnapshot(t, asidePath), before)
}

// worldWritableDir returns a fresh directory any user could add entries to.
func worldWritableDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRemovals_RefuseUnsafeTargetDir(t *testing.T) {
	t.Run("RemoveIfTarget", func(t *testing.T) {
		dir := worldWritableDir(t)
		linkPath := filepath.Join(dir, "alpha")
		if err := os.Symlink(recordedLinkTarget, linkPath); err != nil {
			t.Fatal(err)
		}
		if _, err := RemoveIfTarget(dir, "alpha", recordedLinkTarget); err == nil || !strings.Contains(err.Error(), "world-writable") {
			t.Fatalf("err = %v, want a world-writable refusal", err)
		}
		if got, err := os.Readlink(linkPath); err != nil || got != recordedLinkTarget {
			t.Fatalf("link changed: %q, %v", got, err)
		}
	})
	t.Run("RemoveCopy", func(t *testing.T) {
		root := t.TempDir()
		store := makeStoreSkill(t, root, "alpha")
		agentDir := filepath.Join(root, "agent")
		if _, err := testCreateCopy(t, agentDir, "alpha", store); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(agentDir, 0o777); err != nil {
			t.Fatal(err)
		}
		if _, _, err := RemoveCopy(agentDir, "alpha", store); err == nil || !strings.Contains(err.Error(), "world-writable") {
			t.Fatalf("err = %v, want a world-writable refusal", err)
		}
		if _, err := os.Stat(filepath.Join(agentDir, "alpha", copyMarker)); err != nil {
			t.Fatalf("copy touched: %v", err)
		}
	})
	t.Run("SweepStages", func(t *testing.T) {
		dir := worldWritableDir(t)
		stage := makeOrphanStage(t, dir)
		if _, _, err := SweepStages(dir); err == nil || !strings.Contains(err.Error(), "world-writable") {
			t.Fatalf("err = %v, want a world-writable refusal", err)
		}
		if _, err := os.Stat(stage); err != nil {
			t.Fatalf("stage removed from an unsafe dir: %v", err)
		}
	})
}

func TestRemovals_MissingTargetDirIsNotCreated(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if result, err := RemoveIfTarget(missing, "alpha", recordedLinkTarget); err != nil || result != Absent {
		t.Fatalf("RemoveIfTarget = %v, %v; want Absent", result, err)
	}
	if removed, _, err := RemoveCopy(missing, "alpha", "/store/alpha"); err != nil || removed {
		t.Fatalf("RemoveCopy = %v, %v; want not removed", removed, err)
	}
	if removed, skipped, err := SweepStages(missing); err != nil || removed != 0 || len(skipped) != 0 {
		t.Fatalf("SweepStages = %d, %v, %v", removed, skipped, err)
	}
	assertAbsent(t, missing)
}
