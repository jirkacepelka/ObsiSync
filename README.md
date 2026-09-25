# ObsiSync

Jednoduchý vlastní synchronizační server pro [Obsidian](https://obsidian.md). Žádné CouchDB, žádné S3 klíče, žádné ladění konfigurace.

V Obsidianu vyplníš jen **adresu serveru, jméno a heslo**, vybereš vault ze seznamu a je hotovo.

- 🖥️ **Server** – jeden malý Docker kontejner (Go + SQLite, ~27 MB, desítky MB RAM), běží na ZimaOS, NASu i nejlevnějším VPS.
- 🌐 **Webové rozhraní** – uživatelé, vaulty, sdílení, zařízení, historie verzí, koš a zálohy.
- 📱 **Plugin pro Obsidian** – desktop i mobil (iOS/Android), synchronizace během pár sekund.
- 🔀 **Chytré konflikty** – souběžné úpravy různých míst poznámky se sloučí; když to nejde, nic se neztratí (vznikne kopie „… (konflikt …)“).
- 💾 **Zálohy vaultů** – pro každý vault nastavíš, jak často se má zálohovat a jak dlouho zálohy držet; obnova jedním kliknutím.

---

## Rychlý start

### A) ZimaOS (nebo CasaOS)

1. Otevři **App Store → „+“ → Custom Install / Import** a vlož obsah souboru [`deploy/zimaos/docker-compose.yml`](deploy/zimaos/docker-compose.yml).
2. Nainstaluj a otevři `http://<ip-zimaos>:8080`.
3. Vytvoř administrátorský účet (průvodce prvním spuštěním).

Data jsou v `/DATA/AppData/obsisync/data`.

> **Přístup mimo domácí síť (telefon na mobilních datech):** nejjednodušší je [Tailscale](https://tailscale.com) (ZimaOS ho umí nainstalovat z App Storu) nebo Cloudflare Tunnel. Na iOS doporučujeme HTTPS – Tailscale umí HTTPS certifikát zdarma (`tailscale serve`).

### B) Levné VPS s vlastní doménou (automatické HTTPS)

Stačí VPS s 1 GB RAM (např. Hetzner CX22) a Docker.

```bash
# 1) DNS: záznam typu A  sync.mojedomena.cz → IP adresa VPS
# 2) na VPS:
mkdir obsisync && cd obsisync
curl -O https://raw.githubusercontent.com/jirkacepelka/ObsiSync/main/deploy/docker-compose.caddy.yml
DOMAIN=sync.mojedomena.cz docker compose -f docker-compose.caddy.yml up -d
```

Otevři `https://sync.mojedomena.cz` a vytvoř administrátora. Certifikát Let's Encrypt zařídí Caddy sám.

### C) Cokoliv jiného s Dockerem

```bash
docker run -d --name obsisync -p 8080:8080 -e TZ=Europe/Prague \
  -v $PWD/data:/data --restart unless-stopped ghcr.io/jirkacepelka/obsisync:latest
```

---

## Připojení Obsidianu

1. **Nainstaluj plugin** – ve webovém rozhraní klikni na **Plugin → Stáhnout plugin (ZIP)**. Rozbal ho do `<vault>/.obsidian/plugins/` a v Obsidianu zapni *Nastavení → Komunitní pluginy → ObsiSync*.
   Na telefonu je nejsnazší plugin **BRAT** s repozitářem `jirkacepelka/ObsiSync`.
2. V nastavení pluginu vyplň **adresu serveru, jméno a heslo** → **Přihlásit**.
3. Vyber vault ze seznamu → **Připojit**. Případně **Vytvořit nový vault z tohoto** (nahraje obsah aktuálního vaultu na server).

Hotovo. Ikona ve stavové liště ukazuje stav (✓ synchronizováno, ⟳ probíhá, ⚡ server nedostupný, ⚠ chyba). Kliknutím na ni synchronizuješ hned.

Pokud má lokální i serverový vault už nějaký obsah, plugin se zeptá, jak je spojit:
- **Sloučit** (doporučeno) – nic se neztratí; rozdílné soubory se sloučí, nebo vznikne konfliktní kopie.
- **Server má přednost** – u rozdílných souborů vyhraje verze ze serveru.

---

## Správa serveru (webové rozhraní)

| Sekce | Co umí |
|---|---|
| **Přehled** | vaulty, obsazené místo, zařízení online, upozornění na selhané zálohy |
| **Vaulty** | založení vaultu (název, **frekvence záloh**, **doba držení záloh**, členové) |
| → Soubory | procházení složek, náhled poznámek a obrázků, **historie verzí** s obnovením, stažení celého vaultu jako ZIP |
| → Koš | smazané soubory a jejich obnova |
| → Zálohy | seznam záloh, **Zálohovat teď**, stažení ZIP, obnova jednoho souboru nebo **celého vaultu** |
| → Členové | sdílení vaultu s dalšími uživateli: *Vlastník* / *Úpravy* / *Jen čtení* |
| → Nastavení | přejmenování, změna plánu záloh, smazání vaultu |
| **Uživatelé** | zakládání účtů, reset hesla, administrátoři |
| **Zařízení** | každé přihlášení z Obsidianu; odhlášení zařízení mu okamžitě vezme přístup |
| **Nastavení** | jak dlouho držet historii verzí, max. velikost souboru, zda mohou uživatelé zakládat vaulty |

### Zálohy

Při zakládání vaultu administrátor vybere:

- **Frekvence záloh:** vypnuto / každou hodinu / každých 6 hodin / denně / týdně
- **Doba držení záloh:** 7 dní / 30 dní / 90 dní / 1 rok / navždy
- volitelně **ukládat i jako ZIP** – každá záloha se navíc uloží jako samostatný ZIP soubor do `data/backups/` (vhodné pro kopírování na jiný disk)

Jak to funguje:
- Záloha je snímek celého vaultu. Stejný obsah souboru se na disku ukládá jen jednou, takže zálohy skoro nezabírají místo navíc.
- Pokud se vault od poslední zálohy nezměnil, plánovaná záloha se přeskočí.
- Staré zálohy se mažou podle doby držení; **nejnovější záloha se nemaže nikdy**.
- **Obnova celého vaultu** nejdřív uloží aktuální stav jako zálohu „Před obnovou“ a pak změny rozešle do všech zařízení jako běžnou synchronizaci.

### Záloha celého serveru

Všechno (databáze, obsah souborů, ZIP zálohy) je ve složce `data/`. Stačí ji zálohovat. Konzistentní kopii databáze za běhu udělá:

```bash
docker exec obsisync obsisync backup-db /data/obsisync-backup.db
```

### Zapomenuté heslo administrátora

```bash
docker exec obsisync obsisync reset-password admin NoveHeslo123
```

(Pokud uživatel neexistuje, vytvoří se jako administrátor.)

---

## Architektura

```
Obsidian (desktop / mobil)                  Server (1 Docker kontejner)
┌───────────────────────┐   HTTPS (REST)    ┌────────────────────────────────┐
│ plugin ObsiSync       │◄────────────────►│ obsisync (Go)                  │
│  • 3 pole + výběr     │   WebSocket       │  • /api/v1  synchronizace      │
│  • sync engine        │◄──────────────────│  • /        webové rozhraní    │
│  • 3-way merge        │  („je nová verze“)│  • SQLite   metadata, historie │
└───────────────────────┘                   │  • blobs/   obsah (SHA-256)    │
                                            │  • plánovač záloh + údržba     │
                                            └────────────────────────────────┘
```

**Princip synchronizace**

- Obsah souborů se ukládá podle SHA-256 hashe (content-addressed) → deduplikace, historie a přejmenování bez opakovaného nahrávání.
- Každý vault má **rostoucí číslo revize**. Zařízení si pamatuje poslední revizi, kterou vidělo, a stahuje jen změny od ní.
- Každé zařízení si pro každý soubor pamatuje „základní“ hash – verzi, na které se naposledy shodlo se serverem. Zápis na server je **compare-and-swap**: projde jen tehdy, když server pořád má tuto základní verzi. Jinak jde o konflikt:
  - textové soubory → **3-way merge** (změny na různých řádcích se sloučí),
  - překrývající se změny a binární soubory → vítězí verze ze serveru, lokální verze se uloží jako `Poznámka (konflikt 2026-09-25 1530 iPhone).md`,
  - úprava vs. smazání → úprava vyhrává.
- Server rozesílá přes WebSocket jen „ve vaultu je nová revize“; zařízení si pak změny stáhne. Záložně se synchronizuje každou minutu, po úpravě souboru (2 s) a při návratu do aplikace.
- Cesty se normalizují do Unicode NFC (macOS/iOS × Windows × Linux – důležité pro češtinu). Soubory lišící se jen velikostí písmen jsou odmítnuty (kolidovaly by na Windows/macOS).
- Pojistka: když by synchronizace smazala na serveru víc než polovinu souborů (např. vault se nenačetl), plugin se nejdřív zeptá.
- Nesynchronizuje se: `.trash/`, `.git/`, ostatní skryté složky, `workspace.json` a vlastní data pluginu (token). Složku `.obsidian` lze zapnout přepínačem.

**API (pro zvídavé)** – vše pod `/api/v1`, autorizace `Authorization: Bearer <token zařízení>`:

| Endpoint | Účel |
|---|---|
| `POST /auth/login` | jméno + heslo → token zařízení (heslo se v zařízení neukládá) |
| `GET /vaults`, `POST /vaults` | seznam vaultů uživatele / založení nového |
| `GET /vaults/{id}/changes?since=REV` | změny od revize |
| `POST /vaults/{id}/blobs/missing` | který obsah server ještě nemá |
| `PUT` / `GET /vaults/{id}/blobs/{sha256}` | nahrání / stažení obsahu |
| `POST /vaults/{id}/commit` | dávka změn (compare-and-swap) |
| `GET /vaults/{id}/ws` | WebSocket notifikace |

### Struktura repozitáře

```
server/     Go server (cmd/obsisync, internal/{store,api,web,backup,blobs,auth,hub})
plugin/     Obsidian plugin (TypeScript); src/engine je nezávislý na Obsidianu a testovaný
deploy/     docker-compose pro domácí síť, VPS s Caddy a ZimaOS
Dockerfile  multi-arch image (amd64 + arm64) se serverem i pluginem
```

### Vývoj

```bash
# server
cd server && go test ./... && go run ./cmd/obsisync    # http://localhost:8080, data v ./data

# plugin (testy spouští skutečný server a simulují více zařízení)
cd plugin && npm ci && npm test && npm run build       # výstup v plugin/dist
```

Vydání: každý push do `main` → GitHub Actions sestaví Docker image `ghcr.io/jirkacepelka/obsisync` a GitHub Release s pluginem.
Po prvním vydání nastav v GitHubu balíček `obsisync` (Packages) jako **Public**, aby ho ZimaOS/VPS stáhly bez přihlášení.

### Proměnné prostředí (volitelné)

| Proměnná | Výchozí | Význam |
|---|---|---|
| `TZ` | UTC | časové pásmo pro zobrazení (např. `Europe/Prague`) |
| `OBSISYNC_DATA` | `/data` | datová složka |
| `OBSISYNC_ADDR` | `:8080` | adresa a port |
| `OBSISYNC_BACKUP_DIR` | `$OBSISYNC_DATA/backups` | kam ukládat ZIP zálohy |
