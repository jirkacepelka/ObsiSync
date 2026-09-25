import { App, Modal, PluginSettingTab, Setting } from "obsidian";
import { normalizeServerUrl, type VaultInfo } from "./engine/client";
import type ObsiSyncPlugin from "./main";

interface Choice {
	label: string;
	cta?: boolean;
	warning?: boolean;
	action: () => void;
}

/** A small dialog with a message and a few buttons. */
export class ConfirmModal extends Modal {
	constructor(
		app: App,
		private heading: string,
		private message: string,
		private choices: Choice[],
	) {
		super(app);
	}

	onOpen() {
		this.titleEl.setText(this.heading);
		this.contentEl.createEl("p", { text: this.message, cls: "obsisync-message" });
		const row = new Setting(this.contentEl);
		for (const c of this.choices) {
			row.addButton((b) => {
				b.setButtonText(c.label).onClick(() => {
					this.close();
					c.action();
				});
				if (c.cta) b.setCta();
				if (c.warning) b.setWarning();
			});
		}
	}

	onClose() {
		this.contentEl.empty();
	}
}

const ROLE: Record<string, string> = { owner: "vlastník", editor: "úpravy", viewer: "jen čtení" };

export class ObsiSyncSettingTab extends PluginSettingTab {
	private error = "";
	private busy = false;
	private vaults: VaultInfo[] | null = null;
	private canCreate = false;

	constructor(
		app: App,
		private plugin: ObsiSyncPlugin,
	) {
		super(app, plugin);
	}

	hide() {
		this.plugin.onStatusChange = undefined;
	}

	display() {
		const { containerEl } = this;
		containerEl.empty();
		containerEl.addClass("obsisync-settings");
		this.plugin.onStatusChange = () => this.statusLine();
		const s = this.plugin.settings;
		if (!s.token) this.loginForm();
		else if (s.vaultId === null) this.vaultPicker();
		else this.connectedView();
		if (this.error) containerEl.createDiv({ cls: "obsisync-error", text: this.error });
	}

	private async run(fn: () => Promise<void>) {
		this.busy = true;
		this.error = "";
		this.display();
		try {
			await fn();
		} catch (e) {
			this.error = e instanceof Error ? e.message : String(e);
		}
		this.busy = false;
		this.display();
	}

	// ---- step 1: server, name, password ----

	private loginForm() {
		const el = this.containerEl;
		el.createEl("p", { text: "Vyplň adresu svého ObsiSync serveru a své přihlašovací údaje." });
		let url = this.plugin.settings.serverUrl;
		let user = this.plugin.settings.username;
		let pass = "";
		new Setting(el).setName("Adresa serveru").setDesc("Např. https://sync.mojedomena.cz nebo 192.168.1.10:8080").addText((t) =>
			t
				.setPlaceholder("https://…")
				.setValue(url)
				.onChange((v) => (url = v)),
		);
		new Setting(el).setName("Jméno").addText((t) => {
			t.setValue(user).onChange((v) => (user = v));
			t.inputEl.autocapitalize = "off";
			t.inputEl.autocomplete = "username";
		});
		const submit = () =>
			this.run(async () => {
				const server = normalizeServerUrl(url);
				if (!server || !user || !pass) throw new Error("Vyplň adresu serveru, jméno i heslo.");
				await this.plugin.login(server, user.trim(), pass);
				this.vaults = null;
			});
		new Setting(el).setName("Heslo").addText((t) => {
			t.inputEl.type = "password";
			t.inputEl.autocomplete = "current-password";
			t.onChange((v) => (pass = v));
			t.inputEl.addEventListener("keydown", (e) => e.key === "Enter" && submit());
		});
		new Setting(el).addButton((b) =>
			b
				.setButtonText(this.busy ? "Přihlašuji…" : "Přihlásit")
				.setCta()
				.setDisabled(this.busy)
				.onClick(submit),
		);
	}

	// ---- step 2: pick a vault ----

	private account(el: HTMLElement) {
		const s = this.plugin.settings;
		new Setting(el)
			.setName(`Přihlášen(a) jako ${s.username}`)
			.setDesc(s.serverUrl)
			.addButton((b) => b.setButtonText("Odhlásit").onClick(() => this.run(() => this.plugin.logout())));
	}

