// UI translations. English is the default and the fallback for missing keys.
import { cs } from "./cs";
import { de } from "./de";
import { en, type Dict, type Key } from "./en";
import { es } from "./es";
import { fr } from "./fr";
import { it } from "./it";
import { pl } from "./pl";
import { sk } from "./sk";

export type { Key };

export const LANGUAGES: { code: string; name: string; dict: Dict }[] = [
	{ code: "en", name: "English", dict: en },
	{ code: "cs", name: "Čeština", dict: cs },
	{ code: "sk", name: "Slovenčina", dict: sk },
	{ code: "de", name: "Deutsch", dict: de },
	{ code: "fr", name: "Français", dict: fr },
	{ code: "es", name: "Español", dict: es },
	{ code: "it", name: "Italiano", dict: it },
	{ code: "pl", name: "Polski", dict: pl },
];

let chosen = "auto";

/** "auto" follows Obsidian's language, otherwise a code from LANGUAGES. */
export function setLanguage(code: string) {
	chosen = code;
}

function current(): Dict {
	let code = chosen;
	if (code === "auto") {
		try {
			code = globalThis.localStorage?.getItem("language") ?? "en";
		} catch {
			code = "en";
		}
	}
	return LANGUAGES.find((l) => l.code === code)?.dict ?? en;
}

export function t(key: Key | string, vars: Record<string, string | number> = {}): string {
	const text = current()[key as Key] ?? en[key as Key] ?? key;
	return text.replace(/\{(\w+)\}/g, (_, k: string) => String(vars[k] ?? `{${k}}`));
}

export function hasText(key: string): boolean {
	return key in en;
}
