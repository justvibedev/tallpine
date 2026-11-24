import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import Database from "better-sqlite3";
import { afterEach, describe, expect, it, vi } from "vitest";
import { BrowserHistoryStore } from "../browser-history-store";
import { BrowserProfileStore } from "../browser-profile-store";
import { mergeWarnings } from "./cookie-rows";
import { BrowserProfileImportService } from "./service";

const roots: string[] = [];
afterEach(async () => {
	vi.restoreAllMocks();
	await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

function chromiumMicros(iso: string): number {
	return Math.round((Date.parse(iso) / 1_000 + 11_644_473_600) * 1_000_000);
}

type SessionStore = "bound" | "missing" | "no-table" | "corrupt";

async function importChrome(store: SessionStore) {
	const root = await mkdtemp(path.join(os.tmpdir(), "tallpine-dbsc-"));
	roots.push(root);
	const localAppData = path.join(root, "local");
	const userData = path.join(localAppData, "Google", "Chrome", "User Data");
	const profileRoot = path.join(userData, "Default");
	await mkdir(path.join(profileRoot, "Network"), { recursive: true });
	await writeFile(path.join(userData, "Local State"), JSON.stringify({ profile: { info_cache: { Default: { name: "Personal" } } } }));
	const cookies = new Database(path.join(profileRoot, "Network", "Cookies"));
	cookies.exec(`
		CREATE TABLE cookies (
			host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB, path TEXT,
			expires_utc INTEGER, is_secure INTEGER, is_httponly INTEGER, samesite INTEGER,
			top_frame_site_key TEXT, last_access_utc INTEGER, creation_utc INTEGER
		)
	`);
	const future = chromiumMicros("2030-01-01T00:00:00.000Z");
	const insert = cookies.prepare("INSERT INTO cookies VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)");
	insert.run(".google.com", "__Secure-1PSIDTS", "bound", Buffer.alloc(0), "/", future, 1, 1, -1, "", future, future);
	insert.run("accounts.google.com", "__Host-GAPSTS", "bound", Buffer.alloc(0), "/", future, 1, 1, -1, "", future, future);
	insert.run(".github.com", "session", "plain", Buffer.alloc(0), "/", future, 1, 1, 1, "", future, future);
	cookies.close();
	const sessionsFile = path.join(profileRoot, "Device Bound Sessions");
	if (store === "bound" || store === "no-table") {
		const sessions = new Database(sessionsFile);
		if (store === "bound") {
			sessions.exec("CREATE TABLE dbsc_session_tbl (key TEXT, proto BLOB, PRIMARY KEY(key))");
			const add = sessions.prepare("INSERT INTO dbsc_session_tbl VALUES (?, ?)");
			add.run("https://google.com", Buffer.from("private-key-material"));
			// Bound in Chrome, but no cookie for it is imported: not reported.
			add.run("https://claude.ai", Buffer.from("private-key-material"));
			add.run("not a url", Buffer.alloc(0));
		} else {
			sessions.exec("CREATE TABLE meta (key TEXT, value TEXT)");
		}
		sessions.close();
	} else if (store === "corrupt") {
		await writeFile(sessionsFile, "not a sqlite database");
	}
	const sessionsBefore = store === "missing" ? null : await readFile(sessionsFile);

	const stateDir = path.join(root, "tallpine-state");
	const profileStore = new BrowserProfileStore({ stateDir });
	await profileStore.load();
	const set = vi.fn(async () => undefined);
	const service = new BrowserProfileImportService({
		stateDir,
		profileStore,
		historyStore: new BrowserHistoryStore({ stateDir }),
		platform: "win32",
		homeDir: root,
		env: { LOCALAPPDATA: localAppData },
		fromPartition: () => ({ cookies: { set }, clearStorageData: async () => undefined, clearCache: async () => undefined }),
	});
	const source = (await service.discover()).sources[0]!;
	const result = await service.import({
		requestId: "1d1d1d1d-1d1d-4d1d-8d1d-1d1d1d1d1d1d",
		sourceId: source.id,
		profileIds: [source.profiles[0]!.id],
		includeCookies: true,
		includeHistory: false,
		destination: { mode: "merge", name: "Chrome" },
	}, vi.fn());
	if (sessionsBefore) expect(await readFile(sessionsFile)).toEqual(sessionsBefore);
	return { entry: result.entries[0]!, set };
}

describe("Chromium device-bound session warning", () => {
	it("names the bound sites whose cookies were imported, without reading session key material", async () => {
		const { entry, set } = await importChrome("bound");

		expect(set).toHaveBeenCalledTimes(3);
		expect(entry.importedCookies).toBe(3);
		expect(entry.warnings).toContainEqual({ code: "device-bound-sessions", count: 1, sites: ["google.com"] });
		expect(JSON.stringify(entry.warnings)).not.toContain("private-key-material");
	});

	it.each(["missing", "no-table", "corrupt"] as const)("imports cookies without the warning when the session store is %s", async (store) => {
		const { entry } = await importChrome(store);

		expect(entry.importedCookies).toBe(3);
		expect(entry.warnings.some((warning) => warning.code === "device-bound-sessions")).toBe(false);
	});
});

describe("mergeWarnings", () => {
	it("unions site lists across profiles and counts distinct sites", () => {
		expect(mergeWarnings([
			{ code: "expired-cookies-skipped", count: 2 },
			{ code: "device-bound-sessions", count: 2, sites: ["google.com", "youtube.com"] },
			{ code: "expired-cookies-skipped", count: 3 },
			{ code: "device-bound-sessions", count: 1, sites: ["google.com"] },
		])).toEqual([
			{ code: "expired-cookies-skipped", count: 5 },
			{ code: "device-bound-sessions", count: 2, sites: ["google.com", "youtube.com"] },
		]);
	});
});
