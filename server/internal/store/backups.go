package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Backup kinds.
const (
	BackupScheduled  = "scheduled"
	BackupManual     = "manual"
	BackupPreRestore = "pre-restore"
)

type Backup struct {
	ID        int64
	VaultID   int64
	Rev       int64
	Kind      string
	CreatedAt time.Time
	FileCount int64
	SizeBytes int64
	ZipPath   string
}

const backupCols = "id, vault_id, rev, kind, created_at, file_count, size_bytes, zip_path"

func scanBackup(row interface{ Scan(...any) error }) (*Backup, error) {
	var b Backup
	var created int64
	if err := row.Scan(&b.ID, &b.VaultID, &b.Rev, &b.Kind, &created, &b.FileCount, &b.SizeBytes, &b.ZipPath); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	b.CreatedAt = time.Unix(created, 0)
	return &b, nil
}

// CreateBackup snapshots the current state of a vault. The snapshot is only
// a manifest (path -> content hash); content is shared with the blob store.
func (s *Store) CreateBackup(ctx context.Context, vaultID int64, kind string) (*Backup, error) {
	var id int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var head int64
		if err := tx.QueryRow("SELECT head_rev FROM vaults WHERE id = ?", vaultID).Scan(&head); err != nil {
			return err
		}
		res, err := tx.Exec("INSERT INTO backups(vault_id, rev, kind, created_at, file_count, size_bytes) VALUES(?, ?, ?, ?, 0, 0)",
			vaultID, head, kind, s.now())
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		if _, err := tx.Exec("INSERT INTO backup_files(backup_id, path, hash, size, mtime) SELECT ?, path, hash, size, mtime FROM files WHERE vault_id = ? AND deleted = 0",
			id, vaultID); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE backups SET file_count = (SELECT COUNT(*) FROM backup_files WHERE backup_id = ?1),
			size_bytes = (SELECT COALESCE(SUM(size), 0) FROM backup_files WHERE backup_id = ?1) WHERE id = ?1`, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Backup(ctx, id)
}

func (s *Store) Backup(ctx context.Context, id int64) (*Backup, error) {
	return scanBackup(s.db.QueryRowContext(ctx, "SELECT "+backupCols+" FROM backups WHERE id = ?", id))
}

func (s *Store) ListBackups(ctx context.Context, vaultID int64) ([]*Backup, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+backupCols+" FROM backups WHERE vault_id = ? ORDER BY created_at DESC, id DESC", vaultID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) BackupFiles(ctx context.Context, backupID int64) ([]FileEntry, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT path, hash, size, mtime FROM backup_files WHERE backup_id = ? ORDER BY path", backupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileEntry
	for rows.Next() {
		var f FileEntry
		if err := rows.Scan(&f.Path, &f.Hash, &f.Size, &f.Mtime); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) SetBackupZip(ctx context.Context, id int64, zipPath string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE backups SET zip_path = ? WHERE id = ?", zipPath, id)
	return err
}

func (s *Store) DeleteBackup(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM backups WHERE id = ?", id)
	return err
}

// ExpiredBackups returns backups of a vault created before cutoff. The newest
// backup is never returned, so a vault always keeps at least one.
func (s *Store) ExpiredBackups(ctx context.Context, vaultID int64, cutoff time.Time) ([]*Backup, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+backupCols+` FROM backups WHERE vault_id = ?1 AND created_at < ?2
		AND id != (SELECT id FROM backups WHERE vault_id = ?1 ORDER BY created_at DESC, id DESC LIMIT 1)`, vaultID, cutoff.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LatestBackup returns the newest backup of a vault, or ErrNotFound.
func (s *Store) LatestBackup(ctx context.Context, vaultID int64) (*Backup, error) {
	return scanBackup(s.db.QueryRowContext(ctx, "SELECT "+backupCols+" FROM backups WHERE vault_id = ? ORDER BY created_at DESC, id DESC LIMIT 1", vaultID))
}

// MarkBackupRun records when the scheduler last handled a vault and the error
// (empty on success).
func (s *Store) MarkBackupRun(ctx context.Context, vaultID int64, at time.Time, errMsg string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE vaults SET last_backup_at = ?, last_backup_error = ? WHERE id = ?", at.Unix(), errMsg, vaultID)
	return err
}
