import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import type { Http } from "../src/engine/types";

const serverDir = fileURLToPath(new URL("../../server", import.meta.url));

function freePort(): Promise<number> {
	return new Promise((res) => {
		const s = createServer();
		s.listen(0, () => {
			const port = (s.address() as { port: number }).port;
			s.close(() => res(port));
		});
	});
}

/** Builds and starts a real SimpleSync server with an admin "admin"/"heslo1234". */
export async function startServer(): Promise<{ url: string; data: string; bin: string; stop: () => void }> {
	const bin = join(tmpdir(), "obsisync-test-bin");
	execFileSync("go", ["build", "-o", bin, "./cmd/obsisync"], { cwd: serverDir, stdio: "inherit" });
	const data = mkdtempSync(join(tmpdir(), "obsisync-data-"));
	const env = { ...process.env, OBSISYNC_DATA: data };
	execFileSync(bin, ["reset-password", "admin", "heslo1234"], { env });
	const port = await freePort();
	const proc: ChildProcess = spawn(bin, ["serve"], { env: { ...env, OBSISYNC_ADDR: `127.0.0.1:${port}` }, stdio: "ignore" });
	const url = `http://127.0.0.1:${port}`;
	for (let i = 0; i < 100; i++) {
		try {
			if ((await fetch(url + "/api/v1/ping")).ok) break;
		} catch {
			/* not up yet */
		}
		await new Promise((r) => setTimeout(r, 50));
	}
	return { url, data, bin, stop: () => proc.kill() };
}

/** Http implementation on top of fetch (the plugin uses Obsidian's requestUrl). */
export const fetchHttp: Http = async (req) => {
	const headers: Record<string, string> = { ...req.headers };
	if (req.body !== undefined && req.contentType) headers["Content-Type"] = req.contentType;
	const res = await fetch(req.url, { method: req.method, headers, body: req.body as BodyInit | undefined });
	const buf = await res.arrayBuffer();
	let json: unknown = undefined;
	try {
		json = JSON.parse(new TextDecoder().decode(buf));
	} catch {
		/* binary */
	}
	return { status: res.status, json, arrayBuffer: buf };
};
