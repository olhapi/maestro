package skills

import (
	"sort"
	"strings"
	"testing"
)

func TestBundledSkillPathsAndMetadata(t *testing.T) {
	paths, err := BundledPaths()
	if err != nil {
		t.Fatalf("BundledPaths failed: %v", err)
	}

	sort.Strings(paths)
	wantPaths := []string{
		"SKILL.md",
		"references/operations.md",
		"references/project-work.md",
		"references/readiness.md",
		"references/setup.md",
	}
	if len(paths) != len(wantPaths) {
		t.Fatalf("unexpected bundled path count: got %d want %d (%v)", len(paths), len(wantPaths), paths)
	}
	for i, want := range wantPaths {
		if paths[i] != want {
			t.Fatalf("unexpected bundled path at %d: got %q want %q", i, paths[i], want)
		}
	}

	data, err := ReadBundledFile("SKILL.md")
	if err != nil {
		t.Fatalf("ReadBundledFile(SKILL.md) failed: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"name: maestro",
		"description: Use Maestro to initialize workflows, run the local loop, bridge MCP, and manage projects, epics, issues, and readiness checks.",
		"# Maestro CLI Skill",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected SKILL.md to contain %q, got %q", want, text)
		}
	}
}
