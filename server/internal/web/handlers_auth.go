package web

import (
	"net/http"
	"strings"

	"github.com/jirkacepelka/obsisync/server/internal/api"
	"github.com/jirkacepelka/obsisync/server/internal/auth"
	"github.com/jirkacepelka/obsisync/server/internal/store"
)

func (w *Web) setupForm(rw http.ResponseWriter, r *http.Request) {
	if !w.needSetup(r) {
		http.Redirect(rw, r, "/", http.StatusSeeOther)
		return
	}
	w.render(rw, r, "setup", &page{Title: "První spuštění"})
}

func (w *Web) setupSubmit(rw http.ResponseWriter, r *http.Request) {
	if !w.needSetup(r) {
		http.Redirect(rw, r, "/", http.StatusSeeOther)
		return
	}
	p := &page{Title: "První spuštění", D: map[string]any{"Username": r.FormValue("username")}}
	pw := r.FormValue("password")
	if pw != r.FormValue("password2") {
		p.Err = "Hesla se neshodují."
	} else if err := auth.ValidatePassword(pw); err != nil {
		p.Err = err.Error()
	}
	if p.Err == "" {
		hash, err := auth.HashPassword(pw)
		if err == nil {
			var u *store.User
			if u, err = w.Store.CreateUser(r.Context(), r.FormValue("username"), hash, true); err == nil {
				w.Log.Info("setup: admin created", "user", u.Username)
				if err = w.startSession(rw, r, u); err == nil {
					redirect(rw, r, "/", "Server je připravený. Založ si první vault.", "")
					return
				}
			}
		}
		p.Err = err.Error()
	}
	w.render(rw, r, "setup", p)
}

func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}

func (w *Web) loginForm(rw http.ResponseWriter, r *http.Request) {
	if w.needSetup(r) {
		http.Redirect(rw, r, "/setup", http.StatusSeeOther)
		return
	}
	if w.session(r) != nil {
		http.Redirect(rw, r, "/", http.StatusSeeOther)
		return
	}
	w.render(rw, r, "login", &page{Title: "Přihlášení", D: map[string]any{"Next": safeNext(r.URL.Query().Get("next"))}})
}

func (w *Web) loginSubmit(rw http.ResponseWriter, r *http.Request) {
	name := r.FormValue("username")
	next := safeNext(r.FormValue("next"))
	p := &page{Title: "Přihlášení", D: map[string]any{"Next": next, "Username": name}}
	key := api.ClientIP(r) + "|" + strings.ToLower(name)
	if !w.Limiter.Allowed(key) {
		p.Err = "Příliš mnoho pokusů, zkus to za 15 minut."
		w.render(rw, r, "login", p)
		return
	}
	u, err := w.Store.UserByName(r.Context(), name)
	if err != nil || !auth.CheckPassword(u.PasswordHash(), r.FormValue("password")) {
		w.Limiter.Fail(key)
		p.Err = "Špatné jméno nebo heslo."
		w.render(rw, r, "login", p)
		return
	}
	w.Limiter.Reset(key)
	if err := w.startSession(rw, r, u); err != nil {
		p.Err = err.Error()
		w.render(rw, r, "login", p)
		return
	}
	http.Redirect(rw, r, next, http.StatusSeeOther)
}

func (w *Web) logout(rw http.ResponseWriter, r *http.Request, p *page) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		w.Store.DeleteSession(r.Context(), auth.HashToken(c.Value))
	}
	http.SetCookie(rw, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(rw, r, "/login", http.StatusSeeOther)
}

func (w *Web) account(rw http.ResponseWriter, r *http.Request, p *page) {
	p.Title, p.Nav = "Můj účet", "account"
	w.render(rw, r, "account", p)
}

func (w *Web) accountSave(rw http.ResponseWriter, r *http.Request, p *page) {
	if !auth.CheckPassword(p.User.PasswordHash(), r.FormValue("current")) {
		redirect(rw, r, "/account", "", "Současné heslo není správně.")
		return
	}
	pw := r.FormValue("password")
	if pw != r.FormValue("password2") {
		redirect(rw, r, "/account", "", "Nová hesla se neshodují.")
		return
	}
	if err := auth.ValidatePassword(pw); err != nil {
		redirect(rw, r, "/account", "", err.Error())
		return
	}
	hash, _ := auth.HashPassword(pw)
	if err := w.Store.SetPassword(r.Context(), p.User.ID, hash); err != nil {
		redirect(rw, r, "/account", "", err.Error())
		return
	}
	u, _ := w.Store.UserByID(r.Context(), p.User.ID)
	w.startSession(rw, r, u)
	redirect(rw, r, "/account", "Heslo bylo změněno. Připojená zařízení zůstávají přihlášená.", "")
}

// ---- users (admin) ----

func (w *Web) users(rw http.ResponseWriter, r *http.Request, p *page) {
	p.Title, p.Nav = "Uživatelé", "users"
	users, err := w.Store.ListUsers(r.Context())
	if err != nil {
		w.fail(rw, r, 500, err.Error())
		return
	}
	p.D = map[string]any{"Users": users}
	w.render(rw, r, "users", p)
}

