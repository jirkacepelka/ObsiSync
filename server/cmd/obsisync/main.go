// Command obsisync is a simple self-hosted sync server for Obsidian.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // lets TZ work in minimal containers

	"github.com/jirkacepelka/obsisync/server/internal/app"
	"github.com/jirkacepelka/obsisync/server/internal/auth"
	"github.com/jirkacepelka/obsisync/server/internal/store"
)

var version = "dev"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func usage() {
	fmt.Fprintf(os.Stderr, `ObsiSync %s

Použití:
  obsisync [serve]                       spustí server
  obsisync reset-password <jméno> <heslo> nastaví heslo uživateli (i když zapomeneš admin heslo)
  obsisync backup-db <soubor>            uloží konzistentní kopii databáze
  obsisync healthcheck                   ověří, že server běží (pro Docker)
  obsisync version

Proměnné prostředí:
  TZ                   časové pásmo pro zobrazení časů (např. Europe/Prague)
  OBSISYNC_DATA        datová složka (výchozí /data, mimo Docker ./data)
  OBSISYNC_ADDR        adresa pro naslouchání (výchozí :8080)
  OBSISYNC_BACKUP_DIR  kam ukládat ZIP zálohy (výchozí $OBSISYNC_DATA/backups)
  OBSISYNC_PLUGIN_DIR  složka se sestaveným pluginem pro stažení (výchozí /app/plugin)
`, version)
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	defaultData := "/data"
	if _, err := os.Stat("/.dockerenv"); err != nil {
		defaultData = "./data"
	}
	dataDir := env("OBSISYNC_DATA", defaultData)

	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		if err := serve(log, dataDir); err != nil {
			log.Error("server stopped", "err", err)
			os.Exit(1)
		}
	case "reset-password":
		if len(os.Args) != 4 {
			usage()
			os.Exit(2)
		}
		exitOn(resetPassword(dataDir, os.Args[2], os.Args[3]))
		fmt.Println("Heslo nastaveno.")
	case "backup-db":
		if len(os.Args) != 3 {
			usage()
			os.Exit(2)
		}
		st, err := store.Open(filepath.Join(dataDir, "obsisync.db"))
		exitOn(err)
		exitOn(st.BackupDB(context.Background(), os.Args[2]))
		fmt.Println("Databáze uložena do", os.Args[2])
	case "healthcheck":
		exitOn(healthcheck(env("OBSISYNC_ADDR", ":8080")))
	case "version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
}

// healthcheck is used by Docker (the image has no curl).
func healthcheck(addr string) error {
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	c := &http.Client{Timeout: 5 * time.Second}
	res, err := c.Get("http://" + addr + "/api/v1/ping")
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	return nil
}

func exitOn(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "Chyba:", err)
		os.Exit(1)
	}
}

func resetPassword(dataDir, username, password string) error {
	if err := auth.ValidatePassword(password); err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(dataDir, "obsisync.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	u, err := st.UserByName(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		// Creating an admin this way is the recovery path for a lost account.
		_, err = st.CreateUser(ctx, username, hash, true)
		return err
	}
	if err != nil {
		return err
	}
	return st.SetPassword(ctx, u.ID, hash)
}

func serve(log *slog.Logger, dataDir string) error {
	a, err := app.New(app.Config{
		DataDir:   dataDir,
		BackupDir: os.Getenv("OBSISYNC_BACKUP_DIR"),
		PluginDir: env("OBSISYNC_PLUGIN_DIR", "/app/plugin"),
		Version:   version,
		Log:       log,
	})
	if err != nil {
		return err
	}
	defer a.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go a.Backup.Run(ctx)

	addr := env("OBSISYNC_ADDR", ":8080")
	srv := &http.Server{Addr: addr, Handler: a.Handler, ReadHeaderTimeout: 20 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Info("ObsiSync started", "version", version, "addr", addr, "data", dataDir)
	if n, _ := a.Store.CountUsers(ctx); n == 0 {
		host := addr
		if strings.HasPrefix(host, ":") {
			host = "localhost" + host
		}
		log.Info("První spuštění: otevři webové rozhraní a vytvoř administrátorský účet", "url", "http://"+host+"/setup")
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
