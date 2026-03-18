package kanban

import (
	"database/sql"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const MaxIssueAssetBytes int64 = 100 * 1024 * 1024

func IssueAssetRoot(dbPath string) string {
	resolved := ResolveDBPath(dbPath)
	return filepath.Join(filepath.Dir(resolved), "assets", "issues")
}

func (s *Store) IssueAssetRoot() string {
	return IssueAssetRoot(s.dbPath)
}

func normalizeIssueAssetFilename(name string) string {
	base := strings.TrimSpace(filepath.Base(name))
	base = strings.ReplaceAll(base, string(filepath.Separator), "_")
	if base == "" || base == "." {
		return "asset"
	}
	return base
}

func ensureIssueAssetRoot(root string) error {
	return os.MkdirAll(root, 0o755)
}

func (s *Store) CreateIssueAsset(issueID, originalFilename string, src io.Reader) (*IssueAsset, error) {
	if strings.TrimSpace(issueID) == "" {
		return nil, validationErrorf("issue_id is required")
	}
	if src == nil {
		return nil, validationErrorf("asset content is required")
	}
	if _, err := s.GetIssue(issueID); err != nil {
		if err == sql.ErrNoRows {
			return nil, notFoundError("issue", issueID)
		}
		return nil, err
	}

	root := s.IssueAssetRoot()
	filename := normalizeIssueAssetFilename(originalFilename)
	contentType, byteSize, tempPath, err := writeIssueAssetTempFile(root, src, filename)
	if err != nil {
		return nil, err
	}
	defer func() {
		if strings.TrimSpace(tempPath) != "" {
			_ = os.Remove(tempPath)
		}
	}()

	now := time.Now().UTC()
	assetID := generateID("ast")
	issueDir := filepath.Join(root, issueID)
	if err := os.MkdirAll(issueDir, 0o755); err != nil {
		return nil, err
	}
	ext := filepath.Ext(filename)
	if ext == "" {
		if guessed, err := mime.ExtensionsByType(contentType); err == nil && len(guessed) > 0 {
			ext = guessed[0]
		}
	}
	finalPath := filepath.Join(issueDir, assetID+ext)
	if err := os.Rename(tempPath, finalPath); err != nil {
		return nil, err
	}
	tempPath = ""

	relPath, err := filepath.Rel(root, finalPath)
	if err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}

	asset := &IssueAsset{
		ID:          assetID,
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
		INSERT INTO issue_assets (id, issue_id, filename, content_type, byte_size, storage_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		asset.ID, asset.IssueID, asset.Filename, asset.ContentType, asset.ByteSize, asset.StoragePath, asset.CreatedAt, asset.UpdatedAt,
	); err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}
	if err := s.appendChangeTx(tx, "issue_asset", asset.ID, "created", map[string]interface{}{"issue_id": issueID}); err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}
	if err := s.commitTx(tx, true); err != nil {
		_ = os.Remove(finalPath)
		return nil, err
	}
	tx = nil
	return asset, nil
}

func (s *Store) ListIssueAssets(issueID string) ([]IssueAsset, error) {
	rows, err := s.db.Query(`
		SELECT id, issue_id, filename, content_type, byte_size, storage_path, created_at, updated_at
		FROM issue_assets
		WHERE issue_id = ?
		ORDER BY created_at ASC, id ASC`, issueID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	assets := []IssueAsset{}
	for rows.Next() {
		asset, err := scanIssueAsset(rows)
		if err != nil {
			return nil, err
		}
		assets = append(assets, *asset)
	}
	return assets, rows.Err()
}

func (s *Store) GetIssueAsset(issueID, assetID string) (*IssueAsset, error) {
	row := s.db.QueryRow(`
		SELECT id, issue_id, filename, content_type, byte_size, storage_path, created_at, updated_at
		FROM issue_assets
		WHERE issue_id = ? AND id = ?`, issueID, assetID,
	)
	asset, err := scanIssueAsset(row)
	if err == sql.ErrNoRows {
		return nil, notFoundError("issue_asset", assetID)
	}
	if err != nil {
		return nil, err
	}
	return asset, nil
}

func (s *Store) GetIssueAssetContent(issueID, assetID string) (*IssueAsset, string, error) {
	asset, err := s.GetIssueAsset(issueID, assetID)
	if err != nil {
		return nil, "", err
	}
	path, err := s.issueAssetPath(asset.StoragePath)
	if err != nil {
		return nil, "", err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, "", notFoundError("issue_asset", assetID)
		}
		return nil, "", err
	}
	return asset, path, nil
}

