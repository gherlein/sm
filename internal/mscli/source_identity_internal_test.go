package mscli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gherlein/sm/internal/ingest"
)

// A source skill replaced by a symlink to another directory after discovery
// validated it must not be ingested: the store and the ledger would carry
// whatever the link points at.
func TestRun_RefusesSkillSwappedAfterDiscovery(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	source := filepath.Join(t.TempDir(), "claude")
	writeSourceSkill(t, source, "alpha", "from source")
	destination := filepath.Join(t.TempDir(), "store")
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.MkdirAll(secret, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secret, "id_ed25519"), []byte("private key"), 0o600); err != nil {
		t.Fatal(err)
	}
	graveyard := t.TempDir()
	original := afterDiscover
	t.Cleanup(func() { afterDiscover = original })
	afterDiscover = func(discovered ingest.DiscoverResult) {
		for _, skill := range discovered.Skills {
			if err := os.Rename(skill.Dir, filepath.Join(graveyard, skill.Name)); err != nil {
				t.Errorf("seam: %v", err)
				return
			}
			if err := os.Symlink(secret, skill.Dir); err != nil {
				t.Errorf("seam: %v", err)
			}
		}
	}

	code, stdout, stderr := run(source, destination)
	if code != exitError {
		t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, exitError, stdout, stderr)
	}
	if !strings.Contains(stderr, "source changed since it was validated") {
		t.Fatalf("stderr lacks the source-changed refusal: %s", stderr)
	}
	assertAbsent(t, filepath.Join(destination, "alpha"))
	ledgerPath := filepath.Join(dataHome, "skills-mapper", "ingest-log.json")
	assertAbsent(t, ledgerPath)
}
