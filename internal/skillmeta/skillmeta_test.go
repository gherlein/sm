package skillmeta

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkillFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestReadName_Valid(t *testing.T) {
	path := writeSkillFile(t, "---\nname: git-ops\ndescription: x\n---\nbody\n")
	name, err := ReadName(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "git-ops" {
		t.Fatalf("name = %q, want git-ops", name)
	}
}

func TestReadName_CRLF(t *testing.T) {
	path := writeSkillFile(t, "---\r\nname: git-ops\r\n---\r\nbody\r\n")
	name, err := ReadName(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "git-ops" {
		t.Fatalf("name = %q, want git-ops", name)
	}
}

func TestReadName_NoFrontmatter(t *testing.T) {
	path := writeSkillFile(t, "# just a heading\n")
	if _, err := ReadName(path); err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestReadName_Unterminated(t *testing.T) {
	path := writeSkillFile(t, "---\nname: git-ops\nbody\n")
	if _, err := ReadName(path); err == nil {
		t.Fatal("expected error for unterminated frontmatter")
	}
}

func TestReadName_MissingName(t *testing.T) {
	path := writeSkillFile(t, "---\ndescription: x\n---\nbody\n")
	if _, err := ReadName(path); err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestValidName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"git-ops", true},
		{"Foo", false},
		{"-x", false},
		{"x-", false},
		{"", false},
		{"a", true},
	}
	for _, testCase := range cases {
		if got := ValidName(testCase.name); got != testCase.want {
			t.Errorf("ValidName(%q) = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

func TestReadName_BOMAndTrailingSpace(t *testing.T) {
	path := writeSkillFile(t, "\xEF\xBB\xBF--- \t\nname: git-ops\n---  \nbody\n")
	name, err := ReadName(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "git-ops" {
		t.Fatalf("name = %q, want git-ops", name)
	}
}

func TestReadName_SizeCap(t *testing.T) {
	padding := strings.Repeat("x", MaxSkillFileBytes)
	path := writeSkillFile(t, "---\nname: git-ops\ndescription: "+padding+"\n---\n")
	if _, err := ReadName(path); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v, want size limit error", err)
	}
}

func TestReadName_InvalidYAMLMessage(t *testing.T) {
	path := writeSkillFile(t, "---\nname: x\ndescription: Use when: x\n---\n")
	_, err := ReadName(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	for _, want := range []string{path, "frontmatter must be valid YAML (quote values that contain \": \")"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}

func TestReadName_SymlinkedSkillMdToRegularFile(t *testing.T) {
	target := writeSkillFile(t, "---\nname: git-ops\n---\n")
	link := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	name, err := ReadName(link)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "git-ops" {
		t.Fatalf("name = %q, want git-ops", name)
	}
}