	private vaultPicker() {
		const el = this.containerEl;
		this.account(el);
		const refresh = () =>
			new Setting(el).addButton((b) =>
				b.setButtonText("Obnovit seznam").onClick(() => {
					this.vaults = null;
					this.error = "";
					this.display();
				}),
			);
		if (this.vaults === null) {
			if (this.error) {
				refresh();
				return;
			}
			el.createEl("p", { text: "Načítám seznam vaultů…" });
			if (!this.busy) {
				void this.run(async () => {
					const r = await this.plugin.client().vaults();
					this.vaults = r.vaults;
					this.canCreate = r.can_create;
				});
			}
			return;
		}
		const vaults = this.vaults;
		let selected = vaults[0]?.id;
		if (vaults.length) {
			new Setting(el)
				.setName("Vault na serveru")
				.setDesc("Vyber, který vault se má synchronizovat s tímto vaultem v Obsidianu.")
				.addDropdown((d) => {
					for (const v of vaults) d.addOption(String(v.id), `${v.name} (${ROLE[v.role] ?? v.role})`);
					d.onChange((v) => (selected = Number(v)));
				})
				.addButton((b) =>
					b
						.setButtonText("Připojit")
						.setCta()
						.setDisabled(this.busy)
						.onClick(() => this.connect(vaults.find((v) => v.id === selected)!)),
				);
		} else {
			el.createEl("p", { text: "Tvůj účet zatím nemá přístup k žádnému vaultu." });
		}
		if (this.canCreate) {
			let name = this.app.vault.getName();
			new Setting(el)
				.setName("Nebo vytvoř nový vault z tohoto")
				.setDesc("Na serveru vznikne nový vault a nahraje se do něj obsah tohoto vaultu.")
				.addText((t) => t.setValue(name).onChange((v) => (name = v)))
				.addButton((b) =>
					b
						.setButtonText("Vytvořit a připojit")
						.setDisabled(this.busy)
						.onClick(() =>
							this.run(async () => {
								const v = await this.plugin.client().createVault(name.trim());
								await this.plugin.connect(v, false);
							}),
						),
				);
		}
		refresh();
	}

	private connect(vault: VaultInfo) {
		const localFiles = this.app.vault.getFiles().length;
		if (localFiles === 0 || vault.head_rev === 0) {
			void this.run(() => this.plugin.connect(vault, false));
			return;
		}
		new ConfirmModal(
			this.app,
			`Připojit k „${vault.name}“`,
			`Tento vault už obsahuje ${localFiles} souborů a vault na serveru také není prázdný.\n\n` +
				"Sloučit: soubory z obou stran zůstanou, rozdílné verze se sloučí nebo uloží jako konfliktní kopie.\n" +
				"Server má přednost: u rozdílných souborů vyhraje verze ze serveru; soubory, které jsou jen tady, se nahrají.",
			[
				{ label: "Sloučit (doporučeno)", cta: true, action: () => void this.run(() => this.plugin.connect(vault, false)) },
				{ label: "Server má přednost", action: () => void this.run(() => this.plugin.connect(vault, true)) },
			],
		).open();
	}

	// ---- step 3: connected ----

	private statusEl: HTMLElement | null = null;

	private statusLine() {
		if (!this.statusEl) return;
		const st = this.plugin.status;
		this.statusEl.setText(st.text + (st.at ? ` · ${st.at.toLocaleTimeString()}` : ""));
		this.statusEl.toggleClass("obsisync-error", st.kind === "error");
	}

	private connectedView() {
		const el = this.containerEl;
		const s = this.plugin.settings;
		new Setting(el)
			.setName(`Připojeno k vaultu „${s.vaultName}“`)
			.setDesc(`${s.username} @ ${s.serverUrl}`)
			.addButton((b) => b.setButtonText("Synchronizovat nyní").setCta().onClick(() => this.plugin.requestSync(0)));
		this.statusEl = el.createDiv({ cls: "obsisync-statusline" });
		this.statusLine();

		new Setting(el)
			.setName("Synchronizovat i nastavení Obsidianu")
			.setDesc("Složka .obsidian (vzhled, pluginy, klávesové zkratky). Změny se projeví po restartu Obsidianu.")
			.addToggle((t) =>
				t.setValue(s.syncConfig).onChange(async (v) => {
					s.syncConfig = v;
					await this.plugin.saveSettings();
					await this.plugin.restart(v);
				}),
			);
		new Setting(el)
			.setName("Odpojit vault")
			.setDesc("Soubory zůstanou v zařízení i na serveru, jen se přestanou synchronizovat.")
			.addButton((b) =>
				b.setButtonText("Odpojit").onClick(() =>
					this.run(async () => {
						await this.plugin.disconnect();
						this.vaults = null;
					}),
				),
			);
		this.account(el);
	}
}
