package link

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const expectedStageMode = os.FileMode(0o700)

func TestStageAndSwap_StageIsRandomPrivateAndBesideDestination(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "skill")
	var report CopyReport
	inspected := false

	err := stageAndSwap(destination, false, nil, &report, func(stage *os.Root) error {
		inspected = true
		stagePath := stage.Name()
		if got := filepath.Dir(stagePath); got != parent {
			t.Errorf("stage lives in %q, want the destination's parent %q", got, parent)
		}
		if filepath.Base(stagePath) == filepath.Base(destination)+".sm-stage" {
			t.Errorf("stage uses the predictable name %q", filepath.Base(stagePath))
		}
		if !strings.HasPrefix(filepath.Base(stagePath), stagePrefix) {
			t.Errorf("stage name %q lacks the random %q prefix", filepath.Base(stagePath), stagePrefix)
		}
		info, err := os.Lstat(stagePath)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != expectedStageMode {
			t.Errorf("stage mode = %o, want %o", got, expectedStageMode)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stageAndSwap: %v", err)
	}
	if !inspected {
		t.Fatal("build callback never ran")
	}
}
