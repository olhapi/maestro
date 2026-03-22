package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/olhapi/maestro/skills"
)

func TestInstallSkillsWritesAndReplacesBundle(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	code, stdout, stderr := runCLI(t, "install", "--skills")
	if code != 0 {
		t.Fatalf("install --skills failed: %d stderr=%s stdout=%s", code, stderr, stdout)
	}

	targets := []string{
		filepath.Join(homeDir, ".agents", "skills", "maestro"),
		filepath.Join(homeDir, ".claude", "skills", "maestro"),
	}
	for _, want := range []string{
		"Installed Maestro skill bundle:",
		targets[0],
		targets[1],
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected install output to contain %q, got %q", want, stdout)
		}
	}

	paths, err := skills.BundledPaths()
	if err != nil {
		t.Fatalf("bundled paths: %v", err)
	}
	sort.Strings(paths)

	for _, target := range targets {
		for _, rel := range paths {
			want, err := skills.ReadBundledFile(rel)
			if err != nil {
				t.Fatalf("read bundled file %q: %v", rel, err)
			}
			installedPath := filepath.Join(target, filepath.FromSlash(rel))
			got, err := os.ReadFile(installedPath)
			if err != nil {
				t.Fatalf("read installed file %q: %v", rel, err)
			}
			if string(got) != string(want) {
				t.Fatalf("installed file %q mismatch for %s", rel, target)
			}
			info, err := os.Stat(installedPath)
			if err != nil {
				t.Fatalf("stat installed file %q: %v", rel, err)
			}
			if info.Mode().Perm()&0o200 == 0 {
				t.Fatalf("expected installed file %q to be user-writable, mode=%v", rel, info.Mode().Perm())
			}
		}
	}

	staleFile := filepath.Join(targets[0], "stale.txt")
	if err := os.WriteFile(staleFile, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}
	corruptedSkill := filepath.Join(targets[0], "SKILL.md")
	if err := os.Chmod(corruptedSkill, 0o644); err != nil {
		t.Fatalf("make SKILL.md writable: %v", err)
	}
	if err := os.WriteFile(corruptedSkill, []byte("corrupted"), 0o644); err != nil {
		t.Fatalf("corrupt installed skill: %v", err)
	}

	code, stdout, stderr = runCLI(t, "install", "--skills")
	if code != 0 {
		t.Fatalf("second install --skills failed: %d stderr=%s stdout=%s", code, stderr, stdout)
	}

	if _, err := os.Stat(staleFile); !os.IsNotExist(err) {
		t.Fatalf("expected stale file to be removed, got err=%v", err)
	}

	wantSkill, err := skills.ReadBundledFile("SKILL.md")
	if err != nil {
		t.Fatalf("read bundled SKILL.md: %v", err)
	}
	gotSkill, err := os.ReadFile(corruptedSkill)
	if err != nil {
		t.Fatalf("read restored SKILL.md: %v", err)
	}
	if string(gotSkill) != string(wantSkill) {
		t.Fatalf("expected reinstall to restore SKILL.md contents")
	}
}

func TestInstallHelpIncludesSkillsFlag(t *testing.T) {
	code, stdout, stderr := runCLI(t, "install", "--help")
	if code != 0 {
		t.Fatalf("install --help failed: %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	for _, want := range []string{
		"--skills",
		"Install the bundled Maestro skill",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected install help to contain %q, got %q", want, stdout)
		}
	}
}
