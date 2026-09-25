package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jirkacepelka/obsisync/server/internal/auth"
	"github.com/jirkacepelka/obsisync/server/internal/i18n"
	"github.com/jirkacepelka/obsisync/server/internal/store"
)

type env struct {
	t   *testing.T
	app *App
	srv *httptest.Server
}

func newEnv(t *testing.T) *env {
	t.Helper()
	a, err := New(Config{DataDir: t.TempDir(), Version: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler)
	t.Cleanup(func() { srv.Close(); a.Close() })
	return &env{t, a, srv}
}

func (e *env) user(name string, admin bool) *store.User {
	h, _ := auth.HashPassword("heslo1234")
	u, err := e.app.Store.CreateUser(context.Background(), name, h, admin)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

// call performs an API request and decodes the JSON response into out.
func (e *env) call(method, path, token string, body any, out any) int {
	e.t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		r = bytes.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		r = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if out != nil {
		if b, ok := out.(*[]byte); ok {
			*b = data
		} else if err := json.Unmarshal(data, out); err != nil {
			e.t.Fatalf("%s %s: bad json %q", method, path, data)
		}
	}
	return res.StatusCode
}

func (e *env) login(name string) string {
	var out struct{ Token string }
	if st := e.call("POST", "/api/v1/auth/login", "", map[string]string{"username": name, "password": "heslo1234", "device_name": "test"}, &out); st != 200 {
		e.t.Fatalf("login %s: %d", name, st)
	}
	return out.Token
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

type commitRes struct {
	Results []store.OpResult
	Head    int64
}

func (e *env) put(token string, vault int64, path string, content []byte, base string) store.OpResult {
	e.t.Helper()
	h := sha(content)
	vp := "/api/v1/vaults/" + itoa(vault)
	if st := e.call("PUT", vp+"/blobs/"+h, token, content, nil); st != 200 {
		e.t.Fatalf("upload: %d", st)
	}
	var res commitRes
	if st := e.call("POST", vp+"/commit", token, map[string]any{"ops": []store.Op{{Path: path, Hash: h, Size: int64(len(content)), Mtime: 1, BaseHash: base}}}, &res); st != 200 {
		e.t.Fatalf("commit: %d", st)
	}
	return res.Results[0]
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestSyncAPI(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	jirka := e.user("jirka", false)
	e.user("petra", false)
	v, _ := e.app.Store.CreateVault(ctx, "Poznámky", store.DefaultBackupPolicy, jirka.ID)

	if st := e.call("POST", "/api/v1/auth/login", "", map[string]string{"username": "jirka", "password": "spatne"}, nil); st != 401 {
		t.Fatalf("bad password: %d", st)
	}
	tj := e.login("jirka")
	tp := e.login("petra")

	var list struct {
		Vaults []struct {
			ID   int64
			Name string
			Role string
		}
	}
	e.call("GET", "/api/v1/vaults", tj, nil, &list)
	if len(list.Vaults) != 1 || list.Vaults[0].Name != "Poznámky" || list.Vaults[0].Role != "owner" {
		t.Fatalf("vault list: %+v", list)
	}
	e.call("GET", "/api/v1/vaults", tp, nil, &list)
	if len(list.Vaults) != 0 {
		t.Fatalf("petra sees vaults: %+v", list)
	}
	vp := "/api/v1/vaults/" + itoa(v.ID)
	if st := e.call("GET", vp+"/changes", tp, nil, nil); st != 404 {
		t.Fatalf("non-member access: %d", st)
	}

	// WebSocket notifications.
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(e.srv.URL, "http") + vp + "/ws?token=" + url.QueryEscape(tj)
	conn, _, err := websocket.Dial(wctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	readRev := func() int64 {
		_, msg, err := conn.Read(wctx)
		if err != nil {
			t.Fatal(err)
		}
		var m struct{ Rev int64 }
		json.Unmarshal(msg, &m)
		return m.Rev
	}
	if r := readRev(); r != 0 {
		t.Fatalf("initial rev %d", r)
	}

	r := e.put(tj, v.ID, "Deník/Dnes.md", []byte("ahoj"), "")
	if !r.OK || r.Rev != 1 {
		t.Fatalf("put: %+v", r)
	}
	if rv := readRev(); rv != 1 {
		t.Fatalf("ws rev %d", rv)
	}

	var ch struct {
		Changes []store.FileEntry
		Head    int64
	}
	e.call("GET", vp+"/changes?since=0", tj, nil, &ch)
	if len(ch.Changes) != 1 || ch.Head != 1 || ch.Changes[0].Hash != sha([]byte("ahoj")) {
		t.Fatalf("changes: %+v", ch)
	}
	var body []byte
	if st := e.call("GET", vp+"/blobs/"+ch.Changes[0].Hash, tj, nil, &body); st != 200 || string(body) != "ahoj" {
		t.Fatalf("download: %d %q", st, body)
	}

	// Stale write is rejected.
	r = e.put(tj, v.ID, "Deník/Dnes.md", []byte("jiný text"), "")
	if r.OK || r.Error != "conflict" {
		t.Fatalf("expected conflict: %+v", r)
	}

	// Wrong hash upload is rejected.
	if st := e.call("PUT", vp+"/blobs/"+sha([]byte("x")), tj, []byte("y"), nil); st != 400 {
		t.Fatalf("hash mismatch accepted: %d", st)
	}

	// Viewer cannot write.
	e.app.Store.SetMember(ctx, v.ID, mustUser(e, "petra").ID, store.RoleViewer)
	if st := e.call("POST", vp+"/commit", tp, map[string]any{"ops": []store.Op{{Path: "x.md", Deleted: true}}}, nil); st != 403 {
		t.Fatalf("viewer commit: %d", st)
	}
	if st := e.call("GET", vp+"/changes", tp, nil, nil); st != 200 {
		t.Fatalf("viewer read: %d", st)
	}

	// Blobs of another vault are not downloadable.
	other, _ := e.app.Store.CreateVault(ctx, "Tajné", store.DefaultBackupPolicy, 0)
	secret, _ := e.app.Blobs.PutBytes([]byte("tajemství"))
	e.app.Store.Commit(ctx, other.ID, []store.Op{{Path: "s.md", Hash: secret, Size: 9}}, store.Author{}, e.app.Blobs.Has)
	if st := e.call("GET", vp+"/blobs/"+secret, tj, nil, nil); st != 404 {
		t.Fatalf("cross-vault blob: %d", st)
	}
	var miss struct{ Missing []string }
	e.call("POST", vp+"/blobs/missing", tj, map[string]any{"hashes": []string{secret, sha([]byte("ahoj"))}}, &miss)
	if len(miss.Missing) != 1 || miss.Missing[0] != secret {
		t.Fatalf("missing: %+v", miss)
	}

	// Creating a vault from the plugin.
	var nv struct {
		ID   int64
		Role string
	}
	if st := e.call("POST", "/api/v1/vaults", tj, map[string]string{"name": "Z Obsidianu"}, &nv); st != 201 || nv.Role != "owner" {
		t.Fatalf("create vault: %d %+v", st, nv)
	}

	// Logout revokes the token.
	e.call("POST", "/api/v1/auth/logout", tj, nil, nil)
	if st := e.call("GET", "/api/v1/vaults", tj, nil, nil); st != 401 {
		t.Fatalf("after logout: %d", st)
	}
}

func mustUser(e *env, name string) *store.User {
	u, err := e.app.Store.UserByName(context.Background(), name)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func TestBackupsScheduleRetentionRestore(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	e.app.Store.Now = clock
	e.app.Backup.Now = clock

	v, err := e.app.Store.CreateVault(ctx, "Zálohovaný", store.BackupPolicy{IntervalSec: 3600, RetentionDays: 7, Zip: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	commit := func(path, content string) {
		h, _ := e.app.Blobs.PutBytes([]byte(content))
		cur, _ := e.app.Store.File(ctx, v.ID, path)
		base := ""
		if cur != nil && !cur.Deleted {
			base = cur.Hash
		}
		res, head, err := e.app.Store.Commit(ctx, v.ID, []store.Op{{Path: path, Hash: h, Size: int64(len(content)), BaseHash: base}}, store.Author{Name: "t"}, e.app.Blobs.Has)
		if err != nil || !res[0].OK {
			t.Fatalf("commit %s: %+v %v", path, res, err)
		}
		e.app.Hub.Notify(v.ID, head)
	}
	count := func() int {
		bs, _ := e.app.Store.ListBackups(ctx, v.ID)
		return len(bs)
	}

	commit("a.md", "verze 1")
	e.app.Backup.Tick(ctx)
	if count() != 1 {
		t.Fatalf("first backup not created: %d", count())
	}
	first, _ := e.app.Store.LatestBackup(ctx, v.ID)
	if first.ZipPath == "" {
		t.Fatal("zip not written")
	}
	if _, err := os.Stat(first.ZipPath); err != nil {
		t.Fatal(err)
	}

	// Not due yet.
	now = now.Add(30 * time.Minute)
	commit("a.md", "verze 2")
	e.app.Backup.Tick(ctx)
	if count() != 1 {
		t.Fatalf("backup before interval: %d", count())
	}
	// Due and changed.
	now = now.Add(31 * time.Minute)
	e.app.Backup.Tick(ctx)
	if count() != 2 {
		t.Fatalf("second backup: %d", count())
	}
	// Due but unchanged: skipped.
	now = now.Add(2 * time.Hour)
	e.app.Backup.Tick(ctx)
	if count() != 2 {
		t.Fatalf("unchanged vault backed up: %d", count())
	}

	// Restore the first backup: a.md goes back to "verze 1", b.md disappears.
	commit("b.md", "nový soubor")
	n, err := e.app.Backup.Restore(ctx, first, store.Author{Name: "admin (web)"})
	if err != nil || n != 2 {
		t.Fatalf("restore: n=%d err=%v", n, err)
	}
	a, _ := e.app.Store.File(ctx, v.ID, "a.md")
	b, _ := e.app.Store.File(ctx, v.ID, "b.md")
	if a.Hash != sha([]byte("verze 1")) || !b.Deleted {
		t.Fatalf("after restore a=%+v b=%+v", a, b)
	}
	latest, _ := e.app.Store.LatestBackup(ctx, v.ID)
	if latest.Kind != store.BackupPreRestore || latest.FileCount != 2 {
		t.Fatalf("pre-restore backup: %+v", latest)
	}

	// Retention: after 8 days everything but the newest backup expires.
	// GC must keep blobs still referenced by the remaining backup.
	now = now.Add(8 * 24 * time.Hour)
	commit("a.md", "verze 3")
	e.app.Backup.Tick(ctx)
	bs, _ := e.app.Store.ListBackups(ctx, v.ID)
	if len(bs) != 1 {
		t.Fatalf("retention kept %d backups", len(bs))
	}
	if _, err := os.Stat(first.ZipPath); !os.IsNotExist(err) {
		t.Fatal("expired zip not removed")
	}
	e.app.Store.SaveSettings(ctx, store.Settings{VersionRetentionDays: 1, MaxFileMB: 10})
	e.app.Backup.Maintenance(ctx)
	files, _ := e.app.Store.BackupFiles(ctx, bs[0].ID)
	for _, f := range files {
		if !e.app.Blobs.Has(f.Hash) {
			t.Fatalf("GC removed blob of %s still in backup", f.Path)
		}
	}
}

// browser is a tiny cookie-keeping client for the web GUI.
type browser struct {
	e    *env
	c    *http.Client
	csrf string
}

var csrfRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

func (b *browser) get(path string) (int, string) {
	res, err := b.c.Get(b.e.srv.URL + path)
	if err != nil {
		b.e.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if m := csrfRe.FindSubmatch(body); m != nil {
		b.csrf = string(m[1])
	}
	return res.StatusCode, string(body)
}

func (b *browser) post(path string, form url.Values) (int, string) {
	if b.csrf != "" {
		form.Set("_csrf", b.csrf)
	}
	res, err := b.c.PostForm(b.e.srv.URL+path, form)
	if err != nil {
		b.e.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body)
}

func TestWebGUI(t *testing.T) {
	e := newEnv(t)
	jar, _ := cookiejar.New(nil)
	b := &browser{e: e, c: &http.Client{Jar: jar}}

	// Fresh server redirects to setup.
	if _, body := b.get("/"); !strings.Contains(body, "Welcome to ObsiSync") {
		t.Fatal("setup page not shown")
	}
	if _, body := b.post("/setup", url.Values{"username": {"admin"}, "password": {"heslo1234"}, "password2": {"heslo1234"}}); !strings.Contains(body, "The server is ready") {
		t.Fatalf("setup failed: %s", body)
	}
	// Setup cannot run twice.
	if _, body := b.get("/setup"); strings.Contains(body, "Welcome to ObsiSync") {
		t.Fatal("setup reachable after first user")
	}

	b.get("/vaults/new")
	if !strings.Contains(mustGet(b, "/vaults/new"), "Backup frequency") {
		t.Fatal("new vault form lacks backup settings")
	}
	// Missing backup settings are rejected.
	if _, body := b.post("/vaults", url.Values{"name": {"Bez záloh"}}); !strings.Contains(body, "Pick the backup frequency") {
		t.Fatalf("vault without backup policy accepted: %s", body)
	}
	_, body := b.post("/vaults", url.Values{"name": {"Rodina"}, "backup_interval": {"21600"}, "backup_retention_days": {"90"}})
	if !strings.Contains(body, "Vault Rodina was created") {
		t.Fatalf("create vault: %s", body)
	}
	vs, _ := e.app.Store.ListVaults(context.Background())
	if len(vs) != 1 || vs[0].Backup.IntervalSec != 21600 || vs[0].Backup.RetentionDays != 90 {
		t.Fatalf("vault policy: %+v", vs[0].Backup)
	}
	id := itoa(vs[0].ID)
	if _, body := b.post("/vaults/"+id+"/backups", url.Values{}); !strings.Contains(body, "Backup created") {
		t.Fatalf("manual backup: %s", body)
	}
	for _, p := range []string{"/", "/vaults", "/vaults/" + id, "/vaults/" + id + "/trash", "/vaults/" + id + "/backups",
		"/vaults/" + id + "/members", "/vaults/" + id + "/settings", "/users", "/devices", "/account", "/settings", "/plugin"} {
		if st, body := b.get(p); st != 200 || strings.Contains(body, "flash err") {
			t.Errorf("GET %s: %d", p, st)
		}
	}
	// Every language renders every page without missing keys or template errors.
	for _, l := range i18n.Languages {
		b.get("/lang?l=" + l.Code + "&next=/")
		for _, p := range []string{"/", "/vaults/" + id, "/vaults/" + id + "/backups", "/vaults/" + id + "/settings", "/vaults/new", "/users", "/devices", "/plugin"} {
			st, body := b.get(p)
			if st != 200 || !strings.Contains(body, `<html lang="`+l.Code+`">`) || !strings.Contains(body, "</footer>") {
				t.Errorf("%s %s: status %d or incomplete page", l.Code, p, st)
			}
			if m := regexp.MustCompile(`\b(?:nav|tab|col|msg|field|backup|backups|interval|retention|role|kind)\.[a-zA-Z]+\b`).FindString(body); m != "" {
				t.Errorf("%s %s: untranslated key %q", l.Code, p, m)
			}
		}
	}
	b.get("/lang?l=cs&next=/")
	if !strings.Contains(mustGet(b, "/vaults/new"), "Frekvence záloh") {
		t.Fatal("Czech not applied")
	}
	b.get("/lang?l=en&next=/")

	// POST without CSRF token is refused.
	b.csrf = ""
	if st, _ := b.post("/users", url.Values{"username": {"x"}, "password": {"heslo1234"}}); st != 403 {
		t.Fatalf("csrf not enforced: %d", st)
	}
}

func mustGet(b *browser, p string) string {
	_, body := b.get(p)
	return body
}
