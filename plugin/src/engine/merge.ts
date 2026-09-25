import { diff3Merge } from "node-diff3";

const TEXT_EXT = new Set(["md", "txt", "canvas", "json", "css", "csv", "base", "yaml", "yml", "html", "svg", "js", "tex"]);
const JSON_EXT = new Set(["canvas", "json"]);

function ext(path: string): string {
	const name = path.slice(path.lastIndexOf("/") + 1);
	const i = name.lastIndexOf(".");
	return i < 0 ? "" : name.slice(i + 1).toLowerCase();
}

export function isMergeable(path: string): boolean {
	return TEXT_EXT.has(ext(path));
}

/** Splits text into lines, keeping line terminators so joining is lossless. */
function lines(s: string): string[] {
	return s.match(/[^\n]*\n|[^\n]+$/g) ?? [];
}

/**
 * Three-way merge of text files. Returns the merged text, or null when both
 * sides changed the same lines (or the result would be invalid JSON).
 */
export function mergeText(path: string, local: string, base: string, remote: string): string | null {
	if (local === remote) return local;
	if (local === base) return remote;
	if (remote === base) return local;
	const regions = diff3Merge(lines(local), lines(base), lines(remote), { excludeFalseConflicts: true });
	let out = "";
	for (const r of regions) {
		if (r.conflict) return null;
		out += (r.ok ?? []).join("");
	}
	if (JSON_EXT.has(ext(path))) {
		try {
			JSON.parse(out);
		} catch {
			return null;
		}
	}
	return out;
}
