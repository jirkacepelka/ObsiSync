import { describe, expect, it } from "vitest";
import { insecureRemote, normalizeServerUrl } from "../src/engine/client";
import { safePath } from "../src/engine/engine";

describe("safePath", () => {
	it("accepts normal vault paths", () => {
		for (const p of ["Note.md", "Folder/Sub/Obrázek.png", "a b/c.d.md", ".obsidian/app.json"]) expect(safePath(p)).toBe(true);
	});
	it("rejects paths that could leave the vault", () => {
		for (const p of ["", "/etc/passwd", "../x.md", "a/../../x", "a/./b", "a//b", "a\\..\\b", "x\0y"]) expect(safePath(p)).toBe(false);
	});
});

describe("insecureRemote", () => {
	it("allows https and http on the home network or Tailscale", () => {
		for (const u of ["https://sync.example.com", "http://192.168.0.98:8080", "http://10.0.0.2", "http://localhost:8080", "http://nas.local", "http://100.101.2.3:8080", "http://box.tail1234.ts.net"])
			expect(insecureRemote(u)).toBe(false);
	});
	it("flags plain http over the internet", () => {
		for (const u of ["http://sync.example.com", "http://203.0.113.9:8080", "http://100.200.1.1"]) expect(insecureRemote(u)).toBe(true);
	});
	it("defaults to https for internet hosts", () => {
		expect(normalizeServerUrl("sync.example.com")).toBe("https://sync.example.com");
		expect(normalizeServerUrl("192.168.0.98:8080")).toBe("http://192.168.0.98:8080");
	});
});
