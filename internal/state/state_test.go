package state

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func inspectFrom(table map[string]EntryInfo) func(string) EntryInfo {
	return func(name string) EntryInfo {
		return table[name]
	}
}

var (
	absent   = EntryInfo{}
	ours     = EntryInfo{Exists: true, Ours: true}
	notOurs  = EntryInfo{Exists: true, Ours: false}
	noAction = TargetPlan{Create: map[string]string{}, Repoint: map[string]string{}}
)

// planOf builds an expected plan; nil maps and slices mean "empty".
func planOf(create, repoint map[string]string, keep, remove, unmanaged, lost []string) TargetPlan {
	plan := TargetPlan{Create: map[string]string{}, Repoint: map[string]string{}}
	for name, storePath := range create {
		plan.Create[name] = storePath
	}
	for name, storePath := range repoint {
		plan.Repoint[name] = storePath
	}
	plan.Keep, plan.Remove, plan.Unmanaged, plan.Lost = keep, remove, unmanaged, lost
	return plan
}

func assertPlan(t *testing.T, got, want TargetPlan) {
	t.Helper()
	if !reflect.DeepEqual(got.Create, want.Create) {
		t.Errorf("Create = %v, want %v", got.Create, want.Create)
	}
	if !reflect.DeepEqual(got.Repoint, want.Repoint) {
		t.Errorf("Repoint = %v, want %v", got.Repoint, want.Repoint)
	}
	checkSlice := func(field string, got, want []string) {
		if len(got) == 0 && len(want) == 0 {
			return
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", field, got, want)
		}
	}
	checkSlice("Keep", got.Keep, want.Keep)
	checkSlice("Remove", got.Remove, want.Remove)
	checkSlice("Unmanaged", got.Unmanaged, want.Unmanaged)
	checkSlice("Lost", got.Lost, want.Lost)
}

// TestPlanTarget_Rules covers every rule of the identity-based planner, one
// row per rule.
func TestPlanTarget_Rules(t *testing.T) {
	cases := []struct {
		name     string
		desired  map[string]string
		recorded map[string]string
		disk     map[string]EntryInfo
		want     TargetPlan
	}{
		{
			name:    "desired and absent is created",
			desired: map[string]string{"a": "/store/a"},
			disk:    map[string]EntryInfo{"a": absent},
			want:    planOf(map[string]string{"a": "/store/a"}, nil, nil, nil, nil, nil),
		},
		{
			name:     "desired, recorded but absent is recreated",
			desired:  map[string]string{"a": "/store/a"},
			recorded: map[string]string{"a": "/store/a"},
			disk:     map[string]EntryInfo{"a": absent},
			want:     planOf(map[string]string{"a": "/store/a"}, nil, nil, nil, nil, nil),
		},
		{
			name:     "desired, recorded, ours, same target is kept",
			desired:  map[string]string{"a": "/store/a"},
			recorded: map[string]string{"a": "/store/a"},
			disk:     map[string]EntryInfo{"a": ours},
			want:     planOf(nil, nil, []string{"a"}, nil, nil, nil),
		},
		{
			name:     "desired, recorded, ours, different target is repointed",
			desired:  map[string]string{"a": "/new-store/a"},
			recorded: map[string]string{"a": "/old-store/a"},
			disk:     map[string]EntryInfo{"a": ours},
			want:     planOf(nil, map[string]string{"a": "/new-store/a"}, nil, nil, nil, nil),
		},
		{
			name:     "desired, recorded, not ours is unmanaged and lost",
			desired:  map[string]string{"a": "/store/a"},
			recorded: map[string]string{"a": "/store/a"},
			disk:     map[string]EntryInfo{"a": notOurs},
			want:     planOf(nil, nil, nil, nil, []string{"a"}, []string{"a"}),
		},
		{
			name:    "desired, present, unrecorded is unmanaged",
			desired: map[string]string{"a": "/store/a"},
			disk:    map[string]EntryInfo{"a": notOurs},
			want:    planOf(nil, nil, nil, nil, []string{"a"}, nil),
		},
		{
			name:     "recorded, undesired, absent is removed",
			recorded: map[string]string{"a": "/store/a"},
			disk:     map[string]EntryInfo{"a": absent},
			want:     planOf(nil, nil, nil, []string{"a"}, nil, nil),
		},
		{
			name:     "recorded, undesired, ours is removed",
			recorded: map[string]string{"a": "/store/a"},
			disk:     map[string]EntryInfo{"a": ours},
			want:     planOf(nil, nil, nil, []string{"a"}, nil, nil),
		},
		{
			name:     "recorded, undesired, not ours is lost and never removed",
			recorded: map[string]string{"a": "/store/a"},
			disk:     map[string]EntryInfo{"a": notOurs},
			want:     planOf(nil, nil, nil, nil, nil, []string{"a"}),
		},
		{
			name: "nothing desired or recorded",
			disk: map[string]EntryInfo{},
			want: noAction,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := PlanTarget(testCase.desired, testCase.recorded, inspectFrom(testCase.disk))
			assertPlan(t, got, testCase.want)
		})
	}
}

