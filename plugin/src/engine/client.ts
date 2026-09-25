import type { CommitOp, Http, HttpResponse, OpResult, RemoteEntry } from "./types";

export class ApiError extends Error {
	constructor(
		public status: number,
		public code: string,
		message: string,
	) {
		super(message);
	}
}

export interface VaultInfo {
	id: number;
	name: string;
	role: "owner" | "editor" | "viewer";
	head_rev: number;
}

export interface ChangesPage {
	changes: RemoteEntry[];
	head: number;
	more: boolean;
	role: string;
}

/**
 * Normalizes what the user typed as server address. Without a scheme, local
 * addresses get http:// and everything else https://.
 */
export function normalizeServerUrl(input: string): string {
	let s = input.trim().replace(/\/+$/, "");
	if (!s) return s;
	if (!/^https?:\/\//i.test(s)) {
		const host = s.split(/[/:]/)[0];
		const local = /^(localhost|127\.|10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(host) || host.endsWith(".local") || host.endsWith(".lan");
		s = (local ? "http://" : "https://") + s;
	}
	return s;
}

export class Client {
	constructor(
		public baseUrl: string,
		public token: string,
		private http: Http,
	) {}

	private async req(method: string, path: string, body?: unknown, raw?: ArrayBuffer): Promise<HttpResponse> {
		const headers: Record<string, string> = {};
		if (this.token) headers["Authorization"] = `Bearer ${this.token}`;
		let res: HttpResponse;
		try {
			res = await this.http({
				url: this.baseUrl + path,
				method,
				headers,
				body: raw ?? (body === undefined ? undefined : JSON.stringify(body)),
				contentType: raw ? "application/octet-stream" : "application/json",
			});
		} catch (e) {
			throw new ApiError(0, "network", `Server je nedostupný (${e instanceof Error ? e.message : String(e)})`);
		}
		if (res.status >= 400) {
			let code = "http_" + res.status;
			let msg = `Chyba serveru (${res.status})`;
			try {
				const j = res.json;
				if (j?.error) code = j.error;
				if (j?.message) msg = j.message;
			} catch {
				/* not JSON */
			}
			throw new ApiError(res.status, code, msg);
		}
		return res;
	}

	/** Verifies that an ObsiSync server answers at baseUrl. */
	async ping(): Promise<void> {
		let ok = false;
		try {
			ok = (await this.req("GET", "/api/v1/ping")).json?.app === "obsisync";
		} catch (e) {
			if (e instanceof ApiError && e.status === 0) throw e;
		}
		if (!ok) throw new ApiError(0, "not_obsisync", "Na této adrese neběží ObsiSync server");
	}

	async login(username: string, password: string, deviceName: string): Promise<string> {
		const res = await this.req("POST", "/api/v1/auth/login", { username, password, device_name: deviceName });
		this.token = res.json.token;
		return this.token;
	}

	async logout(): Promise<void> {
		await this.req("POST", "/api/v1/auth/logout");
	}

	async vaults(): Promise<{ vaults: VaultInfo[]; can_create: boolean }> {
		return (await this.req("GET", "/api/v1/vaults")).json;
	}

	async createVault(name: string): Promise<VaultInfo> {
		return (await this.req("POST", "/api/v1/vaults", { name })).json;
	}

	async changes(vaultId: number, since: number): Promise<ChangesPage> {
		return (await this.req("GET", `/api/v1/vaults/${vaultId}/changes?since=${since}&limit=1000`)).json;
	}

	async missing(vaultId: number, hashes: string[]): Promise<string[]> {
		return (await this.req("POST", `/api/v1/vaults/${vaultId}/blobs/missing`, { hashes })).json.missing;
	}

	async upload(vaultId: number, hash: string, data: ArrayBuffer): Promise<void> {
		await this.req("PUT", `/api/v1/vaults/${vaultId}/blobs/${hash}`, undefined, data);
	}

	/** Downloads content by hash; null if the server no longer has it. */
	async download(vaultId: number, hash: string): Promise<ArrayBuffer | null> {
		try {
			return (await this.req("GET", `/api/v1/vaults/${vaultId}/blobs/${hash}`)).arrayBuffer;
		} catch (e) {
			if (e instanceof ApiError && e.status === 404) return null;
			throw e;
		}
	}

	async commit(vaultId: number, ops: CommitOp[]): Promise<{ results: OpResult[]; head: number }> {
		return (await this.req("POST", `/api/v1/vaults/${vaultId}/commit`, { ops })).json;
	}

	wsUrl(vaultId: number): string {
		return this.baseUrl.replace(/^http/, "ws") + `/api/v1/vaults/${vaultId}/ws?token=${encodeURIComponent(this.token)}`;
	}
}
