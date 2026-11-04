import { readdirSync, readFileSync } from "node:fs";

const read = (url: URL) => readFileSync(url, "utf8");
const dirSource = (dir: string) => {
	const base = new URL(`./${dir}/`, import.meta.url);
	return readdirSync(base).map((f) => read(new URL(f, base)));
};

/** app/<screen>.tsx plus the hooks/parts/styles split into lib/<dir>/, as one string, for source-text tests. */
function screenSource(screen: string, dir: string): string {
	return [read(new URL(`../app/${screen}.tsx`, import.meta.url)), ...dirSource(dir)].join("\n");
}

export const spawnSource = () => screenSource("spawn", "spawn");
export const pairSource = () => screenSource("pair", "pair");
export const notificationsSource = () => screenSource("notifications", "notifications");
