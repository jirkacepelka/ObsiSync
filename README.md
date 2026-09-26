# SimpleSync

Simple self-hosted sync for [Obsidian](https://obsidian.md): a small server plus an Obsidian plugin. No CouchDB, no S3 keys, no config files to tune.

In Obsidian you enter **server address, name and password**, pick a vault from the list, and you're done.

- 🖥️ **Server**: one small Docker container (Go + SQLite, ~27 MB image, tens of MB of RAM). It runs on ZimaOS, a NAS or the cheapest VPS.
- 🌐 **Web admin**: users, vaults, sharing, devices, version history, trash and backups. Available in English, Čeština, Slovenčina, Deutsch, Français, Español, Italiano and Polski.
- 📱 **Obsidian plugin**: desktop and mobile (iOS/Android), changes arrive within seconds. Same languages as the web admin.
- 🔀 **Safe conflicts**: concurrent edits of different parts of a note are merged. When that's not possible, nothing is lost: a copy named `… (conflict …)` is kept.
- 💾 **Vault backups**: for each vault you choose how often to back up and how long to keep backups. Restoring takes one click.

---

## Quick start

### A) ZimaOS (or CasaOS)

1. Open **App Store → "+" → Install a customized app**, switch to **YAML**, delete what's there (Ctrl+A, Delete) and paste this (also in [`deploy/zimaos/docker-compose.yml`](deploy/zimaos/docker-compose.yml)):

   ```yaml
   name: obsisync
   services:
     obsisync:
       image: ghcr.io/jirkacepelka/obsisync:latest
       container_name: obsisync
       restart: unless-stopped
       ports:
         - target: 8080
           published: "8080"
           protocol: tcp
       environment:
         TZ: Europe/Prague
       volumes:
         - type: bind
           source: /DATA/AppData/obsisync/data
           target: /data
   x-casaos:
     architectures:
       - amd64
       - arm64
     main: obsisync
     category: Utilities
     title:
       en_us: SimpleSync
     tagline:
       en_us: Simple self-hosted sync for Obsidian
     icon: https://raw.githubusercontent.com/jirkacepelka/SimpleSync/main/server/internal/web/static/icon.svg
     index: /
     port_map: "8080"
     scheme: http
   ```

2. Click **Install**, then open `http://<zimaos-ip>:8080`.
3. Create the administrator account (first-start wizard).

Data lives in `/DATA/AppData/obsisync/data`.

> **Access from outside your home network** (a phone on mobile data): the easiest option is [Tailscale](https://tailscale.com) (available in the ZimaOS App Store) or a Cloudflare Tunnel. HTTPS is recommended for iOS; Tailscale provides free HTTPS certificates (`tailscale serve`).

### B) Cheap VPS with your own domain (automatic HTTPS)

A 1 GB RAM VPS with Docker is enough.

```bash
# 1) DNS: an A record  sync.example.com → the VPS IP address
# 2) on the VPS:
mkdir obsisync && cd obsisync
curl -O https://raw.githubusercontent.com/jirkacepelka/SimpleSync/main/deploy/docker-compose.caddy.yml
DOMAIN=sync.example.com docker compose -f docker-compose.caddy.yml up -d
```

Open `https://sync.example.com` and create the administrator. Caddy obtains the Let's Encrypt certificate by itself.

### C) Anything else with Docker

```bash
docker run -d --name obsisync -p 8080:8080 -e TZ=Europe/Prague \
  -v $PWD/data:/data --restart unless-stopped ghcr.io/jirkacepelka/obsisync:latest
```

### Updating

Pull the new image and recreate the container (on ZimaOS: the app's settings → update / reinstall). Your data in `/data` is kept.

---

## Connecting Obsidian

1. **Install the plugin.** In the web admin click **Plugin → Download plugin (ZIP)**, unzip it into `<vault>/.obsidian/plugins/` (a `simplesync` folder appears) and enable *Settings → Community plugins → SimpleSync*. On a phone, the easiest way is the **BRAT** plugin with the repository `jirkacepelka/SimpleSync`.
2. In the plugin settings enter the **server address, name and password**, then click **Log in**.
3. Pick a vault from the list and click **Connect**. Or use **Create a new vault from this one**, which uploads the current vault to the server.

The status bar icon shows the state: ✓ synced, ⟳ syncing, ⚡ server unreachable, ⚠ error. Click it to sync right away. The plugin language follows Obsidian; you can change it in the plugin settings.

### What happens when you connect

- **The server vault has content:** it always wins. This device becomes an exact copy of the server and **nothing is uploaded** from it. Local files that differ from the server, or exist only on this device, are moved to Obsidian's trash (`.trash`), so nothing is lost. An empty or brand-new Obsidian vault can never overwrite the server.
- **The server vault is empty:** the content of this device is uploaded to it.
- If the first sync is interrupted (network drops, app closed), the next sync continues in the same "copy the server" mode.

After the first sync, both directions sync normally.

---

## Web admin

| Section | What it does |
|---|---|
| **Overview** | vaults, disk usage, devices online, alerts about failed backups |
| **Vaults** | create a vault (name, **backup frequency**, **how long to keep backups**, members) |
| → Files | browse folders, preview notes and images, **version history** with restore, download the whole vault as ZIP |
| → Trash | deleted files and restoring them |
| → Backups | list of backups, **Back up now**, ZIP download, restore a single file or the **whole vault** |
| → Members | share the vault with other users: *Owner* / *Editor* / *Read only* |
| → Settings | rename, change the backup plan, delete the vault |
| **Users** | create accounts, reset passwords, administrators |
| **Devices** | every Obsidian login; logging a device out removes its access immediately |
| **Settings** | how long to keep version history, maximum file size, whether users may create vaults |

The language picker is at the bottom of every page.

### Backups

When creating a vault, the administrator chooses:

- **Backup frequency:** off / every hour / every 6 hours / daily / weekly
- **Keep backups for:** 7 days / 30 days / 90 days / 1 year / forever
- optionally **also save as ZIP**: every backup is additionally written as a standalone ZIP file to `data/backups/`, handy for copying to another disk

How it works:
- A backup is a snapshot of the whole vault. Identical file content is stored only once on disk, so backups take almost no extra space.
- If the vault hasn't changed since the last backup, the scheduled backup is skipped.
- Old backups are removed according to the retention setting; **the newest backup is never deleted**.
- **Restoring the whole vault** first saves the current state as a "Before restore" backup, then sends the changes to every device like a normal sync.

### Backing up the whole server

Everything (database, file contents, ZIP backups) is in the `data/` folder. Back up that folder. For a consistent copy of the database while running:

```bash
docker exec obsisync obsisync backup-db /data/obsisync-backup.db
```

### Forgotten administrator password

```bash
docker exec obsisync obsisync reset-password admin NewPassword123
```

(If the user doesn't exist, it is created as an administrator.)

---

## Network use and privacy

The SimpleSync plugin communicates **only with the SimpleSync server whose address you enter**, a server you run yourself. It sends your login once to obtain a device token (the password is not stored), then uploads and downloads the files of the connected vault. There is no telemetry, no third-party service and no account with anyone else. Content is protected in transit by HTTPS when the server is reachable over HTTPS; it is not end-to-end encrypted, so whoever runs the server can read the notes stored on it.

---

## Architecture

```
Obsidian (desktop / mobile)                 Server (1 Docker container)
┌───────────────────────┐   HTTPS (REST)    ┌────────────────────────────────┐
│ SimpleSync plugin    │◄────────────────►│ SimpleSync server (Go)        │
│  • 3 fields + picker  │   WebSocket       │  • /api/v1  sync               │
│  • sync engine        │◄──────────────────│  • /        web admin          │
│  • 3-way merge        │  ("new revision") │  • SQLite   metadata, history  │
└───────────────────────┘                   │  • blobs/   content (SHA-256)  │
                                            │  • backup scheduler + upkeep   │
                                            └────────────────────────────────┘
```

**How sync works**

- File contents are stored by their SHA-256 hash (content-addressed). This gives deduplication and history, and renames need no re-upload.
- Every vault has a **growing revision number**. A device remembers the last revision it saw and fetches only changes after it.
- For every file, each device remembers the "base" hash: the version it last agreed on with the server. Writes to the server are **compare-and-swap**: a write only succeeds if the server still has that base version. Otherwise it's a conflict:
  - text files: **3-way merge** (changes on different lines are combined),
  - overlapping changes and binary files: the server version wins and the local one is saved as `Note (conflict 2026-09-25 1530 iPhone).md`,
  - edit vs. delete: the edit wins.
- Over WebSocket the server only says "the vault has a new revision"; devices then fetch the changes. As a fallback the plugin syncs every minute, 2 s after a file changes, and when the app comes back to the foreground.
- Paths are normalized to Unicode NFC (macOS/iOS vs. Windows vs. Linux). Paths that differ only in letter case are rejected, because they would collide on Windows/macOS.
- Safety net: if a sync would delete more than half of the files on the server (for example, the vault didn't load), the plugin asks first.
- Not synced: `.trash/`, `.git/`, other hidden folders, `workspace.json` and the plugin's own data (token). The `.obsidian` folder can be enabled with a toggle.

**API**: everything is under `/api/v1`, authorized with `Authorization: Bearer <device token>`:

| Endpoint | Purpose |
|---|---|
| `POST /auth/login` | name + password → device token (the password is not stored on the device) |
| `GET /vaults`, `POST /vaults` | the user's vaults / create one |
| `GET /vaults/{id}/changes?since=REV` | changes since a revision |
| `POST /vaults/{id}/blobs/missing` | which content the server doesn't have yet |
| `PUT` / `GET /vaults/{id}/blobs/{sha256}` | upload / download content |
| `POST /vaults/{id}/commit` | a batch of changes (compare-and-swap) |
| `GET /vaults/{id}/ws` | WebSocket notifications |

### Repository layout

```
server/     Go server (cmd/obsisync, internal/{store,api,web,backup,blobs,auth,hub,i18n})
plugin/     Obsidian plugin (TypeScript); src/engine is Obsidian-independent and tested
deploy/     docker-compose for home network, VPS with Caddy, and ZimaOS
Dockerfile  multi-arch image (amd64 + arm64) with the server and the plugin
```

### Development

```bash
# server
cd server && go test ./... && go run ./cmd/obsisync    # http://localhost:8080, data in ./data

# plugin (tests start a real server and simulate several devices)
cd plugin && npm ci && npm test && npm run build       # output in plugin/dist
```

Translations: the web admin uses `server/internal/i18n/locales/<lang>.json`, the plugin uses `plugin/src/i18n/<lang>.ts`. English is the source; missing keys fall back to English.

Releases: every push to `main` makes GitHub Actions build the Docker image `ghcr.io/jirkacepelka/obsisync` (`latest` and the version from `manifest.json`) and a GitHub release with the plugin files. To publish a new version, bump `version` in `manifest.json`, `plugin/manifest.json` and `versions.json`.

### Environment variables (optional)

| Variable | Default | Meaning |
|---|---|---|
| `TZ` | UTC | time zone for displayed times (e.g. `Europe/Prague`) |
| `OBSISYNC_DATA` | `/data` | data folder |
| `OBSISYNC_ADDR` | `:8080` | listen address and port |
| `OBSISYNC_BACKUP_DIR` | `$OBSISYNC_DATA/backups` | where ZIP backups go |

## License

[MIT](LICENSE)
