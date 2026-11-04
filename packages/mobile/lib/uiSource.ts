import { readdirSync, readFileSync } from "node:fs";

/** The ui barrel and every ./ui/* module, as one string, for source-text tests. */
export function uiSource(): string {
	const dir = new URL("./ui/", import.meta.url);
	return [new URL("./ui.tsx", import.meta.url), ...readdirSync(dir).map((f) => new URL(f, dir))]
		.map((u) => readFileSync(u, "utf8"))
		.join("\n");
}