func TestPlanTarget_CreateNew(t *testing.T) {
	plan := PlanTarget(
		map[string]string{"a": "/store/a"},
		map[string]string{},
		inspectFrom(map[string]EntryInfo{"a": absent}),
	)
	assertPlan(t, plan, planOf(map[string]string{"a": "/store/a"}, nil, nil, nil, nil, nil))
}

func TestPlanTarget_KeepRecorded(t *testing.T) {
	plan := PlanTarget(
		map[string]string{"a": "/store/a"},
		map[string]string{"a": "/store/a"},
		inspectFrom(map[string]EntryInfo{"a": ours}),
	)
	assertPlan(t, plan, planOf(nil, nil, []string{"a"}, nil, nil, nil))
}

func TestPlanTarget_RemoveStale(t *testing.T) {
	plan := PlanTarget(
		map[string]string{},
		map[string]string{"a": "/store/a"},
		inspectFrom(map[string]EntryInfo{"a": ours}),
	)
	assertPlan(t, plan, planOf(nil, nil, nil, []string{"a"}, nil, nil))
}

func TestPlanTarget_UnmanagedSkipped(t *testing.T) {
	plan := PlanTarget(
		map[string]string{"a": "/store/a"},
		map[string]string{},
		inspectFrom(map[string]EntryInfo{"a": notOurs}),
	)
	assertPlan(t, plan, planOf(nil, nil, nil, nil, []string{"a"}, nil))
}

func TestPlanTarget_StaleAlreadyGone(t *testing.T) {
	plan := PlanTarget(
		map[string]string{},
		map[string]string{"a": "/store/a"},
		inspectFrom(map[string]EntryInfo{"a": absent}),
	)
	assertPlan(t, plan, planOf(nil, nil, nil, []string{"a"}, nil, nil))
}

// A symlink sm did not record is never ours, whatever it points at.
func TestPlanTarget_UnmanagedSymlinkNotOurs(t *testing.T) {
	plan := PlanTarget(
		map[string]string{"a": "/store/a"},
		map[string]string{},
		inspectFrom(map[string]EntryInfo{"a": notOurs}),
	)
	assertPlan(t, plan, planOf(nil, nil, nil, nil, []string{"a"}, nil))
}

// R4: a store move must re-point links sm owns instead of keeping them.
func TestPlanTarget_RepointWhenStoreMoved(t *testing.T) {
	plan := PlanTarget(
		map[string]string{"a": "/new/a"},
		map[string]string{"a": "/old/a"},
		inspectFrom(map[string]EntryInfo{"a": ours}),
	)
	assertPlan(t, plan, planOf(nil, map[string]string{"a": "/new/a"}, nil, nil, nil, nil))
}

