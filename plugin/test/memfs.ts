import type { FileStat, LocalFS } from "../src/engine/types";

let clock = 1_700_000_000_000;

/** In-memory vault used to simulate devices in tests. */
export class MemFS implements LocalFS {
	files = new Map<string, { data: Uint8Array; mtime: number }>();

	set(path: string, text: string) {
		this.files.set(path, { data: new TextEncoder().encode(text), mtime: clock++ });
	}
	get(path: string): string | undefined {
		const f = this.files.get(path);
		return f && new TextDecoder().decode(f.data);
	}
	del(path: string) {
		this.files.delete(path);
	}
	snapshot(): Record<string, string> {
		const out: Record<string, string> = {};
		for (const k of [...this.files.keys()].sort()) out[k] = this.get(k)!;
		return out;
	}

	async list(): Promise<FileStat[]> {
		return [...this.files].map(([path, f]) => ({ path, size: f.data.byteLength, mtime: f.mtime }));
	}
	async stat(path: string): Promise<FileStat | null> {
		const f = this.files.get(path);
		return f ? { path, size: f.data.byteLength, mtime: f.mtime } : null;
	}
	async read(path: string): Promise<ArrayBuffer> {
		const f = this.files.get(path);
		if (!f) throw new Error("ENOENT " + path);
		return f.data.slice().buffer;
	}
	async write(path: string, data: ArrayBuffer, mtime: number): Promise<FileStat> {
		this.files.set(path, { data: new Uint8Array(data.slice(0)), mtime });
		return { path, size: data.byteLength, mtime };
	}
	async remove(path: string): Promise<void> {
		this.files.delete(path);
	}
	async trash(path: string): Promise<void> {
		const f = this.files.get(path);
		if (!f) return;
		this.files.delete(path);
		this.files.set(".trash/" + path, f);
	}
	/** Files outside the trash. */
	notes(): Record<string, string> {
		return Object.fromEntries(Object.entries(this.snapshot()).filter(([k]) => !k.startsWith(".trash/")));
	}
}
