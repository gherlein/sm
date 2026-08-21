// Package state persists sm-created links per target and computes reconcile plans.
package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/gherlein/skills-mapper/internal/resolve"
)

const version = 1

type TargetLinks struct {
	Links     map[string]string `json:"links"`
	UpdatedAt string            `json:"updated_at"`
}
type State struct {
	Version int                    `json:"version"`
	Targets map[string]TargetLinks `json:"targets"`
}
type Recon struct {
	Add    []resolve.Link
	Remove []string
}

func Plan(prev map[string]string, desired []resolve.Link) Recon {
	want := map[string]bool{}
	var r Recon
	for _, l := range desired {
		want[l.Name] = true
		if src, ok := prev[l.Name]; !ok || src != l.SourceDir {
			r.Add = append(r.Add, l)
		}
	}
	for name := range prev {
		if !want[name] {
			r.Remove = append(r.Remove, name)
		}
	}
	return r
}

func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &State{Version: version, Targets: map[string]TargetLinks{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Targets == nil {
		s.Targets = map[string]TargetLinks{}
	}
	if s.Version == 0 {
		s.Version = version
	}
	return &s, nil
}

func (s *State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func DefaultPath() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "skills-mapper", "state.json"), nil
}
