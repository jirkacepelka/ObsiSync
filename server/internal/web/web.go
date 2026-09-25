// Package web serves the administration GUI.
package web

import (
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/jirkacepelka/obsisync/server/internal/auth"
	"github.com/jirkacepelka/obsisync/server/internal/backup"
	"github.com/jirkacepelka/obsisync/server/internal/blobs"
	"github.com/jirkacepelka/obsisync/server/internal/hub"
	"github.com/jirkacepelka/obsisync/server/internal/store"
)

//go:embed templates/*.html static/*
var assets embed.FS

const sessionCookie = "obsisync_session"
const sessionTTL = 30 * 24 * time.Hour

type Web struct {
	Store   *store.Store
	Blobs   *blobs.Store
	Hub     *hub.Hub
	Backup  *backup.Service
	Limiter *auth.Limiter
	Version string
	Log     *slog.Logger
	// PluginDir holds the built Obsidian plugin (main.js, manifest.json,
	// styles.css) offered for download; may be empty.
	PluginDir string

	pages map[string]*template.Template
}

// page is the data passed to every template.
type page struct {
	Title   string
	Nav     string
	User    *store.User
	CSRF    string
	OK      string
	Err     string
	Version string
	Vault   *store.Vault
	Role    string
	Tab     string
	D       map[string]any
}

func (w *Web) Register(mux *http.ServeMux) error {
	if err := w.parseTemplates(); err != nil {
		return err
	}
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))

	mux.HandleFunc("GET /setup", w.setupForm)
	mux.HandleFunc("POST /setup", w.setupSubmit)
	mux.HandleFunc("GET /login", w.loginForm)
	mux.HandleFunc("POST /login", w.loginSubmit)
	mux.HandleFunc("POST /logout", w.user(w.logout))

	mux.HandleFunc("GET /{$}", w.user(w.dashboard))
	mux.HandleFunc("GET /vaults", w.user(w.vaultList))
	mux.HandleFunc("GET /vaults/new", w.admin(w.vaultNewForm))
	mux.HandleFunc("POST /vaults", w.admin(w.vaultCreate))

	mux.HandleFunc("GET /vaults/{id}", w.vault(store.RoleViewer, w.vaultFiles))
	mux.HandleFunc("GET /vaults/{id}/file", w.vault(store.RoleViewer, w.fileHistory))
	mux.HandleFunc("GET /vaults/{id}/version/{vid}", w.vault(store.RoleViewer, w.versionRaw))
	mux.HandleFunc("POST /vaults/{id}/version/{vid}/restore", w.vault(store.RoleEditor, w.versionRestore))
	mux.HandleFunc("GET /vaults/{id}/trash", w.vault(store.RoleViewer, w.trash))
	mux.HandleFunc("GET /vaults/{id}/zip", w.vault(store.RoleViewer, w.vaultZip))

	mux.HandleFunc("GET /vaults/{id}/backups", w.vault(store.RoleViewer, w.backups))
	mux.HandleFunc("POST /vaults/{id}/backups", w.vault(store.RoleEditor, w.backupCreate))
	mux.HandleFunc("GET /vaults/{id}/backups/{bid}", w.vault(store.RoleViewer, w.backupView))
	mux.HandleFunc("GET /vaults/{id}/backups/{bid}/zip", w.vault(store.RoleViewer, w.backupZip))
	mux.HandleFunc("GET /vaults/{id}/backups/{bid}/raw", w.vault(store.RoleViewer, w.backupRaw))
	mux.HandleFunc("POST /vaults/{id}/backups/{bid}/restore", w.vault(store.RoleOwner, w.backupRestore))
	mux.HandleFunc("POST /vaults/{id}/backups/{bid}/restore-file", w.vault(store.RoleEditor, w.backupRestoreFile))
	mux.HandleFunc("POST /vaults/{id}/backups/{bid}/delete", w.vault(store.RoleOwner, w.backupDelete))

	mux.HandleFunc("GET /vaults/{id}/members", w.vault(store.RoleOwner, w.members))
	mux.HandleFunc("POST /vaults/{id}/members", w.vault(store.RoleOwner, w.memberSet))
	mux.HandleFunc("POST /vaults/{id}/members/remove", w.vault(store.RoleOwner, w.memberRemove))
	mux.HandleFunc("GET /vaults/{id}/settings", w.vault(store.RoleOwner, w.vaultSettings))
	mux.HandleFunc("POST /vaults/{id}/settings", w.vault(store.RoleOwner, w.vaultSettingsSave))
	mux.HandleFunc("POST /vaults/{id}/delete", w.vault(store.RoleOwner, w.vaultDelete))

	mux.HandleFunc("GET /users", w.admin(w.users))
	mux.HandleFunc("POST /users", w.admin(w.userCreate))
	mux.HandleFunc("POST /users/{uid}/password", w.admin(w.userPassword))
	mux.HandleFunc("POST /users/{uid}/admin", w.admin(w.userAdmin))
	mux.HandleFunc("POST /users/{uid}/delete", w.admin(w.userDelete))

	mux.HandleFunc("GET /devices", w.user(w.devices))
	mux.HandleFunc("POST /devices/{did}/delete", w.user(w.deviceDelete))
	mux.HandleFunc("GET /account", w.user(w.account))
	mux.HandleFunc("POST /account", w.user(w.accountSave))
	mux.HandleFunc("GET /settings", w.admin(w.settings))
	mux.HandleFunc("POST /settings", w.admin(w.settingsSave))
	mux.HandleFunc("GET /plugin", w.user(w.plugin))
	mux.HandleFunc("GET /plugin/obsisync.zip", w.pluginZip)
	return nil
}

