package skills

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

type failingDirEntry struct{}

func (failingDirEntry) Name() string               { return "file.txt" }
func (failingDirEntry) IsDir() bool                { return false }
func (failingDirEntry) Type() fs.FileMode          { return 0 }
func (failingDirEntry) Info() (fs.FileInfo, error) { return nil, errors.New("info failed") }

type fakeFileInfo struct {
	name string
	mode fs.FileMode
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() interface{}   { return nil }

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

func TestInstallMaestroCopiesBundledSkillAtomically(t *testing.T) {
	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "skills", maestroSkillName)

	if err := InstallMaestro(dest); err != nil {
		t.Fatalf("InstallMaestro first install: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dest, "stale.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("WriteFile stale marker: %v", err)
	}

	if err := InstallMaestro(dest); err != nil {
		t.Fatalf("InstallMaestro second install: %v", err)
	}

	for _, rel := range []string{"SKILL.md", filepath.Join("references", "setup.md")} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err != nil {
			t.Fatalf("expected bundled file %s after install: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected stale file to be replaced, stat err=%v", err)
	}
}

func TestCopyFSPreservesStructureAndExecutableBits(t *testing.T) {
	tmpDir := t.TempDir()
	source := fstest.MapFS{
		"root.txt": {
			Data: []byte("root"),
			Mode: 0o755,
		},
		"nested/file.txt": {
			Data: []byte("nested"),
			Mode: 0o644,
		},
	}

	if err := copyFS(tmpDir, source); err != nil {
		t.Fatalf("copyFS: %v", err)
	}

	if data, err := os.ReadFile(filepath.Join(tmpDir, "root.txt")); err != nil || string(data) != "root" {
		t.Fatalf("expected root file to copy, data=%q err=%v", string(data), err)
	}
	if data, err := os.ReadFile(filepath.Join(tmpDir, "nested", "file.txt")); err != nil || string(data) != "nested" {
		t.Fatalf("expected nested file to copy, data=%q err=%v", string(data), err)
	}
	info, err := os.Stat(filepath.Join(tmpDir, "root.txt"))
	if err != nil {
		t.Fatalf("Stat executable file: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("expected executable bits to be preserved, got mode %v", info.Mode())
	}
}

func TestInstallTreeRejectsMissingBundle(t *testing.T) {
	err := installTree(filepath.Join(t.TempDir(), "skills", "missing"), "missing")
	if err == nil {
		t.Fatal("expected missing bundle to fail")
	}
}

func TestReadBundledFileSupportsLeadingSlashAndMissingFiles(t *testing.T) {
	data, err := ReadBundledFile("/SKILL.md")
	if err != nil {
		t.Fatalf("ReadBundledFile leading slash: %v", err)
	}
	if !strings.Contains(string(data), "# Maestro CLI Skill") {
		t.Fatalf("expected bundled skill content, got %q", string(data))
	}
	if _, err := ReadBundledFile("references/missing.md"); err == nil {
		t.Fatal("expected missing bundled file to fail")
	}
}

func TestBundledPathsAndReadBundledFileReportSubErrors(t *testing.T) {
	origSub := subFunc
	t.Cleanup(func() {
		subFunc = origSub
	})

	subFunc = func(fs.FS, string) (fs.FS, error) {
		return nil, errors.New("sub failed")
	}

	if _, err := BundledPaths(); err == nil {
		t.Fatal("expected BundledPaths to fail when bundle resolution fails")
	}
	if _, err := ReadBundledFile("SKILL.md"); err == nil {
		t.Fatal("expected ReadBundledFile to fail when bundle resolution fails")
	}
}

func TestCopyFSReportsDestinationPreparationError(t *testing.T) {
	tmp := t.TempDir()
	parentFile := filepath.Join(tmp, "blocked")
	if err := os.WriteFile(parentFile, []byte("file"), 0o644); err != nil {
		t.Fatalf("WriteFile parent file: %v", err)
	}

	err := copyFS(filepath.Join(parentFile, "nested"), fstest.MapFS{
		"file.txt": {Data: []byte("data")},
	})
	if err == nil {
		t.Fatal("expected copyFS to fail when destination parent is a file")
	}
}

func TestInstallTreeReportsDestinationPreparationError(t *testing.T) {
	tmp := t.TempDir()
	parentFile := filepath.Join(tmp, "blocked")
	if err := os.WriteFile(parentFile, []byte("file"), 0o644); err != nil {
		t.Fatalf("WriteFile parent file: %v", err)
	}

	err := installTree(filepath.Join(parentFile, "nested", "maestro"), maestroSkillName)
	if err == nil {
		t.Fatal("expected installTree to fail when destination parent is a file")
	}
}

func TestCopyFSReportsWalkReadAndChmodErrors(t *testing.T) {
	origWalk := walkDirFunc
	origRead := readFileFunc
	origChmod := chmodFunc
	t.Cleanup(func() {
		walkDirFunc = origWalk
		readFileFunc = origRead
		chmodFunc = origChmod
	})

	walkDirFunc = func(source fs.FS, root string, fn fs.WalkDirFunc) error {
		return errors.New("walk failed")
	}
	if err := copyFS(t.TempDir(), fstest.MapFS{"file.txt": {Data: []byte("data")}}); err == nil {
		t.Fatal("expected walk error to fail copyFS")
	}

	walkDirFunc = func(source fs.FS, root string, fn fs.WalkDirFunc) error {
		return fn("file.txt", failingDirEntry{}, nil)
	}
	readFileFunc = func(source fs.FS, name string) ([]byte, error) {
		return nil, errors.New("read failed")
	}
	if err := copyFS(t.TempDir(), fstest.MapFS{"file.txt": {Data: []byte("data")}}); err == nil {
		t.Fatal("expected read error to fail copyFS")
	}

	readFileFunc = func(source fs.FS, name string) ([]byte, error) {
		return []byte("data"), nil
	}
	chmodFunc = func(string, os.FileMode) error {
		return errors.New("chmod failed")
	}
	if err := copyFS(t.TempDir(), fstest.MapFS{"file.txt": {Data: []byte("data")}}); err == nil {
		t.Fatal("expected chmod error to fail copyFS")
	}
}

func TestInstallTreeReportsRenameFailures(t *testing.T) {
	origRename := renameFunc
	origLstat := lstatFunc
	origMkdirTemp := mkdirTempFunc
	t.Cleanup(func() {
		renameFunc = origRename
		lstatFunc = origLstat
		mkdirTempFunc = origMkdirTemp
	})

	mkdirTempFunc = func(parent, pattern string) (string, error) {
		return filepath.Join(parent, "tmp-install"), nil
	}
	lstatFunc = func(string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	renameFunc = func(oldpath, newpath string) error {
		return errors.New("rename failed")
	}
	if err := installTree(filepath.Join(t.TempDir(), "skills", maestroSkillName), maestroSkillName); err == nil {
		t.Fatal("expected rename failure to fail installTree")
	}

	renameCalls := 0
	lstatFunc = func(string) (os.FileInfo, error) {
		return fakeFileInfo{name: "maestro", mode: 0o755}, nil
	}
	renameFunc = func(oldpath, newpath string) error {
		renameCalls++
		switch renameCalls {
		case 1:
			return nil
		case 2:
			return errors.New("tmp rename failed")
		default:
			return errors.New("restore failed")
		}
	}
	if err := installTree(filepath.Join(t.TempDir(), "skills", maestroSkillName), maestroSkillName); err == nil {
		t.Fatal("expected restore failure to fail installTree")
	}
}

func TestBundledPathsReportsWalkErrors(t *testing.T) {
	origWalk := walkDirFunc
	t.Cleanup(func() {
		walkDirFunc = origWalk
	})

	walkDirFunc = func(source fs.FS, root string, fn fs.WalkDirFunc) error {
		return errors.New("walk failed")
	}

	if _, err := BundledPaths(); err == nil {
		t.Fatal("expected BundledPaths to fail when walking the bundle fails")
	}
}

func TestReadBundledFileReportsReadErrors(t *testing.T) {
	origRead := readFileFunc
	t.Cleanup(func() {
		readFileFunc = origRead
	})

	readFileFunc = func(source fs.FS, name string) ([]byte, error) {
		return nil, errors.New("read failed")
	}

	if _, err := ReadBundledFile("SKILL.md"); err == nil {
		t.Fatal("expected ReadBundledFile to fail when the embedded file read fails")
	}
}

func TestBundledPathsReportsWalkError(t *testing.T) {
	origWalk := walkDirFunc
	t.Cleanup(func() {
		walkDirFunc = origWalk
	})

	walkDirFunc = func(source fs.FS, root string, fn fs.WalkDirFunc) error {
		return errors.New("walk failed")
	}

	if _, err := BundledPaths(); err == nil {
		t.Fatal("expected BundledPaths to surface walk error")
	}
}

func TestInstallTreeReportsExistingDestinationBackupFailure(t *testing.T) {
	origRename := renameFunc
	origLstat := lstatFunc
	origMkdirTemp := mkdirTempFunc
	t.Cleanup(func() {
		renameFunc = origRename
		lstatFunc = origLstat
		mkdirTempFunc = origMkdirTemp
	})

	mkdirTempFunc = func(parent, pattern string) (string, error) {
		return filepath.Join(parent, "tmp-install"), nil
	}
	lstatFunc = func(string) (os.FileInfo, error) {
		return fakeFileInfo{name: "maestro", mode: 0o755}, nil
	}
	renameFunc = func(oldpath, newpath string) error { return errors.New("backup failed") }

	if err := installTree(filepath.Join(t.TempDir(), "skills", maestroSkillName), maestroSkillName); err == nil {
		t.Fatal("expected backup failure to fail installTree")
	}
}