func (s *Store) DeleteIssueAsset(issueID, assetID string) error {
	asset, err := s.GetIssueAsset(issueID, assetID)
	if err != nil {
		return err
	}
	path, err := s.issueAssetPath(asset.StoragePath)
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
	res, err := tx.Exec(`DELETE FROM issue_assets WHERE issue_id = ? AND id = ?`, issueID, assetID)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("issue_asset", assetID)
	}
	if err := s.appendChangeTx(tx, "issue_asset", assetID, "deleted", map[string]interface{}{"issue_id": issueID}); err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil

	removeIssueAssetFile(path)
	removeIfEmpty(filepath.Dir(path))
	return nil
}

func (s *Store) deleteIssueAssetsTx(tx *sql.Tx, issueID string) ([]string, error) {
	rows, err := tx.Query(`SELECT storage_path FROM issue_assets WHERE issue_id = ?`, issueID)
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
		path, err := s.issueAssetPath(storagePath)
		if err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM issue_assets WHERE issue_id = ?`, issueID); err != nil {
		return nil, err
	}
	return paths, nil
}

func (s *Store) cleanupIssueAssetPaths(paths []string) {
	seenDirs := map[string]struct{}{}
	for _, path := range paths {
		removeIssueAssetFile(path)
		dir := filepath.Dir(path)
		if _, ok := seenDirs[dir]; ok {
			continue
		}
		seenDirs[dir] = struct{}{}
		removeIfEmpty(dir)
	}
}

func (s *Store) issueAssetPath(storagePath string) (string, error) {
	root := s.IssueAssetRoot()
	fullPath := filepath.Join(root, filepath.FromSlash(strings.TrimSpace(storagePath)))
	rel, err := filepath.Rel(root, fullPath)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") {
		return "", validationErrorf("invalid stored asset path")
	}
	return fullPath, nil
}

type issueAssetScanner interface {
	Scan(dest ...interface{}) error
}

func scanIssueAsset(scanner issueAssetScanner) (*IssueAsset, error) {
	asset := &IssueAsset{}
	if err := scanner.Scan(
		&asset.ID,
		&asset.IssueID,
		&asset.Filename,
		&asset.ContentType,
		&asset.ByteSize,
		&asset.StoragePath,
		&asset.CreatedAt,
		&asset.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return asset, nil
}

func writeIssueAssetTempFile(root string, src io.Reader, filename string) (string, int64, string, error) {
	if err := ensureIssueAssetRoot(root); err != nil {
		return "", 0, "", err
	}
	tempFile, err := os.CreateTemp(root, ".issue-asset-*")
	if err != nil {
		return "", 0, "", err
	}
	defer tempFile.Close()

	limited := io.LimitReader(src, MaxIssueAssetBytes+1)
	sniff := &limitedBuffer{limit: 512}
	written, err := io.Copy(io.MultiWriter(tempFile, sniff), limited)
	if err != nil {
		return "", 0, "", err
	}
	if written == 0 {
		_ = os.Remove(tempFile.Name())
		return "", 0, "", validationErrorf("asset file is empty")
	}
	if written > MaxIssueAssetBytes {
		_ = os.Remove(tempFile.Name())
		return "", 0, "", validationErrorf("issue asset %s exceeds %d bytes", filename, MaxIssueAssetBytes)
	}

	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	if strings.TrimSpace(contentType) == "" {
		contentType = http.DetectContentType(sniff.Bytes())
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/octet-stream"
	}
	return contentType, written, tempFile.Name(), nil
}

func removeIssueAssetFile(path string) {
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