var funcs = template.FuncMap{
	"bytes": humanBytes,
	"date": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.Local().Format("2. 1. 2006 15:04")
	},
	"datep": func(t *time.Time) string {
		if t == nil {
			return "—"
		}
		return t.Local().Format("2. 1. 2006 15:04")
	},
	"ms": func(ms int64) time.Time { return time.UnixMilli(ms) },
	"ago": func(t time.Time) string {
		d := time.Since(t)
		switch {
		case d < time.Minute:
			return "právě teď"
		case d < time.Hour:
			return fmt.Sprintf("před %d min", int(d.Minutes()))
		case d < 48*time.Hour:
			return fmt.Sprintf("před %d h", int(d.Hours()))
		default:
			return fmt.Sprintf("před %d dny", int(d.Hours()/24))
		}
	},
	"intervalLabel": func(sec int64) string {
		for _, i := range store.BackupIntervals {
			if i.Seconds == sec {
				return i.Label
			}
		}
		return fmt.Sprintf("%d s", sec)
	},
	"retentionLabel": func(days int) string {
		for _, r := range store.BackupRetentions {
			if r.Days == days {
				return r.Label
			}
		}
		return fmt.Sprintf("%d dní", days)
	},
	"roleLabel": func(r string) string {
		return map[string]string{store.RoleOwner: "Vlastník", store.RoleEditor: "Úpravy", store.RoleViewer: "Jen čtení"}[r]
	},
	"kindLabel": func(k string) string {
		return map[string]string{store.BackupScheduled: "Plánovaná", store.BackupManual: "Ruční", store.BackupPreRestore: "Před obnovou"}[k]
	},
	"atLeast":    store.RoleAtLeast,
	"intervals":  func() any { return store.BackupIntervals },
	"retentions": func() any { return store.BackupRetentions },
	"base":       path.Base,
	"q":          url.QueryEscape,
	"short":      func(h string) string { return h[:min(len(h), 10)] },
	"dict3": func(interval int64, retention int, zip bool) map[string]any {
		return map[string]any{"Interval": interval, "Retention": retention, "Zip": zip}
	},
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func (w *Web) parseTemplates() error {
	w.pages = map[string]*template.Template{}
	files, err := fs.Glob(assets, "templates/*.html")
	if err != nil {
		return err
	}
	for _, f := range files {
		name := strings.TrimSuffix(path.Base(f), ".html")
		if name == "layout" || name == "partials" {
			continue
		}
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(assets, "templates/layout.html", "templates/partials.html", f)
		if err != nil {
			return fmt.Errorf("template %s: %w", name, err)
		}
		w.pages[name] = t
	}
	return nil
}