func (w *Web) userCreate(rw http.ResponseWriter, r *http.Request, p *page) {
	pw := r.FormValue("password")
	if err := auth.ValidatePassword(pw); err != nil {
		redirect(rw, r, "/users", "", err.Error())
		return
	}
	hash, _ := auth.HashPassword(pw)
	u, err := w.Store.CreateUser(r.Context(), r.FormValue("username"), hash, r.FormValue("admin") == "1")
	if err != nil {
		redirect(rw, r, "/users", "", err.Error())
		return
	}
	redirect(rw, r, "/users", "Uživatel "+u.Username+" byl vytvořen. Přidej ho do vaultu v nastavení členů.", "")
}

func (w *Web) targetUser(rw http.ResponseWriter, r *http.Request) *store.User {
	u, err := w.Store.UserByID(r.Context(), formIntPath(r, "uid"))
	if err != nil {
		redirect(rw, r, "/users", "", "Uživatel neexistuje.")
		return nil
	}
	return u
}

func (w *Web) userPassword(rw http.ResponseWriter, r *http.Request, p *page) {
	u := w.targetUser(rw, r)
	if u == nil {
		return
	}
	pw := r.FormValue("password")
	if err := auth.ValidatePassword(pw); err != nil {
		redirect(rw, r, "/users", "", err.Error())
		return
	}
	hash, _ := auth.HashPassword(pw)
	if err := w.Store.SetPassword(r.Context(), u.ID, hash); err != nil {
		redirect(rw, r, "/users", "", err.Error())
		return
	}
	redirect(rw, r, "/users", "Heslo uživatele "+u.Username+" bylo změněno.", "")
}

func (w *Web) userAdmin(rw http.ResponseWriter, r *http.Request, p *page) {
	u := w.targetUser(rw, r)
	if u == nil {
		return
	}
	makeAdmin := !u.IsAdmin
	if !makeAdmin {
		if n, _ := w.Store.CountAdmins(r.Context()); n <= 1 {
			redirect(rw, r, "/users", "", "Musí zůstat alespoň jeden administrátor.")
			return
		}
	}
	w.Store.SetAdmin(r.Context(), u.ID, makeAdmin)
	redirect(rw, r, "/users", "Uloženo.", "")
}

func (w *Web) userDelete(rw http.ResponseWriter, r *http.Request, p *page) {
	u := w.targetUser(rw, r)
	if u == nil {
		return
	}
	if u.ID == p.User.ID {
		redirect(rw, r, "/users", "", "Nemůžeš smazat sám sebe.")
		return
	}
	if u.IsAdmin {
		if n, _ := w.Store.CountAdmins(r.Context()); n <= 1 {
			redirect(rw, r, "/users", "", "Musí zůstat alespoň jeden administrátor.")
			return
		}
	}
	w.Store.DeleteUser(r.Context(), u.ID)
	redirect(rw, r, "/users", "Uživatel "+u.Username+" byl smazán.", "")
}

// ---- devices ----

func (w *Web) devices(rw http.ResponseWriter, r *http.Request, p *page) {
	p.Title, p.Nav = "Zařízení", "devices"
	var uid int64
	if !p.User.IsAdmin {
		uid = p.User.ID
	}
	devs, err := w.Store.ListDevices(r.Context(), uid)
	if err != nil {
		w.fail(rw, r, 500, err.Error())
		return
	}
	p.D = map[string]any{"Devices": devs, "Online": w.Hub.OnlineDevices()}
	w.render(rw, r, "devices", p)
}

func (w *Web) deviceDelete(rw http.ResponseWriter, r *http.Request, p *page) {
	d, err := w.Store.DeviceByID(r.Context(), formIntPath(r, "did"))
	if err != nil || (d.UserID != p.User.ID && !p.User.IsAdmin) {
		redirect(rw, r, "/devices", "", "Zařízení neexistuje.")
		return
	}
	w.Store.DeleteDevice(r.Context(), d.ID)
	redirect(rw, r, "/devices", "Zařízení "+d.Name+" bylo odhlášeno. Při další synchronizaci bude vyzváno k přihlášení.", "")
}

// ---- server settings (admin) ----

func (w *Web) settings(rw http.ResponseWriter, r *http.Request, p *page) {
	p.Title, p.Nav = "Nastavení serveru", "settings"
	p.D = map[string]any{"S": w.Store.Settings(r.Context())}
	w.render(rw, r, "settings", p)
}

func (w *Web) settingsSave(rw http.ResponseWriter, r *http.Request, p *page) {
	st := store.Settings{
		VersionRetentionDays: int(formInt(r, "version_retention_days")),
		MaxFileMB:            formInt(r, "max_file_mb"),
		UsersCanCreateVaults: r.FormValue("users_can_create_vaults") == "1",
	}
	if st.VersionRetentionDays < 0 || st.MaxFileMB < 1 || st.MaxFileMB > 10240 {
		redirect(rw, r, "/settings", "", "Neplatné hodnoty.")
		return
	}
	if err := w.Store.SaveSettings(r.Context(), st); err != nil {
		redirect(rw, r, "/settings", "", err.Error())
		return
	}
	redirect(rw, r, "/settings", "Nastavení uloženo.", "")
}

func formIntPath(r *http.Request, key string) int64 {
	var n int64
	for _, c := range r.PathValue(key) {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}
