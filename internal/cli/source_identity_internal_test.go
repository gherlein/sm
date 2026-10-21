package cli

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gherlein/sm/internal/link"
	"github.com/gherlein/sm/internal/state"
	"github.com/gherlein/sm/internal/store"
)

// identityOf is the identity store enumeration records for path.
func identityOf(t *testing.T, path string) fs.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info
}

// pinOf is what store enumeration records for the skill at path.
func pinOf(t *testing.T, path string) link.StorePin {
	t.Helper()
	return link.StorePin{Dir: identityOf(t, path), Manifest: identityOf(t, filepath.Join(path, "SKILL.md"))}
}

// A store skill replaced by a symlink to another directory after the store
// was enumerated and validated must not be copied: the copy would carry
// whatever the link points at (a private key directory, say) into the agent.
func TestDoSync_CopyRefusesSkillSwappedAfterEnumeration(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.MkdirAll(secret, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secret, "id_ed25519"), []byte("private key"), 0o600); err != nil {
		t.Fatal(err)
	}
	graveyard := t.TempDir()
	original := afterEnumerate
	t.Cleanup(func() { afterEnumerate = original })
	afterEnumerate = func(listing store.Listing) {
		skillDir := filepath.Join(fixture.storeDir, "alpha")
		if err := os.Rename(skillDir, filepath.Join(graveyard, "alpha")); err != nil {
			t.Errorf("seam: %v", err)
			return
		}
		if err := os.Symlink(secret, skillDir); err != nil {
			t.Errorf("seam: %v", err)
		}
	}

	var log bytes.Buffer
	_, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log)
	if err == nil || !strings.Contains(err.Error(), "source changed since it was validated") {
		t.Fatalf("doSync err = %v, want a source-changed refusal", err)
	}
	if _, statErr := os.Lstat(filepath.Join(fixture.agentDir, "alpha")); !os.IsNotExist(statErr) {
		t.Fatalf("the swapped-in directory was copied into the agent (lstat err = %v)", statErr)
	}
	saved, loadErr := state.Load(fixture.statePath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if copies := saved.Targets[fixture.agentDir].Copies; len(copies) != 0 {
		t.Fatalf("state records a copy that was refused: %v", copies)
	}
}

// sm --copy dereferences a symlinked SKILL.md, so the file it copies must be
// the very manifest enumeration validated: a target replaced after
// enumeration (here by a link to a private key) is refused.
func TestDoSync_CopyRefusesManifestSwappedAfterEnumeration(t *testing.T) {
	fixture := newSyncFixture(t)
	shared := filepath.Join(t.TempDir(), "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(shared, "foo.md")
	if err := os.WriteFile(manifest, []byte("---\nname: foo\n---\nshared manifest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(fixture.storeDir, "foo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(manifest, filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(secret, []byte("private key"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := afterEnumerate
	t.Cleanup(func() { afterEnumerate = original })
	afterEnumerate = func(listing store.Listing) {
		if err := os.Remove(manifest); err != nil {
			t.Errorf("seam: %v", err)
			return
		}
		if err := os.Symlink(secret, manifest); err != nil {
			t.Errorf("seam: %v", err)
		}
	}

	var log bytes.Buffer
	_, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log)
	if err == nil || !strings.Contains(err.Error(), "SKILL.md changed since it was validated") {
		t.Fatalf("doSync err = %v, want a manifest-changed refusal", err)
	}
	if _, statErr := os.Lstat(filepath.Join(fixture.agentDir, "foo")); !os.IsNotExist(statErr) {
		t.Fatalf("a copy was placed with the swapped-in manifest (lstat err = %v)", statErr)
	}
}