func (w *Web) render(rw http.ResponseWriter, r *http.Request, name string, p *page) {
	t, ok := w.pages[name]
	if !ok {
		http.Error(rw, "missing template "+name, http.StatusInternalServerError)
		return
	}
	p.Version = w.Version
	if p.OK == "" {
		p.OK = r.URL.Query().Get("ok")
	}
	if p.Err == "" {
		p.Err = r.URL.Query().Get("err")
	}
	if p.D == nil {
		p.D = map[string]any{}
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Header().Set("X-Frame-Options", "DENY")
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	rw.Header().Set("Referrer-Policy", "same-origin")
	if err := t.Execute(rw, p); err != nil {
		w.Log.Error("render", "page", name, "err", err)
	}
}

// redirect sends the browser to target with an optional flash message.
func redirect(rw http.ResponseWriter, r *http.Request, target, okMsg, errMsg string) {
	u, _ := url.Parse(target)
	q := u.Query()
	if okMsg != "" {
		q.Set("ok", okMsg)
	}
	if errMsg != "" {
		q.Set("err", errMsg)
	}
	u.RawQuery = q.Encode()
	http.Redirect(rw, r, u.String(), http.StatusSeeOther)
}

func (w *Web) fail(rw http.ResponseWriter, r *http.Request, status int, msg string) {
	rw.WriteHeader(status)
	w.render(rw, r, "error", &page{Title: "Chyba", Err: msg})
}

// ---- sessions & middleware ----

type session struct {
	user *store.User
	csrf string
}

func (w *Web) session(r *http.Request) *session {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	u, csrf, err := w.Store.SessionUser(r.Context(), auth.HashToken(c.Value))
	if err != nil {
		return nil
	}
	return &session{u, csrf}
}

func secure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func (w *Web) startSession(rw http.ResponseWriter, r *http.Request, u *store.User) error {
	tok, hash := auth.NewToken("oss_")
	csrf, _ := auth.NewToken("")
	if err := w.Store.CreateSession(r.Context(), u.ID, hash, csrf, sessionTTL); err != nil {
		return err
	}
	http.SetCookie(rw, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true,
		Secure: secure(r), SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds())})
	return nil
}

func (w *Web) needSetup(r *http.Request) bool {
	n, err := w.Store.CountUsers(r.Context())
	return err == nil && n == 0
}

type handler func(http.ResponseWriter, *http.Request, *page)

func (w *Web) user(h handler) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if w.needSetup(r) {
			http.Redirect(rw, r, "/setup", http.StatusSeeOther)
			return
		}
		s := w.session(r)
		if s == nil {
			http.Redirect(rw, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodPost && subtle.ConstantTimeCompare([]byte(r.FormValue("_csrf")), []byte(s.csrf)) != 1 {
			w.fail(rw, r, http.StatusForbidden, "Neplatný formulář (CSRF). Obnov stránku a zkus to znovu.")
			return
		}
		h(rw, r, &page{User: s.user, CSRF: s.csrf})
	}
}

func (w *Web) admin(h handler) http.HandlerFunc {
	return w.user(func(rw http.ResponseWriter, r *http.Request, p *page) {
		if !p.User.IsAdmin {
			w.fail(rw, r, http.StatusForbidden, "Tato stránka je jen pro administrátory.")
			return
		}
		h(rw, r, p)
	})
}

func (w *Web) vault(minRole string, h handler) http.HandlerFunc {
	return w.user(func(rw http.ResponseWriter, r *http.Request, p *page) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		role, err := w.Store.Role(r.Context(), p.User, id)
		if err != nil || role == "" {
			w.fail(rw, r, http.StatusNotFound, "Vault neexistuje nebo k němu nemáš přístup.")
			return
		}
		if !store.RoleAtLeast(role, minRole) {
			w.fail(rw, r, http.StatusForbidden, "K této akci nemáš oprávnění.")
			return
		}
		v, err := w.Store.Vault(r.Context(), id)
		if err != nil {
			w.fail(rw, r, http.StatusNotFound, "Vault neexistuje.")
			return
		}
		p.Vault, p.Role, p.Nav = v, role, "vaults"
		h(rw, r, p)
	})
}

func formInt(r *http.Request, key string) int64 {
	n, _ := strconv.ParseInt(r.FormValue(key), 10, 64)
	return n
}

func errText(err error) string {
	if errors.Is(err, store.ErrNotFound) {
		return "Nenalezeno."
	}
	return err.Error()
}
