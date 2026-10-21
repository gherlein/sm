// Package probe detects which well-known agents are installed and appends
// the newly found ones to agents.toml.
package probe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/gherlein/sm/internal/agents"
	"github.com/gherlein/sm/internal/pathutil"
)

const (
	homePrefix     = "~"
	agentsHeader   = "[agents]\n"
	configDirMode  = 0o755
	configFileMode = 0o644
	agentsTable    = "agents"
	asciiSpace     = 0x20
	asciiDelete    = 0x7f
)

var errUnsupportedLayout = errors.New(`agents.toml declares "agents" as an inline or dotted table; edit the file by hand`)

// Found is a detected agent and its expanded absolute default skills dir.
type Found struct {
	Name string
	Dir  string
}

// Detect reports the registry agents that are installed: ConfigDir exists
// under home, or the binary is on the path. A leading "~" in ConfigDir is
// resolved against the given home rather than the real user home so callers
// (and tests) can probe a fake home; any other ConfigDir must already be
// absolute. DefaultDir is expanded against the real user home because it is
// the directory that will be written into the config.
func Detect(registry []agents.Known, home string, onPath func(bin string) bool) ([]Found, error) {
	var found []Found
	for _, known := range registry {
		installed, err := configDirExists(known.ConfigDir, home)
		if err != nil {
			return nil, fmt.Errorf("probe %s: %w", known.Name, err)
		}
		if !installed && !onPath(known.Binary) {
			continue
		}
		dir, err := agents.Expand(known.DefaultDir)
		if err != nil {
			return nil, fmt.Errorf("expand default dir for %s: %w", known.Name, err)
		}
		found = append(found, Found{Name: known.Name, Dir: dir})
	}
	return found, nil
}

func configDirExists(configDir, home string) (bool, error) {
	resolved := configDir
	if configDir == homePrefix || strings.HasPrefix(configDir, homePrefix+"/") {
		resolved = home + configDir[len(homePrefix):]
	}
	if !filepath.IsAbs(resolved) {
		return false, fmt.Errorf("config dir %q is not absolute after expansion", resolved)
	}
	_, err := os.Stat(resolved)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("stat %s: %w", resolved, err)
}

// AddMissing inserts a `name = "dir"` entry into the [agents] table of
// cfgPath for each found agent that is not already in existing and that
// confirm accepts, returning the names added. The file is edited line by line
// rather than decoded and re-encoded so comments and layout survive. A missing
// file is created with the header, but only if something is actually added.
//
// The layout and the values are validated before confirm is called so the
// user is never asked to accept entries that cannot be written. The final
// write is a same-directory temp file plus rename so a crash cannot truncate
// the user's hand-edited config.
func AddMissing(cfgPath string, found []Found, existing []agents.Agent, confirm func(Found) bool) ([]string, error) {
	known := make(map[string]bool, len(existing))
	for _, agent := range existing {
		known[agent.Name] = true
	}
	var candidates []Found
	for _, candidate := range found {
		if known[candidate.Name] {
			continue
		}
		if !utf8.ValidString(candidate.Name) || !utf8.ValidString(candidate.Dir) {
			return nil, fmt.Errorf("agent %q: name or directory is not valid UTF-8", candidate.Name)
		}
		candidates = append(candidates, candidate)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	targetPath, err := resolveTarget(cfgPath)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(targetPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", cfgPath, err)
	}
	layout, err := scanLayout(string(content))
	if err != nil {
		return nil, fmt.Errorf("update %s: %w", cfgPath, err)
	}

	var accepted []Found
	for _, candidate := range candidates {
		if confirm(candidate) {
			accepted = append(accepted, candidate)
		}
	}
	if len(accepted) == 0 {
		return nil, nil
	}

	updated := layout.insert(string(content), accepted)
	if err := os.MkdirAll(filepath.Dir(targetPath), configDirMode); err != nil {
		return nil, fmt.Errorf("create config directory: %w", err)
	}
	if err := writeAtomically(targetPath, updated); err != nil {
		return nil, err
	}
	added := make([]string, 0, len(accepted))
	for _, candidate := range accepted {
		added = append(added, candidate.Name)
	}
	return added, nil
}

// resolveTarget follows a symlinked config (dotfiles setups), including one
// whose target does not exist yet, so the rename replaces the real file and
// the link survives.
func resolveTarget(cfgPath string) (string, error) {
	absolute, err := filepath.Abs(cfgPath)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", cfgPath, err)
	}
	resolved, err := pathutil.ResolveExisting(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", cfgPath, err)
	}
	return resolved, nil
}

