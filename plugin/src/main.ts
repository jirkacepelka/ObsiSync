import { Notice, Platform, Plugin, requestUrl, TAbstractFile } from "obsidian";
import { ApiError, Client, type VaultInfo } from "./engine/client";
import { MassDeleteError, SyncEngine } from "./engine/engine";
import { makeIgnore } from "./engine/ignore";
import type { Http, SyncState } from "./engine/types";
import { ObsidianFS } from "./obsidian-fs";
import { ConfirmModal, ObsiSyncSettingTab } from "./settings";

export interface Settings {
	serverUrl: string;
	username: string;
	token: string;
	vaultId: number | null;
	vaultName: string;
	syncConfig: boolean;
}

const DEFAULTS: Settings = { serverUrl: "", username: "", token: "", vaultId: null, vaultName: "", syncConfig: false };

export type Status = { kind: "off" | "idle" | "syncing" | "error" | "offline"; text: string; at?: Date };

/** Http transport via Obsidian's requestUrl: no CORS, works on mobile. */
const obsidianHttp: Http = async (req) => {
	const res = await requestUrl({
		url: req.url,
		method: req.method,
		headers: req.headers,
		body: req.body,
		contentType: req.body === undefined ? undefined : req.contentType,
		throw: false,
	});
	let json: unknown;
	try {
		json = res.json;
	} catch {
		json = undefined;
	}
	return { status: res.status, json, arrayBuffer: res.arrayBuffer };
};

export default class ObsiSyncPlugin extends Plugin {
	settings: Settings = { ...DEFAULTS };
	status: Status = { kind: "off", text: "Nepřipojeno" };
	onStatusChange?: () => void;

	private engine: SyncEngine | null = null;
	private statusEl!: HTMLElement;
	private running: Promise<void> | null = null;
	private pending = false;
	private timer: number | null = null;
	private ws: WebSocket | null = null;
	private wsRetry = 0;
	private wsTimer: number | null = null;

	get deviceName(): string {
		const os = Platform.isIosApp ? "iPhone/iPad" : Platform.isAndroidApp ? "Android" : Platform.isMacOS ? "Mac" : Platform.isWin ? "Windows" : "Linux";
		return `${os} – ${this.app.vault.getName()}`;
	}

	get connected(): boolean {
		return !!this.settings.token && this.settings.vaultId !== null;
	}

	client(): Client {
		return new Client(this.settings.serverUrl, this.settings.token, obsidianHttp);
	}

	async onload() {
		this.settings = { ...DEFAULTS, ...(await this.loadData()) };
		this.addSettingTab(new ObsiSyncSettingTab(this.app, this));

		this.statusEl = this.addStatusBarItem();
		this.statusEl.addClass("obsisync-status");
		this.statusEl.onClickEvent(() => (this.connected ? this.requestSync(0) : this.openSettings()));
		this.addCommand({ id: "sync-now", name: "Synchronizovat nyní", callback: () => this.requestSync(0) });

		this.app.workspace.onLayoutReady(async () => {
			const onChange = (f: TAbstractFile) => {
				if (!f.path.startsWith(".")) this.requestSync(2000);
			};
			this.registerEvent(this.app.vault.on("create", onChange));
			this.registerEvent(this.app.vault.on("modify", onChange));
			this.registerEvent(this.app.vault.on("delete", onChange));
			this.registerEvent(this.app.vault.on("rename", onChange));
			this.registerInterval(window.setInterval(() => this.requestSync(0), 60_000));
			this.registerDomEvent(document, "visibilitychange", () => {
				if (document.visibilityState === "visible") {
					this.requestSync(500);
					if (this.connected && !this.ws) this.openSocket();
				}
			});
			if (this.connected) await this.start();
			else this.setStatus({ kind: "off", text: this.settings.token ? "Vyber vault v nastavení" : "Nepřihlášeno" });
		});
	}

	onunload() {
		this.closeSocket();
		if (this.timer) window.clearTimeout(this.timer);
	}

	async saveSettings() {
		await this.saveData(this.settings);
	}