// R3: a user-replaced entry at a recorded name is lost, never removed.
func TestPlanTarget_UserReplacedRecordedEntryIsLostNotRemoved(t *testing.T) {
	plan := PlanTarget(
		map[string]string{},
		map[string]string{"a": "/store/a"},
		inspectFrom(map[string]EntryInfo{"a": notOurs}),
	)
	assertPlan(t, plan, planOf(nil, nil, nil, nil, nil, []string{"a"}))
}

func TestPlanTarget_LostAndUnmanagedWhenDesired(t *testing.T) {
	plan := PlanTarget(
		map[string]string{"a": "/store/a"},
		map[string]string{"a": "/store/a"},
		inspectFrom(map[string]EntryInfo{"a": notOurs}),
	)
	assertPlan(t, plan, planOf(nil, nil, nil, nil, []string{"a"}, []string{"a"}))
}

func TestPlanTarget_SortedOutput(t *testing.T) {
	plan := PlanTarget(
		map[string]string{"z": "/s/z", "m": "/s/m", "k2": "/s/k2", "k1": "/s/k1", "q": "/s/q", "c": "/s/c"},
		map[string]string{"y": "/s/y", "b": "/s/b", "k2": "/s/k2", "k1": "/s/k1", "x": "/s/x", "w": "/s/w", "q": "/s/q", "c": "/s/c"},
		inspectFrom(map[string]EntryInfo{
			"z": notOurs, "m": notOurs,
			"k2": ours, "k1": ours,
			"x": notOurs, "w": notOurs,
			"q": notOurs, "c": notOurs,
		}),
	)
	want := planOf(nil, nil,
		[]string{"k1", "k2"},
		[]string{"b", "y"},
		[]string{"c", "m", "q", "z"},
		[]string{"c", "q", "w", "x"},
	)
	assertPlan(t, plan, want)
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "state.json")
	original := State{Targets: map[string]Target{
		"/home/u/.claude/skills": {Links: map[string]string{"a": "/store/a"}},
	}}
	if err := original.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(loaded.Targets, original.Targets) {
		t.Fatalf("Targets = %v, want %v", loaded.Targets, original.Targets)
	}
	if loaded.Version != stateVersion {
		t.Fatalf("Version = %d", loaded.Version)
	}
	if loaded.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt not set")
	}
}

func TestLoad_Missing(t *testing.T) {
	loaded, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Targets == nil {
		t.Fatal("Targets must be usable (non-nil)")
	}
	if len(loaded.Targets) != 0 {
		t.Fatalf("Targets = %v", loaded.Targets)
	}
}

func TestSave_ReplacesExistingFileAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	old := State{Targets: map[string]Target{"/a": {Links: map[string]string{"old": "/store/old"}}}}
	if err := old.Save(path); err != nil {
		t.Fatalf("Save old: %v", err)
	}
	replacement := State{Targets: map[string]Target{"/b": {Links: map[string]string{"new": "/store/new"}}}}
	if err := replacement.Save(path); err != nil {
		t.Fatalf("Save replacement: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(loaded.Targets, replacement.Targets) {
		t.Fatalf("Targets = %v, want %v", loaded.Targets, replacement.Targets)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory has %d entries, want only state.json (temp file leaked)", len(entries))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != stateFileMode {
		t.Fatalf("mode = %o, want %o", info.Mode().Perm(), stateFileMode)
	}
}

func writeStateFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// R17: a state file from a future release, an absent version, or a null
// document must never be silently downgraded and rewritten.
func TestLoad_RejectsVersionMismatchAndNull(t *testing.T) {
	documents := map[string]string{
		"future version": `{"version":2,"targets":{"/a":{"links":{"x":"/store/x"}}}}`,
		"zero version":   `{"version":0,"targets":{}}`,
		"no version":     `{"targets":{"/a":{"links":{"x":"/store/x"}}}}`,
		"null document":  `null`,
		"empty object":   `{}`,
	}
	for label, document := range documents {
		t.Run(label, func(t *testing.T) {
			path := writeStateFile(t, document)
			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load accepted %s", document)
			}
			if !strings.Contains(err.Error(), "unsupported state version") {
				t.Fatalf("error %q lacks 'unsupported state version'", err)
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(content) != document {
				t.Fatalf("state file was modified: %q", content)
			}
		})
	}
}

