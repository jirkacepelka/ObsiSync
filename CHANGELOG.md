# Changelog

Each `## <version>` section below becomes the description of that GitHub release.

## 0.3.3

- Plugin settings use Obsidian's declarative settings API (Obsidian 1.13+), so they show up in the settings search. Older Obsidian versions keep the same settings page.
- Stricter typing of server responses in the plugin (no `any`).
- Releases now contain only `main.js`, `manifest.json` and `styles.css`, with GitHub build provenance attestations.

About vault access: SimpleSync is a sync plugin, so it lists all files in the vault to compare them with your own SimpleSync server. File names and contents are sent only to the server address you enter, nowhere else.

## 0.3.2

- Renamed to SimpleSync (plugin id `simplesync`).

## 0.3.1

- The whole product (server, web admin, plugin) uses one name.

## 0.3.0

- Name that follows the Obsidian plugin naming guidelines.

## 0.2.0

- Server-first connect: when the server vault already has content, this device becomes its copy and nothing is uploaded. Local files that differ go to Obsidian's trash.
- English UI with a language picker (English, Čeština, Slovenčina, Deutsch, Français, Español, Italiano, Polski) in the plugin and the web admin.

## 0.1.0

- First version: sync server with web admin, scheduled vault backups and the Obsidian plugin.
