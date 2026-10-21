// Package skillmeta reads SKILL.md frontmatter and validates skill names. It
// is shared by the store and ingest packages so both apply the same rules.
package skillmeta

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

const (
	// NamePattern is the single source of truth for legal skill names.
	NamePattern          = "^[a-z0-9]([a-z0-9-]*[a-z0-9])?$"
	skillFileName        = "SKILL.md"
	frontmatterDelimiter = "---"
	yamlHint             = "frontmatter must be valid YAML (quote values that contain \": \")"

	// MaxSkillFileBytes caps how much of a SKILL.md is read.
	MaxSkillFileBytes = 1 << 20
)

var byteOrderMark = []byte{0xEF, 0xBB, 0xBF}

var nameRegexp = regexp.MustCompile(NamePattern)

type frontmatter struct {
	Name string `yaml:"name"`
}

// ValidName reports whether name is a legal skill name.
func ValidName(name string) bool {
	return nameRegexp.MatchString(name)
}

// ReadName returns the name declared in the YAML frontmatter of the SKILL.md
// file at skillMdPath (see ReadNameInfo).
func ReadName(skillMdPath string) (string, error) {
	name, _, err := ReadNameInfo(skillMdPath)
	return name, err
}

// ReadNameInfo is ReadName that also returns the os.FileInfo of the file it
// actually read, taken from the open handle: a caller that later uses the
// manifest again (sm --copy dereferencing a symlinked SKILL.md) can require
// it to be that same file. Symlinks are followed but the final target must be
// a regular file no larger than MaxSkillFileBytes: reading a FIFO would hang
// the tool and reading /dev/zero would exhaust memory. A leading UTF-8 BOM and
// trailing spaces, tabs or CR on delimiter lines are tolerated because editors
// produce them invisibly.
func ReadNameInfo(skillMdPath string) (name string, info os.FileInfo, err error) {
	pathInfo, err := os.Stat(skillMdPath)
	if err != nil {
		return "", nil, fmt.Errorf("cannot read %s: %w", skillFileName, err)
	}
	if !pathInfo.Mode().IsRegular() {
		return "", nil, fmt.Errorf("%s at %s is not a regular file", skillFileName, skillMdPath)
	}
	file, err := openNonBlocking(skillMdPath)
	if err != nil {
		return "", nil, fmt.Errorf("cannot read %s: %w", skillFileName, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			name, info, err = "", nil, errors.Join(err, fmt.Errorf("close %s: %w", skillMdPath, closeErr))
		}
	}()
	return ReadNameFromFile(file, skillMdPath)
}

// ReadNameFromFile parses the frontmatter name from an already open SKILL.md
// (opened with ReadOnlyNonBlocking) and returns the handle's os.FileInfo.
// Only the open handle is trusted: the path may have been swapped since any
// earlier check, so the handle must be a regular file within the size cap.
// displayPath names the file in messages. The caller closes file.
func ReadNameFromFile(file *os.File, displayPath string) (string, os.FileInfo, error) {
	openInfo, err := file.Stat()
	if err != nil {
		return "", nil, fmt.Errorf("cannot read %s: %w", skillFileName, err)
	}
	if !openInfo.Mode().IsRegular() {
		return "", nil, fmt.Errorf("%s at %s is not a regular file", skillFileName, displayPath)
	}
	content, err := io.ReadAll(io.LimitReader(file, MaxSkillFileBytes+1))
	if err != nil {
		return "", nil, fmt.Errorf("cannot read %s: %w", skillFileName, err)
	}
	if len(content) > MaxSkillFileBytes {
		return "", nil, fmt.Errorf("%s at %s exceeds the %d byte limit", skillFileName, displayPath, MaxSkillFileBytes)
	}
	name, err := parseName(content, displayPath)
	if err != nil {
		return "", nil, err
	}
	return name, openInfo, nil
}

// parseName extracts the frontmatter name from a SKILL.md's content.
func parseName(content []byte, displayPath string) (string, error) {
	content = bytes.TrimPrefix(content, byteOrderMark)
	lines := bytes.Split(content, []byte("\n"))
	if !isDelimiter(lines[0]) {
		return "", fmt.Errorf("%s has no frontmatter", skillFileName)
	}
	for index := 1; index < len(lines); index++ {
		if !isDelimiter(lines[index]) {
			continue
		}
		var parsed frontmatter
		block := bytes.Join(lines[1:index], []byte("\n"))
		if err := yaml.Unmarshal(block, &parsed); err != nil {
			return "", fmt.Errorf("invalid frontmatter in %s: %w; %s", displayPath, err, yamlHint)
		}
		if parsed.Name == "" {
			return "", errors.New(skillFileName + " frontmatter has no name")
		}
		return parsed.Name, nil
	}
	return "", fmt.Errorf("%s frontmatter is not terminated", skillFileName)
}

func isDelimiter(line []byte) bool {
	return string(bytes.TrimRight(line, " \t\r")) == frontmatterDelimiter
}
