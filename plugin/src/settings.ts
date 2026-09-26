import { App, Modal, PluginSettingTab, Setting } from "obsidian";
import { normalizeServerUrl, type VaultInfo } from "./engine/client";
import { LANGUAGES, t } from "./i18n";
import type SimpleSyncPlugin from "./main";

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

export class SimpleSyncSettingTab extends PluginSettingTab {
	private error = "";
	private busy = false;
	private vaults: VaultInfo[] | null = null;
	private canCreate = false;
	private statusEl: HTMLElement | null = null;

	constructor(
		app: App,
		private plugin: SimpleSyncPlugin,
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
		this.languagePicker();
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

	private languagePicker() {
		new Setting(this.containerEl)
			.setName(t("settings.language"))
			.setDesc(t("settings.languageDesc"))
			.addDropdown((d) => {
				d.addOption("auto", t("settings.languageAuto"));
				for (const l of LANGUAGES) d.addOption(l.code, l.name);
				d.setValue(this.plugin.settings.language).onChange(async (v) => {
					await this.plugin.setLanguage(v);
					this.display();
				});
			});
	}

	// ---- step 1: server, name, password ----

	private loginForm() {
		const el = this.containerEl;
		el.createEl("p", { text: t("login.intro") });
		let url = this.plugin.settings.serverUrl;
		let user = this.plugin.settings.username;
		let pass = "";
		new Setting(el)
			.setName(t("login.server"))
			.setDesc(t("login.serverDesc"))
			.addText((c) =>
				c
					.setPlaceholder("https://…")
					.setValue(url)
					.onChange((v) => (url = v)),
			);
		new Setting(el).setName(t("login.name")).addText((c) => {
			c.setValue(user).onChange((v) => (user = v));
			c.inputEl.autocapitalize = "off";
			c.inputEl.autocomplete = "username";
		});
		const submit = () =>
			this.run(async () => {
				const server = normalizeServerUrl(url);
				if (!server || !user || !pass) throw new Error(t("login.missing"));
				await this.plugin.login(server, user.trim(), pass);
				this.vaults = null;
			});
		new Setting(el).setName(t("login.password")).addText((c) => {
			c.inputEl.type = "password";
			c.inputEl.autocomplete = "current-password";
			c.onChange((v) => (pass = v));
			c.inputEl.addEventListener("keydown", (e) => e.key === "Enter" && submit());
		});
		new Setting(el).addButton((b) =>
			b
				.setButtonText(this.busy ? t("login.busy") : t("login.button"))
				.setCta()
				.setDisabled(this.busy)
				.onClick(submit),
		);
	}

	// ---- step 2: pick a vault ----

	private account(el: HTMLElement) {
		const s = this.plugin.settings;
		new Setting(el)
			.setName(t("account.loggedIn", { user: s.username }))
			.setDesc(s.serverUrl)
			.addButton((b) => b.setButtonText(t("account.logout")).onClick(() => this.run(() => this.plugin.logout())));
	}

	private vaultPicker() {
		const el = this.containerEl;
		this.account(el);
		const refresh = () =>
			new Setting(el).addButton((b) =>
				b.setButtonText(t("vaults.refresh")).onClick(() => {
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
			el.createEl("p", { text: t("vaults.loading") });
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
				.setName(t("vaults.label"))
				.setDesc(t("vaults.desc"))
				.addDropdown((d) => {
					for (const v of vaults) d.addOption(String(v.id), `${v.name} (${t(`role.${v.role}`)})`);
					d.onChange((v) => (selected = Number(v)));
				})
				.addButton((b) =>
					b
						.setButtonText(t("vaults.connect"))
						.setCta()
						.setDisabled(this.busy)
						.onClick(() => this.connect(vaults.find((v) => v.id === selected)!)),
				);
		} else {
			el.createEl("p", { text: t("vaults.none") });
		}
		if (this.canCreate) {
			let name = this.app.vault.getName();
			new Setting(el)
				.setName(t("vaults.create"))
				.setDesc(t("vaults.createDesc"))
				.addText((c) => c.setValue(name).onChange((v) => (name = v)))
				.addButton((b) =>
					b
						.setButtonText(t("vaults.createButton"))
						.setDisabled(this.busy)
						.onClick(() =>
							this.run(async () => {
								const v = await this.plugin.client().createVault(name.trim());
								await this.plugin.connect(v);
							}),
						),
				);
		}
		refresh();
	}

	/**
	 * A server vault with content always wins: this vault becomes its copy.
	 * Confirm first when that would move local notes to the trash.
	 */
	private connect(vault: VaultInfo) {
		const serverHasContent = (vault.file_count ?? vault.head_rev) > 0;
		if (!serverHasContent || this.app.vault.getFiles().length === 0) {
			void this.run(() => this.plugin.connect(vault));
			return;
		}
		new ConfirmModal(this.app, t("replace.title", { vault: vault.name }), t("replace.body"), [
			{ label: t("replace.confirm"), cta: true, action: () => void this.run(() => this.plugin.connect(vault)) },
			{ label: t("replace.cancel"), action: () => {} },
		]).open();
	}

	// ---- step 3: connected ----

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
			.setName(t("connected.title", { vault: s.vaultName }))
			.setDesc(`${s.username} @ ${s.serverUrl}`)
			.addButton((b) =>
				b
					.setButtonText(t("connected.syncNow"))
					.setCta()
					.onClick(() => this.plugin.requestSync(0)),
			);
		this.statusEl = el.createDiv({ cls: "obsisync-statusline" });
		this.statusLine();

		new Setting(el)
			.setName(t("connected.syncConfig"))
			.setDesc(t("connected.syncConfigDesc"))
			.addToggle((c) =>
				c.setValue(s.syncConfig).onChange(async (v) => {
					s.syncConfig = v;
					await this.plugin.saveSettings();
					await this.plugin.restart(v);
				}),
			);
		new Setting(el)
			.setName(t("connected.disconnect"))
			.setDesc(t("connected.disconnectDesc"))
			.addButton((b) =>
				b.setButtonText(t("connected.disconnectButton")).onClick(() =>
					this.run(async () => {
						await this.plugin.disconnect();
						this.vaults = null;
					}),
				),
			);
		this.account(el);
	}
}
