package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jirkacepelka/obsisync/server/internal/pathutil"
)

// FileEntry is the state of one path as exchanged with clients.
// Mtime is in milliseconds (what Obsidian uses).
type FileEntry struct {
	Path    string `json:"path"`
	Hash    string `json:"hash"`
	Size    int64  `json:"size"`
	Mtime   int64  `json:"mtime"`
	Deleted bool   `json:"deleted"`
	Rev     int64  `json:"rev"`
}

// Op is a single change a client wants to commit. BaseHash is the hash the
// client last saw for the path ("" = did not exist); the op only applies if
// it still matches the server (compare-and-swap).
type Op struct {
	Path     string `json:"path"`
	Hash     string `json:"hash"`
	Size     int64  `json:"size"`
	Mtime    int64  `json:"mtime"`
	Deleted  bool   `json:"deleted"`
	BaseHash string `json:"base_hash"`
	force    bool
}

// ForceOp builds an op that overwrites whatever is on the server (used for
// restores from the web UI).
func ForceOp(e FileEntry) Op {
	return Op{Path: e.Path, Hash: e.Hash, Size: e.Size, Mtime: e.Mtime, Deleted: e.Deleted, force: true}
}

type OpResult struct {
	Path    string     `json:"path"`
	OK      bool       `json:"ok"`
	Rev     int64      `json:"rev,omitempty"`
	Error   string     `json:"error,omitempty"`
	Current *FileEntry `json:"current,omitempty"`
}

// Commit error codes returned per op.
const (
	ErrCodeConflict     = "conflict"      // base_hash does not match the server
	ErrCodeCaseConflict = "case_conflict" // another path differs only by letter case
	ErrCodeMissingBlob  = "missing_blob"  // content was not uploaded
	ErrCodeInvalidPath  = "invalid_path"
	ErrCodeInvalid      = "invalid"
)

// Author identifies who made a change.
type Author struct {
	DeviceID int64  // 0 for changes made through the web UI
	Name     string // "user (device)" shown in history
}

// Commit applies ops atomically in one transaction; each op succeeds or fails
// on its own. hasBlob checks that uploaded content exists.
func (s *Store) Commit(ctx context.Context, vaultID int64, ops []Op, author Author, hasBlob func(string) bool) ([]OpResult, int64, error) {
	results := make([]OpResult, len(ops))
	var head int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRow("SELECT head_rev FROM vaults WHERE id = ?", vaultID).Scan(&head); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		now := s.now()
		for i, op := range ops {
			res := &results[i]
			path, err := pathutil.Normalize(op.Path)
			res.Path = op.Path
			if err != nil {
				res.Error = ErrCodeInvalidPath
				continue
			}
			res.Path = path
			if op.Deleted {
				op.Hash, op.Size = "", 0
			} else if !validHash(op.Hash) || op.Size < 0 {
				res.Error = ErrCodeInvalid
				continue
			}
			cur, err := fileTx(tx, vaultID, path)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			curHash := ""
			if cur != nil && !cur.Deleted {
				curHash = cur.Hash
			}
			if !op.force && op.BaseHash != curHash {
				res.Error, res.Current = ErrCodeConflict, cur
				continue
			}
			if op.Hash == curHash {
				// Nothing changes (e.g. a retried request); report success.
				res.OK = true
				if cur != nil {
					res.Rev = cur.Rev
				}
				continue
			}
			if !op.Deleted {
				if !hasBlob(op.Hash) {
					res.Error = ErrCodeMissingBlob
					continue
				}
				if curHash == "" {
					var other string
					err := tx.QueryRow("SELECT path FROM files WHERE vault_id = ? AND path_fold = ? AND path != ? AND deleted = 0",
						vaultID, pathutil.Fold(path), path).Scan(&other)
					if err == nil {
						res.Error = ErrCodeCaseConflict
						res.Current = &FileEntry{Path: other}
						continue
					} else if !errors.Is(err, sql.ErrNoRows) {
						return err
					}
				}
			}
			head++
			var dev any
			if author.DeviceID != 0 {
				dev = author.DeviceID
			}
			if _, err := tx.Exec(`INSERT INTO files(vault_id, path, path_fold, hash, size, mtime, deleted, rev, device_id, updated_at)
				VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(vault_id, path) DO UPDATE SET hash = excluded.hash, size = excluded.size, mtime = excluded.mtime,
				deleted = excluded.deleted, rev = excluded.rev, device_id = excluded.device_id, updated_at = excluded.updated_at`,
				vaultID, path, pathutil.Fold(path), op.Hash, op.Size, op.Mtime, boolInt(op.Deleted), head, dev, now); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO file_versions(vault_id, path, hash, size, mtime, deleted, rev, device_id, author, created_at)
				VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				vaultID, path, op.Hash, op.Size, op.Mtime, boolInt(op.Deleted), head, dev, author.Name, now); err != nil {
				return err
			}
			res.OK, res.Rev = true, head
		}
		_, err := tx.Exec("UPDATE vaults SET head_rev = ? WHERE id = ?", head, vaultID)
		return err
	})
	return results, head, err
}

func validHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for _, c := range h {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

const fileCols = "path, hash, size, mtime, deleted, rev"

func scanFile(row interface{ Scan(...any) error }) (*FileEntry, error) {
	var f FileEntry
	if err := row.Scan(&f.Path, &f.Hash, &f.Size, &f.Mtime, &f.Deleted, &f.Rev); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &f, nil
}

func fileTx(tx *sql.Tx, vaultID int64, path string) (*FileEntry, error) {
	return scanFile(tx.QueryRow("SELECT "+fileCols+" FROM files WHERE vault_id = ? AND path = ?", vaultID, path))
}

func (s *Store) File(ctx context.Context, vaultID int64, path string) (*FileEntry, error) {
	return scanFile(s.db.QueryRowContext(ctx, "SELECT "+fileCols+" FROM files WHERE vault_id = ? AND path = ?", vaultID, path))
}

// Changes returns entries changed after rev `since`, oldest first, at most
// limit of them, and the vault head revision. When more is true the client
// should continue from the last returned entry's rev.
func (s *Store) Changes(ctx context.Context, vaultID, since int64, limit int) (entries []FileEntry, head int64, more bool, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRow("SELECT head_rev FROM vaults WHERE id = ?", vaultID).Scan(&head); err != nil {
			return err
		}
		rows, err := tx.Query("SELECT "+fileCols+" FROM files WHERE vault_id = ? AND rev > ? ORDER BY rev LIMIT ?", vaultID, since, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			f, err := scanFile(rows)
			if err != nil {
				return err
			}
			entries = append(entries, *f)
		}
		return rows.Err()
	})
	if len(entries) > limit {
		entries, more = entries[:limit], true
	}
	return
}

// ListFiles returns the current files of a vault (optionally only deleted ones).
func (s *Store) ListFiles(ctx context.Context, vaultID int64, deleted bool) ([]FileEntry, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+fileCols+" FROM files WHERE vault_id = ? AND deleted = ? ORDER BY path", vaultID, boolInt(deleted))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileEntry
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

type Version struct {
	FileEntry
	ID        int64
	Author    string
	CreatedAt time.Time
}

func (s *Store) Versions(ctx context.Context, vaultID int64, path string) ([]Version, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, "+fileCols+", author, created_at FROM file_versions WHERE vault_id = ? AND path = ? ORDER BY rev DESC", vaultID, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		var v Version
		var created int64
		if err := rows.Scan(&v.ID, &v.Path, &v.Hash, &v.Size, &v.Mtime, &v.Deleted, &v.Rev, &v.Author, &created); err != nil {
			return nil, err
		}
		v.CreatedAt = time.Unix(created, 0)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) Version(ctx context.Context, vaultID, id int64) (*Version, error) {
	var v Version
	var created int64
	err := s.db.QueryRowContext(ctx, "SELECT id, "+fileCols+", author, created_at FROM file_versions WHERE vault_id = ? AND id = ?", vaultID, id).
		Scan(&v.ID, &v.Path, &v.Hash, &v.Size, &v.Mtime, &v.Deleted, &v.Rev, &v.Author, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	v.CreatedAt = time.Unix(created, 0)
	return &v, err
}

// HashInVault reports whether content hash belongs to vault (current file,
// an old version or a backup) - clients may only download such content.
func (s *Store) HashInVault(ctx context.Context, vaultID int64, hash string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 WHERE
		EXISTS (SELECT 1 FROM file_versions WHERE vault_id = ?1 AND hash = ?2) OR
		EXISTS (SELECT 1 FROM files WHERE vault_id = ?1 AND hash = ?2) OR
		EXISTS (SELECT 1 FROM backup_files bf JOIN backups b ON b.id = bf.backup_id WHERE b.vault_id = ?1 AND bf.hash = ?2)`,
		vaultID, hash).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// PruneVersions deletes file versions older than cutoff, except the version
// each file is currently at.
func (s *Store) PruneVersions(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM file_versions WHERE created_at < ? AND NOT EXISTS (
		SELECT 1 FROM files f WHERE f.vault_id = file_versions.vault_id AND f.path = file_versions.path AND f.rev = file_versions.rev)`,
		cutoff.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// LiveHashes returns every content hash still referenced by any vault.
func (s *Store) LiveHashes(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT hash FROM files WHERE deleted = 0
		UNION SELECT hash FROM file_versions WHERE deleted = 0
		UNION SELECT hash FROM backup_files`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out[h] = true
	}
	return out, rows.Err()
}
