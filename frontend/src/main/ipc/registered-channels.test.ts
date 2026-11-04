import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

/**
 * The set of ipcMain channels the main process registers must not change when
 * main.ts is split. The snapshot lives in registered-channels.json; regenerate
 * it deliberately (UPDATE_IPC_CHANNELS=1) when a channel is added or removed.
 */
const srcDir = path.resolve(process.cwd(), "src");
const read = (rel: string) => fs.readFileSync(path.join(srcDir, rel), "utf8");

function sourceFiles(dir: string): string[] {
	return fs.readdirSync(path.join(srcDir, dir), { withFileTypes: true }).flatMap((entry) => {
		const rel = path.join(dir, entry.name);
		if (entry.isDirectory()) return sourceFiles(rel);
		return /\.ts$/.test(entry.name) && !/\.(test|integration)\.ts$/.test(entry.name) ? [rel] : [];
	});
}

const files = ["main.ts", ...sourceFiles("main"), ...sourceFiles("shared")];
const constants = new Map<string, string>();
for (const file of files) {
	for (const m of read(file).matchAll(/(?:export )?const ([A-Z][A-Z0-9_]*)\s*=\s*["']([^"']+)["']/g)) constants.set(m[1]!, m[2]!);
}

export function registeredChannels(): string[] {
	const out = new Set<string>();
	for (const file of ["main.ts", ...sourceFiles("main")]) {
		if (file.endsWith(path.join("main", "ipc-guard.ts"))) continue;
		for (const m of read(file).matchAll(/(?:ipcMain\s*\.\s*|(?<![.\w]))(handle|on|once)\(\s*([A-Za-z_"'][^,)\s]*)/g)) {
			const arg = m[2]!;
			const literal = /^["']([^"']+)["']$/.exec(arg);
			const channel = literal ? literal[1]! : constants.get(arg);
			if (!channel) continue; // variable argument: not a channel (event emitters, helpers)
			out.add(`${m[1] === "once" ? "on" : m[1]} ${channel}`);
		}
	}
	return [...out].sort();
}

const snapshotPath = path.join(srcDir, "main", "ipc", "registered-channels.json");

describe("registered ipcMain channels", () => {
	it("match the committed snapshot", () => {
		const actual = registeredChannels();
		if (process.env.UPDATE_IPC_CHANNELS) fs.writeFileSync(snapshotPath, `${JSON.stringify(actual, null, "\t")}\n`);
		expect(actual).toEqual(JSON.parse(fs.readFileSync(snapshotPath, "utf8")));
		expect(actual.length).toBeGreaterThan(50);
	});
});
