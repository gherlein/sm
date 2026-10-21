// Package pathutil holds the path normalization and containment helpers shared
// by the sm and ms commands.
package pathutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	tildePrefix     = "~"
	parentDirectory = ".."

	dataHomeEnvName    = "XDG_DATA_HOME"
	defaultDataSubpath = ".local/share"

	// maxSymlinkHops mirrors the kernel's limit on followed links.
	maxSymlinkHops = 40

	// lastByteRune is the largest rune Printable escapes in the short \xNN
	// form; larger ones use \uNNNN.
	lastByteRune = 0xff
)

// ExpandTilde expands a leading "~" or "~/" to the user's home directory. It
// never expands environment variables: the shell already did that for
// command-line values, and doing it again silently re-roots paths. The "~user"
// form is rejected because it cannot be resolved portably.
func ExpandTilde(path string) (string, error) {
	if !strings.HasPrefix(path, tildePrefix) {
		return path, nil
	}
	rest := path[len(tildePrefix):]
	if rest != "" && rest[0] != filepath.Separator {
		return "", fmt.Errorf("path %q: the ~user form is not supported", path)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return home + rest, nil
}

// DataHome returns $XDG_DATA_HOME, or ~/.local/share when it is unset. Per
// the XDG specification a relative XDG_DATA_HOME is invalid and ignored, as is
// an empty one. The store, the state file and the ingest ledger all live under
// it, so they must agree on this one rule.
func DataHome() (string, error) {
	dataHome := os.Getenv(dataHomeEnvName)
	if dataHome != "" && filepath.IsAbs(dataHome) {
		return dataHome, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, defaultDataSubpath), nil
}

// Abs expands a leading tilde and returns a cleaned absolute path; a relative
// path resolves against the current directory.
func Abs(path string) (string, error) {
	expanded, err := ExpandTilde(path)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", path, err)
	}
	return filepath.Clean(absolute), nil
}

// ResolveExisting resolves symlinks in the longest existing ancestor of an
// absolute path and re-appends the not-yet-existing remainder, so a path that
// will be created later still compares equal to its symlink aliases. Any error
// other than "does not exist" (e.g. a symlink loop) propagates. A dangling
// symlink is followed by hand (EvalSymlinks reports it as not-exist and would
// drop the link name), so an alias of a missing target compares equal to it.
func ResolveExisting(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path %q is not absolute", path)
	}
	current := filepath.Clean(path)
	remainder := ""
	hops := 0
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			return filepath.Join(resolved, remainder), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("resolve %q: %w", current, err)
		}
		target, followed, err := followDanglingLink(current)
		if err != nil {
			return "", err
		}
		if followed {
			hops++
			if hops > maxSymlinkHops {
				return "", fmt.Errorf("resolve %q: too many levels of symbolic links", path)
			}
			current = target
			continue
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("resolve %q: no existing ancestor: %w", path, err)
		}
		remainder = filepath.Join(filepath.Base(current), remainder)
		current = parent
	}
}

// followDanglingLink returns the cleaned absolute target of path when path
// itself is a symlink; relative targets are relative to the link's directory.
func followDanglingLink(path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("resolve %q: %w", path, err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		return "", false, nil
	}
	target, err := os.Readlink(path)
	if err != nil {
		return "", false, fmt.Errorf("resolve %q: %w", path, err)
	}
	if !filepath.IsAbs(target) {
		// The kernel resolves a relative target against the link's real
		// directory, which differs from the lexical one when that directory is
		// reached through a symlink.
		directory, err := ResolveExisting(filepath.Dir(path))
		if err != nil {
			return "", false, err
		}
		target = filepath.Join(directory, target)
	}
	return filepath.Clean(target), true, nil
}

// IsSingleSegment reports whether name is one clean path component: not
// empty, not "." or "..", and free of both separators, so joining it to a
// directory can never name anything outside that directory. Both separators
// are refused on every platform because names travel between them in state
// files and stores.
func IsSingleSegment(name string) bool {
	return name != "" && name != "." && name != parentDirectory && !strings.ContainsAny(name, `/\`)
}

// Within reports whether target equals base or lies inside it. It is lexical,
// so callers resolve symlinks first.
func Within(base, target string) bool {
	relative, err := filepath.Rel(filepath.Clean(base), filepath.Clean(target))
	if err != nil {
		return false
	}
	// A name that merely starts with ".." (e.g. "..foo") is still inside.
	return relative != parentDirectory && !strings.HasPrefix(relative, parentDirectory+string(filepath.Separator))
}

// Overlaps reports whether either path equals or contains the other.
func Overlaps(first, second string) bool {
	return Within(first, second) || Within(second, first)
}

// Printable returns text with every control character, Unicode bidirectional
// control and invalid UTF-8 byte replaced by a visible Go-style escape (\x1b,
// \u202e), so a name or path taken from the filesystem cannot drive, clear or
// spoof the terminal it is printed to. Ordinary text passes through unchanged.
func Printable(text string) string {
	var builder strings.Builder
	for index := 0; index < len(text); {
		character, size := utf8.DecodeRuneInString(text[index:])
		switch {
		case character == utf8.RuneError && size == 1:
			fmt.Fprintf(&builder, `\x%02x`, text[index])
		case unicode.IsControl(character) || unicode.Is(unicode.Bidi_Control, character):
			if character <= lastByteRune {
				fmt.Fprintf(&builder, `\x%02x`, character)
			} else {
				fmt.Fprintf(&builder, `\u%04x`, character)
			}
		default:
			builder.WriteString(text[index : index+size])
		}
		index += size
	}
	return builder.String()
}

// PrintableLines is Printable applied to each line of text separately, so a
// multi-line message (a joined error, one failure per line) keeps its line
// breaks while every line is made terminal-safe.
func PrintableLines(text string) string {
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		lines[index] = Printable(line)
	}
	return strings.Join(lines, "\n")
}

// ShellQuote returns path as one POSIX shell word in single quotes, so a hint
// such as "chmod o-w <dir>" can be pasted as is even when the path holds
// spaces or quotes. Control characters are left for Printable to escape when
// the message is printed.
func ShellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}