	openSettings() {
		const setting = (this.app as any).setting;
		setting?.open();
		setting?.openTabById(this.manifest.id);
	}

	// ---- state ----

	private get statePath(): string {
		return `${this.manifest.dir}/state.json`;
	}

	private async loadState(): Promise<SyncState | null> {
		try {
			const s = JSON.parse(await this.app.vault.adapter.read(this.statePath)) as SyncState;
			return s.vaultId === this.settings.vaultId ? s : null;
		} catch {
			return null;
		}
	}

	private async saveState(s: SyncState) {
		await this.app.vault.adapter.write(this.statePath, JSON.stringify(s));
	}

	private makeEngine(state: SyncState): SyncEngine {
		return new SyncEngine({
			fs: new ObsidianFS(this.app, () => this.settings.syncConfig),
			client: this.client(),
			state,
			saveState: (s) => this.saveState(s),
			ignore: makeIgnore({ configDir: this.app.vault.configDir, syncConfig: this.settings.syncConfig, pluginId: this.manifest.id }),
			deviceName: this.deviceName,
			log: (m) => console.log("[ObsiSync]", m),
			onConflict: (path, copy) => new Notice(`ObsiSync: konflikt v „${path}“.\nTvoje verze je uložena jako „${copy}“.`, 15000),
		});
	}

	/** Starts syncing the configured vault (after load or after connecting). */
	async start(preferRemote = false) {
		const state = (await this.loadState()) ?? { vaultId: this.settings.vaultId!, lastRev: 0, base: {} };
		this.engine = this.makeEngine(state);
		this.engine.preferRemote = preferRemote;
		this.openSocket();
		this.requestSync(0);
	}

	/**
	 * Rebuilds the engine after a settings change. With rescan, the whole
	 * change feed is read again (files skipped by the old ignore rules).
	 */
	async restart(rescan = false) {
		if (!this.connected || !this.engine) return;
		await this.running;
		const state = this.engine.state;
		if (rescan) state.lastRev = 0;
		this.engine = this.makeEngine(state);
		this.requestSync(0);
	}

	// ---- account & vault ----

	async login(serverUrl: string, username: string, password: string) {
		const client = new Client(serverUrl, "", obsidianHttp);
		await client.ping();
		const token = await client.login(username, password, this.deviceName);
		this.settings = { ...this.settings, serverUrl, username, token, vaultId: null, vaultName: "" };
		await this.saveSettings();
		this.setStatus({ kind: "off", text: "Vyber vault v nastavení" });
	}

	async logout() {
		await this.disconnect();
		try {
			await this.client().logout();
		} catch {
			/* token may already be revoked */
		}
		this.settings.token = "";
		await this.saveSettings();
		this.setStatus({ kind: "off", text: "Nepřihlášeno" });
	}

	async connect(vault: VaultInfo, preferRemote: boolean) {
		await this.disconnect();
		this.settings.vaultId = vault.id;
		this.settings.vaultName = vault.name;
		await this.saveSettings();
		await this.saveState({ vaultId: vault.id, lastRev: 0, base: {} });
		await this.start(preferRemote);
	}

	async disconnect() {
		this.closeSocket();
		await this.running;
		this.engine = null;
		this.settings.vaultId = null;
		this.settings.vaultName = "";
		await this.saveSettings();
		this.setStatus({ kind: "off", text: "Vyber vault v nastavení" });
	}

	// ---- sync loop ----

	requestSync(delay: number) {
		if (!this.engine) return;
		if (this.timer) window.clearTimeout(this.timer);
		this.timer = window.setTimeout(() => {
			this.timer = null;
			void this.runSync();
		}, delay);
	}

	async runSync(): Promise<void> {
		if (!this.engine) return;
		if (this.running) {
			this.pending = true;
			return this.running;
		}
		const engine = this.engine;
		this.running = (async () => {
			this.setStatus({ kind: "syncing", text: "Synchronizuji…" });
			try {
				const r = await engine.sync();
				for (const p of r.problems) new Notice(`ObsiSync: ${p}`, 10000);
				this.setStatus({ kind: "idle", text: r.readOnly ? "Synchronizováno (jen čtení)" : "Synchronizováno", at: new Date() });
			} catch (e) {
				await this.handleError(e);
			}
		})();
		await this.running;
		this.running = null;
		if (this.pending) {
			this.pending = false;
			this.requestSync(0);
		}
	}