// writeAtomically replaces path with content via a temp file in the same
// directory (rename is only atomic within one filesystem), keeping the
// original file's mode.
func writeAtomically(path, content string) (err error) {
	mode := os.FileMode(configFileMode)
	info, statErr := os.Stat(path)
	switch {
	case statErr == nil:
		mode = info.Mode().Perm()
	case !os.IsNotExist(statErr):
		return fmt.Errorf("stat %s: %w", path, statErr)
	}

	temp, err := os.CreateTemp(filepath.Dir(path), ".agents-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", path, err)
	}
	tempPath := temp.Name()
	closed := false
	defer func() {
		if err == nil {
			return
		}
		if !closed {
			if closeErr := temp.Close(); closeErr != nil {
				err = fmt.Errorf("%w (close: %v)", err, closeErr)
			}
		}
		if removeErr := os.Remove(tempPath); removeErr != nil && !os.IsNotExist(removeErr) {
			err = fmt.Errorf("%w (remove temp: %v)", err, removeErr)
		}
	}()
	if err := temp.Chmod(mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tempPath, err)
	}
	if _, err := temp.WriteString(content); err != nil {
		return fmt.Errorf("write %s: %w", tempPath, err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", tempPath, err)
	}
	closed = true
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tempPath, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

var bareKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// fileLayout is where the [agents] table sits in a scanned config.
type fileLayout struct {
	lines          []string
	headerIndex    int
	lastEntryIndex int
}

// scanLayout finds the [agents] table line by line. Lines inside multi-line
// strings or multi-line arrays are skipped so a look-alike header in another
// table's value is not mistaken for the real one.
func scanLayout(content string) (fileLayout, error) {
	layout := fileLayout{headerIndex: -1, lastEntryIndex: -1}
	if strings.TrimSpace(content) == "" {
		return layout, nil
	}
	layout.lines = strings.SplitAfter(content, "\n")
	if layout.lines[len(layout.lines)-1] == "" {
		layout.lines = layout.lines[:len(layout.lines)-1]
	}

	inRoot := true
	multilineDelimiter := ""
	arrayDepth := 0
	for index, line := range layout.lines {
		trimmed := strings.TrimSpace(line)
		if multilineDelimiter != "" {
			if strings.Contains(line, multilineDelimiter) {
				multilineDelimiter = ""
				layout.noteEntry(index)
			}
			continue
		}
		if arrayDepth > 0 {
			arrayDepth += strings.Count(trimmed, "[") - strings.Count(trimmed, "]")
			if arrayDepth <= 0 {
				arrayDepth = 0
				layout.noteEntry(index)
			}
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			inRoot = false
			name, isArrayTable := tableName(trimmed)
			switch {
			case !isArrayTable && name == agentsTable:
				layout.headerIndex = index
				layout.lastEntryIndex = index
				continue
			case name == agentsTable || strings.HasPrefix(name, agentsTable+"."):
				return layout, errUnsupportedLayout
			}
			if layout.headerIndex >= 0 {
				// The agents table ended here; later lines cannot move the insertion
				// point. A later [agents.x] is not checked: Load rejects it anyway.
				return layout, nil
			}
			continue
		}
		if inRoot && isAgentsRootKey(trimmed) {
			return layout, errUnsupportedLayout
		}
		multilineDelimiter, arrayDepth = openValueState(trimmed)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			layout.noteEntry(index)
		}
	}
	return layout, nil
}

// noteEntry records the latest key line, but only while inside [agents].
func (layout *fileLayout) noteEntry(index int) {
	if layout.headerIndex >= 0 && layout.lastEntryIndex >= 0 {
		layout.lastEntryIndex = index
	}
}

