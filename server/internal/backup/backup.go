// Package backup takes scheduled vault snapshots, enforces retention, restores
// snapshots and runs storage maintenance (version pruning, blob GC).
package backup

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/jirkacepelka/obsisync/server/internal/blobs"
	"github.com/jirkacepelka/obsisync/server/internal/hub"
	"github.com/jirkacepelka/obsisync/server/internal/store"
)

type Service struct {
	Store *store.Store
	Blobs *blobs.Store
	Hub   *hub.Hub
	Dir   string // where ZIP backups are written
	Now   func() time.Time
	Log   *slog.Logger

	mu              sync.Mutex // one backup/restore at a time
	lastMaintenance time.Time
}

// Run checks every minute for due backups until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick performs one scheduler pass: due backups, retention and (daily)
// maintenance.
func (s *Service) Tick(ctx context.Context) {
	now := s.Now()
	vaults, err := s.Store.ListVaults(ctx)
	if err != nil {
		s.Log.Error("backup: list vaults", "err", err)
		return
	}
	for _, v := range vaults {
		if v.Backup.IntervalSec > 0 && (v.LastBackupAt == nil || now.Sub(*v.LastBackupAt) >= time.Duration(v.Backup.IntervalSec)*time.Second) {
			msg := ""
			if _, err := s.scheduled(ctx, v); err != nil {
				msg = err.Error()
				s.Log.Error("backup failed", "vault", v.Name, "err", err)
			}
			s.Store.MarkBackupRun(ctx, v.ID, now, msg)
		}
		if v.Backup.RetentionDays > 0 {
			if err := s.expire(ctx, v, now.Add(-time.Duration(v.Backup.RetentionDays)*24*time.Hour)); err != nil {
				s.Log.Error("backup retention", "vault", v.Name, "err", err)
			}
		}
	}
	if now.Sub(s.lastMaintenance) >= 24*time.Hour {
		s.lastMaintenance = now
		s.Maintenance(ctx)
	}
}

// scheduled creates a backup unless nothing changed since the last one.
// It returns nil, nil when skipped.
func (s *Service) scheduled(ctx context.Context, v *store.Vault) (*store.Backup, error) {
	if last, err := s.Store.LatestBackup(ctx, v.ID); err == nil && last.Rev == v.HeadRev {
		return nil, nil
	}
	return s.Create(ctx, v.ID, store.BackupScheduled)
}

// Create snapshots a vault now and writes the ZIP copy if the vault wants one.
func (s *Service) Create(ctx context.Context, vaultID int64, kind string) (*store.Backup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.create(ctx, vaultID, kind)
}

func (s *Service) create(ctx context.Context, vaultID int64, kind string) (*store.Backup, error) {
	v, err := s.Store.Vault(ctx, vaultID)
	if err != nil {
		return nil, err
	}
	b, err := s.Store.CreateBackup(ctx, vaultID, kind)
	if err != nil {
		return nil, err
	}
	if v.Backup.Zip {
		p, err := s.writeZipFile(ctx, v, b)
		if err != nil {
			return b, fmt.Errorf("ZIP backup: %w", err)
		}
		b.ZipPath = p
		s.Store.SetBackupZip(ctx, b.ID, p)
	}
	return b, nil
}

var unsafeChars = regexp.MustCompile(`[^\p{L}\p{N}._-]+`)

func (s *Service) writeZipFile(ctx context.Context, v *store.Vault, b *store.Backup) (string, error) {
	dir := filepath.Join(s.Dir, fmt.Sprintf("%d-%s", v.ID, unsafeChars.ReplaceAllString(v.Name, "_")))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := filepath.Join(dir, b.CreatedAt.UTC().Format("2006-01-02_150405")+fmt.Sprintf("_%d.zip", b.ID))
	files, err := s.Store.BackupFiles(ctx, b.ID)
	if err != nil {
		return "", err
	}
	tmp := name + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	err = s.WriteZip(f, files)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	return name, os.Rename(tmp, name)
}

// WriteZip streams the given files as a ZIP archive.
func (s *Service) WriteZip(w io.Writer, files []store.FileEntry) error {
	zw := zip.NewWriter(w)
	for _, f := range files {
		if f.Deleted {
			continue
		}
		hdr := &zip.FileHeader{Name: f.Path, Method: zip.Deflate, Modified: time.UnixMilli(f.Mtime)}
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		r, err := s.Blobs.Open(f.Hash)
		if err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
		_, err = io.Copy(fw, r)
		r.Close()
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

func (s *Service) expire(ctx context.Context, v *store.Vault, cutoff time.Time) error {
	old, err := s.Store.ExpiredBackups(ctx, v.ID, cutoff)
	if err != nil {
		return err
	}
	for _, b := range old {
		if err := s.Delete(ctx, b); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes a backup and its ZIP file.
func (s *Service) Delete(ctx context.Context, b *store.Backup) error {
	if b.ZipPath != "" {
		if err := os.Remove(b.ZipPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return s.Store.DeleteBackup(ctx, b.ID)
}

// Restore makes the vault look exactly like backup b. A "pre-restore" backup
// is taken first; the restore is committed as ordinary changes, so every
// connected device picks it up by syncing.
func (s *Service) Restore(ctx context.Context, b *store.Backup, author store.Author) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.create(ctx, b.VaultID, store.BackupPreRestore); err != nil {
		return 0, fmt.Errorf("pre-restore backup: %w", err)
	}
	want, err := s.Store.BackupFiles(ctx, b.ID)
	if err != nil {
		return 0, err
	}
	cur, err := s.Store.ListFiles(ctx, b.VaultID, false)
	if err != nil {
		return 0, err
	}
	wantBy := map[string]store.FileEntry{}
	for _, f := range want {
		wantBy[f.Path] = f
	}
	var ops []store.Op
	for _, f := range cur {
		if w, ok := wantBy[f.Path]; !ok {
			ops = append(ops, store.ForceOp(store.FileEntry{Path: f.Path, Deleted: true}))
		} else if w.Hash == f.Hash {
			delete(wantBy, f.Path)
		}
	}
	for _, w := range want {
		if _, ok := wantBy[w.Path]; ok {
			ops = append(ops, store.ForceOp(w))
		}
	}
	if len(ops) == 0 {
		return 0, nil
	}
	res, head, err := s.Store.Commit(ctx, b.VaultID, ops, author, s.Blobs.Has)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range res {
		if r.OK {
			n++
		}
	}
	s.Hub.Notify(b.VaultID, head)
	return n, nil
}

// Maintenance prunes old file versions and deletes unreferenced blobs.
func (s *Service) Maintenance(ctx context.Context) {
	st := s.Store.Settings(ctx)
	if st.VersionRetentionDays > 0 {
		n, err := s.Store.PruneVersions(ctx, s.Now().Add(-time.Duration(st.VersionRetentionDays)*24*time.Hour))
		if err != nil {
			s.Log.Error("prune versions", "err", err)
		} else if n > 0 {
			s.Log.Info("pruned old versions", "count", n)
		}
	}
	live, err := s.Store.LiveHashes(ctx)
	if err != nil {
		s.Log.Error("gc: live hashes", "err", err)
		return
	}
	n, freed, err := s.Blobs.GC(live, 24*time.Hour, s.Now())
	if err != nil {
		s.Log.Error("gc", "err", err)
	} else if n > 0 {
		s.Log.Info("gc removed blobs", "count", n, "bytes", freed)
	}
}