	private async handleError(e: unknown) {
		console.error("[ObsiSync]", e);
		if (e instanceof MassDeleteError) {
			this.setStatus({ kind: "error", text: `Pozastaveno: ${e.count} smazaných souborů` });
			new ConfirmModal(
				this.app,
				"ObsiSync: smazat soubory na serveru?",
				`V tomto zařízení chybí ${e.count} souborů, které jsou na serveru. Pokud jsi je smazal(a) záměrně, smaž je i na serveru. Pokud ne, stáhni je zpět.`,
				[
					{ label: "Stáhnout zpět ze serveru", cta: true, action: () => this.redownload() },
					{
						label: "Smazat i na serveru",
						warning: true,
						action: () => {
							if (this.engine) this.engine.allowMassDelete = true;
							this.requestSync(0);
						},
					},
				],
			).open();
			return;
		}
		if (e instanceof ApiError) {
			if (e.status === 401) {
				this.closeSocket();
				this.engine = null;
				this.setStatus({ kind: "error", text: "Přihlášení vypršelo – přihlas se znovu" });
				new Notice("ObsiSync: přihlášení vypršelo nebo bylo zařízení odhlášeno. Přihlas se znovu v nastavení.", 15000);
				return;
			}
			if (e.status === 404 && e.code === "not_found") {
				this.setStatus({ kind: "error", text: "Vault už na serveru neexistuje nebo k němu nemáš přístup" });
				return;
			}
			if (e.status === 0) {
				this.setStatus({ kind: "offline", text: "Server nedostupný, zkusím později" });
				return;
			}
		}
		this.setStatus({ kind: "error", text: `Chyba: ${e instanceof Error ? e.message : String(e)}` });
	}

	/** Forgets what was synced so that missing files are downloaded again. */
	private async redownload() {
		if (!this.engine) return;
		const state = this.engine.state;
		state.base = {};
		state.lastRev = 0;
		await this.saveState(state);
		this.engine = this.makeEngine(state);
		this.requestSync(0);
	}

	// ---- live notifications ----

	private openSocket() {
		this.closeSocket();
		if (!this.connected) return;
		let ws: WebSocket;
		try {
			ws = new WebSocket(this.client().wsUrl(this.settings.vaultId!));
		} catch {
			return;
		}
		this.ws = ws;
		ws.onopen = () => (this.wsRetry = 0);
		ws.onmessage = (ev) => {
			try {
				const rev = JSON.parse(String(ev.data)).rev as number;
				if (this.engine && rev > this.engine.state.lastRev) this.requestSync(300);
			} catch {
				/* ignore */
			}
		};
		ws.onclose = () => {
			if (this.ws !== ws) return;
			this.ws = null;
			if (!this.connected) return;
			// Reconnect with backoff; the 60 s poll covers the gap.
			const delay = Math.min(60_000, 1000 * 2 ** this.wsRetry++);
			this.wsTimer = window.setTimeout(() => this.openSocket(), delay);
		};
	}

	private closeSocket() {
		if (this.wsTimer) window.clearTimeout(this.wsTimer);
		this.wsTimer = null;
		const ws = this.ws;
		this.ws = null;
		ws?.close();
	}

	// ---- status bar ----

	setStatus(s: Status) {
		this.status = s;
		const icon = { off: "○", idle: "✓", syncing: "⟳", error: "⚠", offline: "⚡" }[s.kind];
		this.statusEl.setText(`${icon} ObsiSync`);
		this.statusEl.setAttr("aria-label", s.text + (s.at ? ` (${s.at.toLocaleTimeString()})` : ""));
		this.statusEl.setAttr("data-tooltip-position", "top");
		this.statusEl.toggleClass("is-error", s.kind === "error");
		this.onStatusChange?.();
	}
}
