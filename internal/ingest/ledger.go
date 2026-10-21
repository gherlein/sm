package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/gherlein/sm/internal/pathutil"
)

const (
	ledgerVersion = 1
	// The ledger lists where third-party skills came from; like the sm state
	// file it is private to the owner.
	ledgerDirectoryMode = 0o700
	ledgerFileMode      = 0o600

	ledgerTempPattern = ".ingest-log-*.tmp"
	ledgerDirectory   = "skills-mapper"
	ledgerFileName    = "ingest-log.json"
)

// Record is one ingested skill and where it came from.
type Record struct {
	Name       string    `json:"name"`
	SourceDir  string    `json:"sourceDir"`
	SourceRoot string    `json:"sourceRoot"`
	FromPlugin bool      `json:"fromPlugin"`
	At         time.Time `json:"at"`
}

// Ledger is the private provenance log of every skill ms wrote. It lives
// outside the store because the store directory must hold only skills.
type Ledger struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}

// LedgerPath returns $XDG_DATA_HOME/skills-mapper/ingest-log.json, with the
// data home resolved by pathutil.DataHome like the store and the state file.
func LedgerPath() (string, error) {
	dataHome, err := pathutil.DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataHome, ledgerDirectory, ledgerFileName), nil
}

// LoadLedger reads the ledger at path. A missing file is an empty ledger; a
// malformed one is an error rather than being silently discarded, since
// overwriting it would erase the provenance history.
func LoadLedger(path string) (Ledger, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Ledger{Version: ledgerVersion}, nil
	}
	if err != nil {
		return Ledger{}, fmt.Errorf("read ingest ledger %s: %w", path, err)
	}
	var ledger Ledger
	if err := json.Unmarshal(content, &ledger); err != nil {
		return Ledger{}, fmt.Errorf("parse ingest ledger %s: %w", path, err)
	}
	if ledger.Version != ledgerVersion {
		return Ledger{}, fmt.Errorf("ingest ledger %s: unsupported version %d (want %d)", path, ledger.Version, ledgerVersion)
	}
	return ledger, nil
}

// Add appends a record.
func (ledger *Ledger) Add(record Record) {
	ledger.Records = append(ledger.Records, record)
}

// Save writes the ledger atomically (temp file in the same directory, then
// rename) so a crash never leaves a truncated log.
func (ledger Ledger) Save(path string) error {
	ledger.Version = ledgerVersion
	if ledger.Records == nil {
		ledger.Records = []Record{}
	}
	content, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return fmt.Errorf("encode ingest ledger: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), ledgerDirectoryMode); err != nil {
		return fmt.Errorf("create ledger dir: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ledgerTempPattern)
	if err != nil {
		return fmt.Errorf("create temp ledger for %s: %w", path, err)
	}
	tempPath := temp.Name()
	if _, err := temp.Write(content); err != nil {
		return errors.Join(fmt.Errorf("write temp ledger %s: %w", tempPath, err), temp.Close(), os.Remove(tempPath))
	}
	if err := temp.Close(); err != nil {
		return errors.Join(fmt.Errorf("close temp ledger %s: %w", tempPath, err), os.Remove(tempPath))
	}
	if err := os.Chmod(tempPath, ledgerFileMode); err != nil {
		return errors.Join(fmt.Errorf("chmod temp ledger %s: %w", tempPath, err), os.Remove(tempPath))
	}
	if err := os.Rename(tempPath, path); err != nil {
		return errors.Join(fmt.Errorf("replace ingest ledger %s: %w", path, err), os.Remove(tempPath))
	}
	return nil
}
