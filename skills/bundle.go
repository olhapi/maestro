package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const maestroSkillName = "maestro"

//go:embed maestro
var embedded embed.FS

// InstallMaestro copies the bundled Maestro skill into dest, replacing any
// existing installation atomically where the platform allows.
func InstallMaestro(dest string) error {
	return installTree(dest, maestroSkillName)
}

func installTree(dest string, bundleName string) error {
	root, err := fs.Sub(embedded, bundleName)
	if err != nil {
		return fmt.Errorf("resolve bundled skill %q: %w", bundleName, err)
	}

	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("prepare skill parent directory: %w", err)
	}

	tmpDir, err := os.MkdirTemp(parent, "."+filepath.Base(dest)+".tmp-")
	if err != nil {
		return fmt.Errorf("create temporary install directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := copyFS(tmpDir, root); err != nil {
		return err
	}

	backupDir := dest + ".bak"
	_ = os.RemoveAll(backupDir)
	hadBackup := false
	if _, err := os.Lstat(dest); err == nil {
		if err := os.Rename(dest, backupDir); err != nil {
			return fmt.Errorf("back up existing skill install: %w", err)
		}
		defer os.RemoveAll(backupDir)
		hadBackup = true
	}

	if err := os.Rename(tmpDir, dest); err != nil {
		if hadBackup {
			restoreErr := os.Rename(backupDir, dest)
			if restoreErr == nil {
				return fmt.Errorf("install bundled skill: %w", err)
			}
			return fmt.Errorf("install bundled skill: %w (and failed to restore previous install: %v)", err, restoreErr)
		}
		return fmt.Errorf("install bundled skill: %w", err)
	}

	return nil
}

func copyFS(dst string, source fs.FS) error {
	return fs.WalkDir(source, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}

		dstPath := filepath.Join(dst, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}

		data, err := fs.ReadFile(source, path)
		if err != nil {
			return err
		}

		mode := os.FileMode(0o644)
		if info, err := d.Info(); err == nil {
			mode = info.Mode().Perm()
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, mode); err != nil {
			return err
		}
		return nil
	})
}

// BundledPaths returns the relative file paths embedded in the Maestro skill.
// It is primarily useful for tests and sanity checks.
func BundledPaths() ([]string, error) {
	root, err := fs.Sub(embedded, maestroSkillName)
	if err != nil {
		return nil, err
	}

	var paths []string
	if err := fs.WalkDir(root, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		paths = append(paths, filepath.ToSlash(path))
		return nil
	}); err != nil {
		return nil, err
	}

	return paths, nil
}

// ReadBundledFile reads a file from the bundled Maestro skill.
func ReadBundledFile(relPath string) ([]byte, error) {
	root, err := fs.Sub(embedded, maestroSkillName)
	if err != nil {
		return nil, err
	}
	data, err := fs.ReadFile(root, filepath.ToSlash(strings.TrimPrefix(relPath, "/")))
	if err != nil {
		return nil, err
	}
	return data, nil
}
