package kanban

import (
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const MaxIssueImageBytes int64 = 10 * 1024 * 1024

var issueImageExtensions = map[string]string{
	"image/gif":  ".gif",
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

func IssueImageAssetRoot(dbPath string) string {
	resolved := ResolveDBPath(dbPath)
	return filepath.Join(filepath.Dir(resolved), "assets", "images")
}

func (s *Store) IssueImageAssetRoot() string {
	return IssueImageAssetRoot(s.dbPath)
}

func issueImageExtension(contentType string) string {
	return issueImageExtensions[strings.TrimSpace(contentType)]
}

func isSupportedIssueImageContentType(contentType string) bool {
	_, ok := issueImageExtensions[strings.TrimSpace(contentType)]
	return ok
}

func normalizeIssueImageFilename(name, contentType string) string {
	base := strings.TrimSpace(filepath.Base(name))
	base = strings.ReplaceAll(base, string(filepath.Separator), "_")
	if base == "" || base == "." {
		base = "image" + issueImageExtension(contentType)
	}
	if filepath.Ext(base) == "" {
		base += issueImageExtension(contentType)
	}
	return base
}

func ensureIssueImageRoot(root string) error {
	return os.MkdirAll(root, 0o755)
}

func (s *Store) CreateIssueImage(issueID, originalFilename string, src io.Reader) (*IssueImage, error) {
	if strings.TrimSpace(issueID) == "" {
		return nil, validationErrorf("issue_id is required")
	}
	if src == nil {
		return nil, validationErrorf("image content is required")
	}
	if _, err := s.GetIssue(issueID); err != nil {
		if err == sql.ErrNoRows {
			return nil, notFoundError("issue", issueID)
		}
		return nil, err
	}

	root := s.IssueImageAssetRoot()
	tempPath, contentType, byteSize, err := writeIssueImageTempFile(root, src)
	if err != nil {
		return nil, err
	}
	defer func() {
		if strings.TrimSpace(tempPath) != "" {
			_ = os.Remove(tempPath)
		}
	}()

	now := time.Now().UTC()
	imageID := generateID("img")
	filename := normalizeIssueImageFilename(originalFilename, contentType)
	issueDir := filepath.Join(root, issueID)
	if err := os.MkdirAll(issueDir, 0o755); err != nil {
		return nil, err
	}
	finalPath := filepath.Join(issueDir, imageID+issueImageExtension(contentType))
	if err := os.Rename(tempPath, finalPath); err != nil {
		return nil, err
	}
	tempPath = ""

	relPath, err := filepath.Rel(root, finalPath)
	if err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}

	image := &IssueImage{
		ID:          imageID,
		IssueID:     issueID,
		Filename:    filename,
		ContentType: contentType,
		ByteSize:    byteSize,
		StoragePath: filepath.ToSlash(relPath),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	tx, err := s.db.Begin()
	if err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.Exec(`
		INSERT INTO issue_images (id, issue_id, filename, content_type, byte_size, storage_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		image.ID, image.IssueID, image.Filename, image.ContentType, image.ByteSize, image.StoragePath, image.CreatedAt, image.UpdatedAt,
	); err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}
	if err := s.appendChangeTx(tx, "issue_image", image.ID, "created", map[string]interface{}{"issue_id": issueID}); err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}
	if err := s.commitTx(tx, true); err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}
	tx = nil
	return image, nil
}

func (s *Store) ListIssueImages(issueID string) ([]IssueImage, error) {
	rows, err := s.db.Query(`
		SELECT id, issue_id, filename, content_type, byte_size, storage_path, created_at, updated_at
		FROM issue_images
		WHERE issue_id = ?
		ORDER BY created_at ASC, id ASC`, issueID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	images := []IssueImage{}
	for rows.Next() {
		image, err := scanIssueImage(rows)
		if err != nil {
			return nil, err
		}
		images = append(images, *image)
	}
	return images, rows.Err()
}

func (s *Store) GetIssueImage(issueID, imageID string) (*IssueImage, error) {
	row := s.db.QueryRow(`
		SELECT id, issue_id, filename, content_type, byte_size, storage_path, created_at, updated_at
		FROM issue_images
		WHERE issue_id = ? AND id = ?`, issueID, imageID,
	)
	image, err := scanIssueImage(row)
	if err == sql.ErrNoRows {
		return nil, notFoundError("issue_image", imageID)
	}
	if err != nil {
		return nil, err
	}
	return image, nil
}

func (s *Store) GetIssueImageContent(issueID, imageID string) (*IssueImage, string, error) {
	image, err := s.GetIssueImage(issueID, imageID)
	if err != nil {
		return nil, "", err
	}
	path, err := s.issueImagePath(image.StoragePath)
	if err != nil {
		return nil, "", err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, "", notFoundError("issue_image", imageID)
		}
		return nil, "", err
	}
	return image, path, nil
}

func (s *Store) DeleteIssueImage(issueID, imageID string) error {
	image, err := s.GetIssueImage(issueID, imageID)
	if err != nil {
		return err
	}
	path, err := s.issueImagePath(image.StoragePath)
	if err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	res, err := tx.Exec(`DELETE FROM issue_images WHERE issue_id = ? AND id = ?`, issueID, imageID)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("issue_image", imageID)
	}
	if err := s.appendChangeTx(tx, "issue_image", imageID, "deleted", map[string]interface{}{"issue_id": issueID}); err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil

	removeIssueImageFile(path)
	removeIfEmpty(filepath.Dir(path))
	return nil
}

func (s *Store) deleteIssueImagesTx(tx *sql.Tx, issueID string) ([]string, error) {
	rows, err := tx.Query(`SELECT storage_path FROM issue_images WHERE issue_id = ?`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	paths := []string{}
	for rows.Next() {
		var storagePath string
		if err := rows.Scan(&storagePath); err != nil {
			return nil, err
		}
		path, err := s.issueImagePath(storagePath)
		if err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM issue_images WHERE issue_id = ?`, issueID); err != nil {
		return nil, err
	}
	return paths, nil
}

func (s *Store) cleanupIssueImagePaths(paths []string) {
	seenDirs := map[string]struct{}{}
	for _, path := range paths {
		removeIssueImageFile(path)
		dir := filepath.Dir(path)
		if _, ok := seenDirs[dir]; ok {
			continue
		}
		seenDirs[dir] = struct{}{}
		removeIfEmpty(dir)
	}
}

func (s *Store) issueImagePath(storagePath string) (string, error) {
	root := s.IssueImageAssetRoot()
	fullPath := filepath.Join(root, filepath.FromSlash(strings.TrimSpace(storagePath)))
	rel, err := filepath.Rel(root, fullPath)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") {
		return "", validationErrorf("invalid stored image path")
	}
	return fullPath, nil
}

type issueImageScanner interface {
	Scan(dest ...interface{}) error
}

func scanIssueImage(scanner issueImageScanner) (*IssueImage, error) {
	image := &IssueImage{}
	if err := scanner.Scan(
		&image.ID,
		&image.IssueID,
		&image.Filename,
		&image.ContentType,
		&image.ByteSize,
		&image.StoragePath,
		&image.CreatedAt,
		&image.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return image, nil
}

func writeIssueImageTempFile(root string, src io.Reader) (string, string, int64, error) {
	if err := ensureIssueImageRoot(root); err != nil {
		return "", "", 0, err
	}
	tempFile, err := os.CreateTemp(root, ".issue-image-*")
	if err != nil {
		return "", "", 0, err
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
	}()

	header := make([]byte, 512)
	n, readErr := src.Read(header)
	if readErr != nil && readErr != io.EOF {
		_ = os.Remove(tempPath)
		return "", "", 0, readErr
	}
	if n == 0 {
		_ = os.Remove(tempPath)
		return "", "", 0, validationErrorf("image file is empty")
	}
	if _, err := tempFile.Write(header[:n]); err != nil {
		_ = os.Remove(tempPath)
		return "", "", 0, err
	}
	contentType := http.DetectContentType(header[:n])
	if !isSupportedIssueImageContentType(contentType) {
		_ = os.Remove(tempPath)
		return "", "", 0, validationErrorf("unsupported image content type %q", contentType)
	}

	size := int64(n)
	if size > MaxIssueImageBytes {
		_ = os.Remove(tempPath)
		return "", "", 0, validationErrorf("image file exceeds %d bytes", MaxIssueImageBytes)
	}
	if readErr != io.EOF {
		remainingLimit := MaxIssueImageBytes - size + 1
		copied, err := io.Copy(tempFile, io.LimitReader(src, remainingLimit))
		if err != nil {
			_ = os.Remove(tempPath)
			return "", "", 0, err
		}
		size += copied
		if size > MaxIssueImageBytes {
			_ = os.Remove(tempPath)
			return "", "", 0, validationErrorf("image file exceeds %d bytes", MaxIssueImageBytes)
		}
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", "", 0, err
	}
	return tempPath, contentType, size, nil
}

func removeIssueImageFile(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return
	}
}

func removeIfEmpty(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) > 0 {
		return
	}
	_ = os.Remove(path)
}
