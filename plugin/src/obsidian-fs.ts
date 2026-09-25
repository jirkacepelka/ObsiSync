import { normalizePath, TFile, TFolder, type App } from "obsidian";
import type { FileStat, LocalFS } from "./engine/types";

/** LocalFS backed by the Obsidian vault (works on desktop and mobile). */
export class ObsidianFS implements LocalFS {
	constructor(
		private app: App,
		private includeConfig: () => boolean,
	) {}

	private get adapter() {
		return this.app.vault.adapter;
	}

	private hidden(path: string): boolean {
		return path.split("/").some((s) => s.startsWith("."));
	}

	async list(): Promise<FileStat[]> {
		const out: FileStat[] = this.app.vault.getFiles().map((f) => ({ path: f.path, size: f.stat.size, mtime: f.stat.mtime }));
		if (this.includeConfig()) {
			// The vault index does not contain dot-folders; walk the config dir.
			const walk = async (dir: string) => {
				const res = await this.adapter.list(dir);
				for (const f of res.files) {
					const st = await this.adapter.stat(f);
					if (st?.type === "file") out.push({ path: f, size: st.size, mtime: st.mtime });
				}
				for (const d of res.folders) await walk(d);
			};
			if (await this.adapter.exists(this.app.vault.configDir)) await walk(this.app.vault.configDir);
		}
		return out;
	}

	async stat(path: string): Promise<FileStat | null> {
		const st = await this.adapter.stat(normalizePath(path));
		return st?.type === "file" ? { path, size: st.size, mtime: st.mtime } : null;
	}

	read(path: string): Promise<ArrayBuffer> {
		return this.adapter.readBinary(normalizePath(path));
	}

	private async ensureFolder(path: string) {
		const dir = path.slice(0, path.lastIndexOf("/"));
		if (!dir || (await this.adapter.exists(dir))) return;
		if (this.hidden(dir)) await this.adapter.mkdir(dir);
		else await this.app.vault.createFolder(dir).catch(() => this.adapter.mkdir(dir));
	}

	async write(path: string, data: ArrayBuffer, mtime: number): Promise<FileStat> {
		path = normalizePath(path);
		await this.ensureFolder(path);
		const opts = { mtime: mtime > 0 ? mtime : Date.now() };
		const file = this.app.vault.getAbstractFileByPath(path);
		// Going through the vault API keeps Obsidian's index and open editors
		// up to date; hidden files are not indexed, so use the adapter.
		if (file instanceof TFile) await this.app.vault.modifyBinary(file, data, opts);
		else if (this.hidden(path) || file) await this.adapter.writeBinary(path, data, opts);
		else await this.app.vault.createBinary(path, data, opts);
		const st = await this.adapter.stat(path);
		return { path, size: st?.size ?? data.byteLength, mtime: st?.mtime ?? opts.mtime };
	}

	async remove(path: string): Promise<void> {
		path = normalizePath(path);
		const file = this.app.vault.getAbstractFileByPath(path);
		if (file instanceof TFile) {
			// Local Obsidian trash (never synced): a safety net for deletions
			// arriving from other devices.
			await this.app.vault.trash(file, false);
		} else if (await this.adapter.exists(path)) {
			await this.adapter.remove(path);
		}
		// Remove folders left empty by the deletion.
		let dir = path.slice(0, path.lastIndexOf("/"));
		while (dir) {
			const folder = this.app.vault.getAbstractFileByPath(dir);
			if (folder instanceof TFolder) {
				if (folder.children.length) break;
				await this.app.vault.delete(folder, true);
			} else if (this.hidden(dir) && (await this.adapter.exists(dir))) {
				const res = await this.adapter.list(dir);
				if (res.files.length || res.folders.length) break;
				await this.adapter.rmdir(dir, false);
			} else break;
			dir = dir.slice(0, Math.max(0, dir.lastIndexOf("/")));
		}
	}
}