// openValueState reports whether a key line opens a multi-line string or a
// multi-line array that continues on following lines.
func openValueState(trimmedLine string) (string, int) {
	_, value, found := strings.Cut(trimmedLine, "=")
	if !found {
		return "", 0
	}
	value = strings.TrimSpace(value)
	for _, delimiter := range []string{`"""`, `'''`} {
		if strings.HasPrefix(value, delimiter) && strings.Count(value, delimiter)%2 == 1 {
			return delimiter, 0
		}
	}
	if strings.HasPrefix(value, "[") {
		if depth := strings.Count(value, "[") - strings.Count(value, "]"); depth > 0 {
			return "", depth
		}
	}
	return "", 0
}

// insert returns content with an entry per accepted agent placed inside the
// [agents] table, adding the table when the file has none.
func (layout fileLayout) insert(content string, accepted []Found) string {
	var entries strings.Builder
	for _, candidate := range accepted {
		entries.WriteString(formatKey(candidate.Name) + " = " + quoteString(candidate.Dir) + "\n")
	}
	if strings.TrimSpace(content) == "" {
		return agentsHeader + entries.String()
	}
	if layout.headerIndex < 0 {
		return appendHeader(content, entries.String())
	}
	var result strings.Builder
	for index, line := range layout.lines {
		result.WriteString(line)
		if index != layout.lastEntryIndex {
			continue
		}
		if !strings.HasSuffix(line, "\n") {
			result.WriteString("\n")
		}
		result.WriteString(entries.String())
	}
	return result.String()
}

// tableName extracts the dotted name of a [table] or [[array]] header line,
// ignoring inner whitespace, quotes and a trailing comment.
func tableName(trimmedHeader string) (string, bool) {
	isArrayTable := strings.HasPrefix(trimmedHeader, "[[")
	closing := strings.Index(trimmedHeader, "]")
	if closing < 0 {
		return "", isArrayTable
	}
	name := strings.TrimLeft(trimmedHeader[:closing], "[")
	name = strings.NewReplacer(" ", "", "\t", "", "\"", "", "'", "").Replace(name)
	return name, isArrayTable
}

// isAgentsRootKey reports a root-level `agents = ...` or `agents.x = ...`
// (bare or quoted), the layouts that cannot be edited safely line by line.
func isAgentsRootKey(trimmedLine string) bool {
	if strings.HasPrefix(trimmedLine, "#") {
		return false
	}
	key, _, found := strings.Cut(trimmedLine, "=")
	if !found {
		return false
	}
	key = strings.NewReplacer(" ", "", "\t", "", "\"", "", "'", "").Replace(key)
	return key == agentsTable || strings.HasPrefix(key, agentsTable+".")
}

func appendHeader(content, entries string) string {
	var result strings.Builder
	result.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		result.WriteString("\n")
	}
	if !strings.HasSuffix(content, "\n\n") {
		result.WriteString("\n")
	}
	result.WriteString(agentsHeader)
	result.WriteString(entries)
	return result.String()
}

func formatKey(name string) string {
	if bareKeyPattern.MatchString(name) {
		return name
	}
	return quoteString(name)
}

// quoteString renders a TOML basic string. strconv.Quote is not used because
// its Go-specific escapes (\x, \a, \v) are not valid TOML.
func quoteString(value string) string {
	var quoted strings.Builder
	quoted.WriteByte('"')
	for _, character := range value {
		switch {
		case character == '\\':
			quoted.WriteString(`\\`)
		case character == '"':
			quoted.WriteString(`\"`)
		case character == '\b':
			quoted.WriteString(`\b`)
		case character == '\t':
			quoted.WriteString(`\t`)
		case character == '\n':
			quoted.WriteString(`\n`)
		case character == '\f':
			quoted.WriteString(`\f`)
		case character == '\r':
			quoted.WriteString(`\r`)
		case character < asciiSpace || character == asciiDelete:
			fmt.Fprintf(&quoted, `\u%04X`, character)
		default:
			quoted.WriteRune(character)
		}
	}
	quoted.WriteByte('"')
	return quoted.String()
}