func TestLoad_AcceptsVersion1(t *testing.T) {
	path := writeStateFile(t, `{"version":1,"targets":{"/a":{"links":{"x":"/store/x"}}}}`)
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]Target{"/a": {Links: map[string]string{"x": "/store/x"}}}
	if !reflect.DeepEqual(loaded.Targets, want) {
		t.Fatalf("Targets = %v, want %v", loaded.Targets, want)
	}
	if loaded.Version != stateVersion {
		t.Fatalf("Version = %d, want %d", loaded.Version, stateVersion)
	}
}

// The exact shape the previous release wrote: MarshalIndent output with a
// symlink-only target (copies omitted), a copy-mode target, a target whose
// links ledger is null, and an RFC 3339 updated_at.
func TestLoad_ReadsPreviousReleaseState(t *testing.T) {
	const previousRelease = `{
  "version": 1,
  "targets": {
    "/home/u/.claude/skills": {
      "links": {
        "alpha": "/home/u/.local/share/skills/alpha",
        "beta": "/home/u/.local/share/skills/beta"
      }
    },
    "/home/u/.pi/skills": {
      "links": null,
      "copies": {
        "gamma": "/home/u/.local/share/skills/gamma"
      }
    }
  },
  "updated_at": "2026-09-01T12:34:56.789Z"
}
`
	loaded, err := Load(writeStateFile(t, previousRelease))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]Target{
		"/home/u/.claude/skills": {Links: map[string]string{
			"alpha": "/home/u/.local/share/skills/alpha",
			"beta":  "/home/u/.local/share/skills/beta",
		}},
		"/home/u/.pi/skills": {Copies: map[string]string{
			"gamma": "/home/u/.local/share/skills/gamma",
		}},
	}
	if !reflect.DeepEqual(loaded.Targets, want) {
		t.Fatalf("Targets = %#v, want %#v", loaded.Targets, want)
	}
	if loaded.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt not parsed")
	}
}

// R28: the state file and the directory sm creates for it are private.
func TestSave_Modes(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "skills-mapper")
	path := filepath.Join(stateDir, "state.json")
	if err := (State{}).Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Errorf("state file mode = %o, want 600", fileInfo.Mode().Perm())
	}
	dirInfo, err := os.Stat(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("state dir mode = %o, want 700", dirInfo.Mode().Perm())
	}
}

// An empty recorded value can never be matched by an identity check, so the
// entry it names could never be removed and every later run would fail on it;
// it is rejected when the file is read instead.
func TestLoad_RejectsEmptyRecordedTarget(t *testing.T) {
	documents := map[string]string{
		"empty link target": `{"version":1,"targets":{"/a":{"links":{"x":""}}}}`,
		"empty copy source": `{"version":1,"targets":{"/a":{"links":{},"copies":{"x":""}}}}`,
	}
	for label, document := range documents {
		t.Run(label, func(t *testing.T) {
			_, err := Load(writeStateFile(t, document))
			if err == nil {
				t.Fatalf("Load accepted %s", document)
			}
			if !strings.Contains(err.Error(), "empty recorded") {
				t.Fatalf("error %q lacks 'empty recorded'", err)
			}
		})
	}
}

// A recorded name is joined to the agent directory before anything is
// removed, so a name that is not a single path segment could reach outside it.
func TestLoad_RejectsInvalidName(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", `a\\b`, "../escape"} {
		document := `{"version":1,"targets":{"/a":{"links":{"` + name + `":"/store/x"}}}}`
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeStateFile(t, document))
			if err == nil {
				t.Fatalf("Load accepted %s", document)
			}
			if !strings.Contains(err.Error(), "invalid recorded name") {
				t.Fatalf("error %q lacks 'invalid recorded name'", err)
			}
		})
	}
	copyDocument := `{"version":1,"targets":{"/a":{"links":{},"copies":{"../x":"/store/x"}}}}`
	if _, err := Load(writeStateFile(t, copyDocument)); err == nil {
		t.Fatalf("Load accepted %s", copyDocument)
	}
}

