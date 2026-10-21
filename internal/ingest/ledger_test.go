package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLedgerPath_FollowsXDG(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	got, err := LedgerPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dataHome, "skills-mapper", "ingest-log.json")
	if got != want {
		t.Fatalf("LedgerPath = %q, want %q", got, want)
	}
}

func TestLedgerPath_FallsBackForEmptyOrRelativeXDG(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".local", "share", "skills-mapper", "ingest-log.json")
	for _, value := range []string{"", "relative/dir"} {
		t.Setenv("XDG_DATA_HOME", value)
		got, err := LedgerPath()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("XDG_DATA_HOME=%q: LedgerPath = %q, want %q", value, got, want)
		}
	}
}

func TestLoadLedger_MissingIsEmpty(t *testing.T) {
	ledger, err := LoadLedger(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Records) != 0 {
		t.Fatalf("records = %v, want none", ledger.Records)
	}
}

func TestLoadLedger_MalformedIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ingest-log.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLedger(path); err == nil {
		t.Fatal("expected error for malformed ledger")
	}
}

func TestLedger_SaveLoadRoundTripWithModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "skills-mapper", "ingest-log.json")
	ingestedAt := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	var ledger Ledger
	ledger.Add(Record{Name: "alpha", SourceDir: "/src/plugins/p/alpha", SourceRoot: "/src", FromPlugin: true, At: ingestedAt})
	ledger.Add(Record{Name: "beta", SourceDir: "/src/beta", SourceRoot: "/src", At: ingestedAt})
	if err := ledger.Save(path); err != nil {
		t.Fatal(err)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v, want 0600", fileInfo.Mode().Perm())
	}
	directoryInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if directoryInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v, want 0700", directoryInfo.Mode().Perm())
	}

	loaded, err := LoadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Records) != 2 || loaded.Records[0].Name != "alpha" || !loaded.Records[0].FromPlugin ||
		loaded.Records[1].Name != "beta" || !loaded.Records[1].At.Equal(ingestedAt) {
		t.Fatalf("round trip mismatch: %+v", loaded.Records)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", entry.Name())
		}
	}
}
