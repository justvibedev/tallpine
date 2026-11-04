import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { isTrustedSender, type IpcMethod } from "./ipc-guard";

/**
 * Static coverage for the IPC guard. Parses the preload sources, so a new
 * channel in a preload, or a raw ipcMain registration, fails here instead of
 * breaking (or bypassing) the guard at runtime.
 *
 * Preloads that exist (`preload:` in webPreferences): the shell preload
 * (preload.ts, shell view only) and the annotation overlay preload
 * (annotate-preload.ts, browser-view pages). There is no render-check or other
 * webContents with a preload.
 */

const srcDir = path.resolve(process.cwd(), "src"); // vitest runs from frontend/
const read = (rel: string) => fs.readFileSync(path.join(srcDir, rel), "utf8");

function sourceFiles(dir: string): string[] {
	return fs.readdirSync(path.join(srcDir, dir), { withFileTypes: true }).flatMap((entry) => {
		const rel = path.join(dir, entry.name);
		if (entry.isDirectory()) return sourceFiles(rel);
		return /\.ts$/.test(entry.name) && !/\.(test|integration)\.ts$/.test(entry.name) ? [rel] : [];
	});
}

// `export const NAME = "value"` (value may be on the next line) across shared/ and main/.
const constants = new Map<string, string>();
for (const file of [...sourceFiles("shared"), ...sourceFiles("main")]) {
	for (const m of read(file).matchAll(/export const ([A-Z][A-Z0-9_]*)\s*=\s*["']([^"']+)["']/g)) constants.set(m[1]!, m[2]!);
}

function resolveArg(arg: string): string {
	const literal = /^["']([^"']+)["']$/.exec(arg);
	if (literal) return literal[1]!;
	const value = constants.get(arg);
	if (!value) throw new Error(`cannot resolve IPC channel ${arg}`);
	return value;
}

/** Calls a preload makes: invoke -> main `handle`, send/sendSync -> main `on`. */
function preloadCalls(rel: string): Array<{ method: IpcMethod; channel: string }> {
	const calls = [...read(rel).matchAll(/ipcRenderer\.(invoke|send|sendSync)\(\s*([A-Za-z_"'][^,)\s]*)/g)];
	return calls.map((m) => ({ method: m[1] === "invoke" ? "handle" : "on", channel: resolveArg(m[2]!) }));
}

const shellCalls = preloadCalls("preload.ts");
const pageCalls = preloadCalls("annotate-preload.ts");

const shell = { id: 1 } as never;
const page = { id: 2 } as never;
const options = { getShellWebContents: () => shell, allowedOrigins: ["app://renderer"] };
const fromShell = { sender: shell, senderFrame: { url: "app://renderer/" } } as never;
const fromPage = { sender: page, senderFrame: { url: "https://page.example/" } } as never;

describe("ipc guard coverage over the preloads", () => {
	it("parses a non-trivial set of channels from both preloads", () => {
		expect(shellCalls.length).toBeGreaterThan(50);
		expect(pageCalls.map((c) => c.channel).sort()).toEqual([
			"browser:annotation:cancel",
			"browser:annotation:capture",
			"browser:annotation:discard",
			"browser:annotation:state",
			"browser:annotation:submit",
		]);
	});

	it("allows every shell-preload channel from the shell on the app origin", () => {
		for (const { method, channel } of shellCalls) {
			expect(isTrustedSender(fromShell, method, channel, options), `${method} ${channel}`).toBe(true);
		}
	});

	it("allows every annotate-preload channel from a browser-view page", () => {
		for (const { method, channel } of pageCalls) {
			expect(isTrustedSender(fromPage, method, channel, options), `${method} ${channel}`).toBe(true);
		}
	});

	it("rejects every shell-only channel from a browser-view page", () => {
		const pageKeys = new Set(pageCalls.map((c) => `${c.method} ${c.channel}`));
		const shellOnly = shellCalls.filter((c) => !pageKeys.has(`${c.method} ${c.channel}`));
		expect(shellOnly.length).toBeGreaterThan(50);
		for (const { method, channel } of shellOnly) {
			expect(isTrustedSender(fromPage, method, channel, options), `${method} ${channel}`).toBe(false);
		}
	});

	it("rejects shell channels from the shell webContents on a foreign frame origin", () => {
		const foreign = { sender: shell, senderFrame: { url: "https://evil.example/" } } as never;
		for (const { method, channel } of shellCalls) {
			expect(isTrustedSender(foreign, method, channel, options), `${method} ${channel}`).toBe(false);
		}
	});

	it("has a main-side registration for every channel the preloads call", () => {
		const registered = new Set<string>();
		for (const file of ["main.ts", ...sourceFiles("main")]) {
			const text = read(file);
			for (const m of text.matchAll(/(?:ipcMain\.|(?<![.\w]))(handle|on|once)\(\s*([A-Za-z_"'][^,)\s]*)/g)) {
				try {
					registered.add(resolveArg(m[2]!));
				} catch {
					// non-channel `.on(...)` callers (event emitters, helpers taking a variable)
				}
			}
		}
		const missing = [...shellCalls, ...pageCalls].map((c) => c.channel).filter((c) => !registered.has(c));
		// Channels registered through a variable (loops over a table) are not
		// visible to this scan; none exist today, so any miss is a real gap.
		expect(missing).toEqual([]);
	});
});

describe("no raw ipcMain registrations bypass the guard", () => {
	// Documented exemptions: none.
	const EXEMPT_FILES = new Set<string>();

	// The guarded ipcMain is built once, in the shared runtime-state module.
	const GUARD_HOLDER = path.join("main", "app-runtime-state.ts");

	it("only app-runtime-state.ts holds the raw electron ipcMain, and only to build the guard", () => {
		const raw = /import\s*\{[^}]*\bipcMain\b[^}]*\}\s*from\s*["']electron["']/;
		const offenders = sourceFiles("main")
			.filter((file) => !EXEMPT_FILES.has(file) && file !== path.join("main", "ipc-guard.ts") && file !== GUARD_HOLDER)
			.filter((file) => raw.test(read(file))); // `import type { IpcMain }` does not match
		expect(offenders).toEqual([]);

		const main = [read(GUARD_HOLDER), ...sourceFiles("main").filter((f) => f !== GUARD_HOLDER).map(read), read("main.ts")];
		const holder = read(GUARD_HOLDER);
		const uses = [...holder.matchAll(/\belectronIpcMain\b/g)].length;
		for (const other of main.slice(1)) expect(other).not.toMatch(/\belectronIpcMain\b/);
		expect(uses).toBe(2); // the renamed import and the guardIpcMain(electronIpcMain, ...) call
		expect(holder).toMatch(/const ipcMain = guardIpcMain\(electronIpcMain,/);
		// Nothing may call handle/on on `electron`'s module object directly either.
		for (const text of main) expect(text).not.toMatch(/\belectron\.ipcMain\b/);
	});

	it("passes the guarded ipcMain (never electron's) to every registering module", () => {
		const main = ["main.ts", ...sourceFiles("main")].map(read).join("\n");
		for (const call of ["registerCliLinkIpc", "registerChatImageIpc", "registerBrowserProfileIpc", "registerRemotesIpc", "initAppUpdates"]) {
			// Call sites only: skip the definition and imports.
			const sites = [...main.matchAll(new RegExp(`(?<!function )\\b${call}\\(`, "g"))].map((m) => m.index ?? 0);
			expect(sites.length, call).toBeGreaterThan(0);
			for (const at of sites) expect(main.slice(at, at + 200), call).toContain("ipcMain");
		}
	});
});