// Target keys are agent directories that clean --orphans acts on; a relative
// key would resolve against whatever directory sm happens to run in.
func TestLoad_RejectsRelativeTargetDir(t *testing.T) {
	for _, key := range []string{"", "relative/skills"} {
		document := `{"version":1,"targets":{"` + key + `":{"links":{"x":"/store/x"}}}}`
		t.Run(key, func(t *testing.T) {
			_, err := Load(writeStateFile(t, document))
			if err == nil {
				t.Fatalf("Load accepted %s", document)
			}
			if !strings.Contains(err.Error(), "not absolute") {
				t.Fatalf("error %q lacks 'not absolute'", err)
			}
		})
	}
}

// A version-1 file without a targets object is not one sm wrote (Save always
// writes the object); accepting it would silently drop every provenance record.
func TestLoad_RejectsVersion1WithoutTargets(t *testing.T) {
	documents := map[string]string{
		"targets absent": `{"version":1}`,
		"targets null":   `{"version":1,"targets":null}`,
	}
	for label, document := range documents {
		t.Run(label, func(t *testing.T) {
			path := writeStateFile(t, document)
			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load accepted %s", document)
			}
			if !strings.Contains(err.Error(), "no targets") {
				t.Fatalf("error %q lacks 'no targets'", err)
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(content) != document {
				t.Fatalf("state file was modified: %q", content)
			}
		})
	}
}

// An empty targets object is what a previous release wrote after cleaning
// everything; it must keep loading.
func TestLoad_AcceptsEmptyTargetsObject(t *testing.T) {
	loaded, err := Load(writeStateFile(t, `{"version":1,"targets":{},"updated_at":"2026-09-01T12:34:56Z"}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Targets == nil || len(loaded.Targets) != 0 {
		t.Fatalf("Targets = %#v, want an empty non-nil map", loaded.Targets)
	}
}

// Keys are joined with recorded names before removal; a key with ".." or
// redundant separators names a different directory than it appears to.
func TestLoad_RejectsUncleanTargetDir(t *testing.T) {
	for _, key := range []string{"/home/u/agent/../.config", "/home/u//skills", "/home/u/./skills", "/home/u/skills/"} {
		document := `{"version":1,"targets":{"` + key + `":{"links":{"x":"/store/x"}}}}`
		t.Run(key, func(t *testing.T) {
			_, err := Load(writeStateFile(t, document))
			if err == nil {
				t.Fatalf("Load accepted %s", document)
			}
			if !strings.Contains(err.Error(), "not a clean path") {
				t.Fatalf("error %q lacks 'not a clean path'", err)
			}
		})
	}
}

// Ours is meaningful only for a recorded name. A present, unrecorded entry is
// foreign even when the caller reports Ours: true, so it must never be planned
// for any change.
func TestPlanTarget_OursIsIgnoredForUnrecordedName(t *testing.T) {
	desired := map[string]string{"alpha": "/store/alpha"}
	disk := map[string]EntryInfo{"alpha": {Exists: true, Ours: true}}

	plan := PlanTarget(desired, map[string]string{}, inspectFrom(disk))

	if !reflect.DeepEqual(plan.Unmanaged, []string{"alpha"}) {
		t.Errorf("Unmanaged = %v, want [alpha]", plan.Unmanaged)
	}
	if len(plan.Create) != 0 || len(plan.Repoint) != 0 || len(plan.Keep) != 0 || len(plan.Remove) != 0 || len(plan.Lost) != 0 {
		t.Errorf("unexpected actions for an unrecorded entry: %+v", plan)
	}
}
