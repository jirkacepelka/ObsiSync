package web

import (
	"archive/zip"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jirkacepelka/obsisync/server/internal/store"
)

func (w *Web) backupFor(rw http.ResponseWriter, r *http.Request, p *page) *store.Backup {
	b, err := w.Store.Backup(r.Context(), formIntPath(r, "bid"))
	if err != nil || b.VaultID != p.Vault.ID {
		w.fail(rw, r, http.StatusNotFound, w.tr(r, "msg.backupNotFound"))
		return nil
	}
	return b
}

func (w *Web) backups(rw http.ResponseWriter, r *http.Request, p *page) {
	p.Title, p.Tab = p.Vault.Name+" – "+w.tr(r, "tab.backups"), "backups"
	list, err := w.Store.ListBackups(r.Context(), p.Vault.ID)
	if err != nil {
		w.fail(rw, r, 500, err.Error())
		return
	}
	d := map[string]any{"Backups": list}
	if p.Vault.Backup.IntervalSec > 0 {
		next := time.Now()
		if p.Vault.LastBackupAt != nil {
			next = p.Vault.LastBackupAt.Add(time.Duration(p.Vault.Backup.IntervalSec) * time.Second)
		}
		d["Next"] = next
	}
	p.D = d
	w.render(rw, r, "vault_backups", p)
}

func (w *Web) backupCreate(rw http.ResponseWriter, r *http.Request, p *page) {
	back := fmt.Sprintf("/vaults/%d/backups", p.Vault.ID)
	b, err := w.Backup.Create(r.Context(), p.Vault.ID, store.BackupManual)
	if err != nil {
		redirect(rw, r, back, "", w.tr(r, "msg.backupFailed", w.errText(r, err)))
		return
	}
	redirect(rw, r, back, w.tr(r, "msg.backupCreated", b.FileCount), "")
}

func (w *Web) backupView(rw http.ResponseWriter, r *http.Request, p *page) {
	b := w.backupFor(rw, r, p)
	if b == nil {
		return
	}
	files, err := w.Store.BackupFiles(r.Context(), b.ID)
	if err != nil {
		w.fail(rw, r, 500, err.Error())
		return
	}
	p.Title, p.Tab = p.Vault.Name+" – "+w.tr(r, "tab.backups"), "backups"
	p.D = map[string]any{"Backup": b, "Files": files}
	w.render(rw, r, "backup", p)
}

func (w *Web) backupZip(rw http.ResponseWriter, r *http.Request, p *page) {
	b := w.backupFor(rw, r, p)
	if b == nil {
		return
	}
	name := fmt.Sprintf("%s_%s.zip", p.Vault.Name, b.CreatedAt.Local().Format("2006-01-02_1504"))
	rw.Header().Set("Content-Type", "application/zip")
	rw.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	if b.ZipPath != "" {
		if f, err := os.Open(b.ZipPath); err == nil {
			defer f.Close()
			io.Copy(rw, f)
			return
		}
	}
	files, err := w.Store.BackupFiles(r.Context(), b.ID)
	if err == nil {
		err = w.Backup.WriteZip(rw, files)
	}
	if err != nil {
		w.Log.Error("backup zip", "err", err)
	}
}

func (w *Web) backupFile(rw http.ResponseWriter, r *http.Request, b *store.Backup, fp string) *store.FileEntry {
	files, err := w.Store.BackupFiles(r.Context(), b.ID)
	if err == nil {
		for _, f := range files {
			if f.Path == fp {
				return &f
			}
		}
	}
	w.fail(rw, r, http.StatusNotFound, w.tr(r, "msg.backupFileNotFound"))
	return nil
}

func (w *Web) backupRaw(rw http.ResponseWriter, r *http.Request, p *page) {
	b := w.backupFor(rw, r, p)
	if b == nil {
		return
	}
	if f := w.backupFile(rw, r, b, r.URL.Query().Get("path")); f != nil {
		w.serveBlob(rw, r, f.Hash, f.Path)
	}
}

func (w *Web) backupRestoreFile(rw http.ResponseWriter, r *http.Request, p *page) {
	b := w.backupFor(rw, r, p)
	if b == nil {
		return
	}
	f := w.backupFile(rw, r, b, r.FormValue("path"))
	if f == nil {
		return
	}
	back := fmt.Sprintf("/vaults/%d/backups/%d", p.Vault.ID, b.ID)
	if err := w.commitForce(r, p, *f); err != nil {
		redirect(rw, r, back, "", w.errText(r, err))
		return
	}
	redirect(rw, r, back, w.tr(r, "msg.fileRestoredFromBackup", f.Path), "")
}

func (w *Web) backupRestore(rw http.ResponseWriter, r *http.Request, p *page) {
	b := w.backupFor(rw, r, p)
	if b == nil {
		return
	}
	back := fmt.Sprintf("/vaults/%d/backups", p.Vault.ID)
	n, err := w.Backup.Restore(r.Context(), b, w.webAuthor(p))
	if err != nil {
		redirect(rw, r, back, "", w.tr(r, "msg.restoreFailed", w.errText(r, err)))
		return
	}
	redirect(rw, r, back, w.tr(r, "msg.vaultRestored", w.date(r, b.CreatedAt), n), "")
}

func (w *Web) backupDelete(rw http.ResponseWriter, r *http.Request, p *page) {
	b := w.backupFor(rw, r, p)
	if b == nil {
		return
	}
	back := fmt.Sprintf("/vaults/%d/backups", p.Vault.ID)
	if err := w.Backup.Delete(r.Context(), b); err != nil {
		redirect(rw, r, back, "", w.errText(r, err))
		return
	}
	redirect(rw, r, back, w.tr(r, "msg.backupDeleted"), "")
}

// ---- plugin download ----

var pluginFiles = []string{"manifest.json", "main.js", "styles.css"}

func (w *Web) pluginAvailable() bool {
	if w.PluginDir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(w.PluginDir, "main.js"))
	return err == nil
}

func (w *Web) plugin(rw http.ResponseWriter, r *http.Request, p *page) {
	p.Title, p.Nav = w.tr(r, "plugin.title"), "plugin"
	p.D = map[string]any{"Available": w.pluginAvailable(), "ServerURL": serverURL(r)}
	w.render(rw, r, "plugin", p)
}

// pluginZip is public so it can be downloaded directly on a phone.
func (w *Web) pluginZip(rw http.ResponseWriter, r *http.Request) {
	if !w.pluginAvailable() {
		http.NotFound(rw, r)
		return
	}
	rw.Header().Set("Content-Type", "application/zip")
	rw.Header().Set("Content-Disposition", `attachment; filename="simplesync.zip"`)
	zw := zip.NewWriter(rw)
	for _, name := range pluginFiles {
		f, err := os.Open(filepath.Join(w.PluginDir, name))
		if err != nil {
			continue
		}
		fw, err := zw.Create("simplesync/" + name)
		if err == nil {
			io.Copy(fw, f)
		}
		f.Close()
	}
	zw.Close()
}
