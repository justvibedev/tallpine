import { mkdtemp, readFile, rm, stat } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { BrowserHistoryStore, type BrowserHistoryEntry } from "./browser-history-store";

const profileId = "11111111-1111-4111-8111-111111111111";
const temporaryDirectories: string[] = [];

afterEach(async () => {
	await Promise.all(temporaryDirectories.splice(0).map((directory) => rm(directory, { recursive: true, force: true })));
});

async function temporaryState(): Promise<string> {
	const directory = await mkdtemp(path.join(os.tmpdir(), "tallpine-browser-history-"));
	temporaryDirectories.push(directory);
	return directory;
}

describe("BrowserHistoryStore", () => {
	it("merges, persists, reloads, suggests, and clears profile-scoped history", async () => {
		const stateDir = await temporaryState();
		const store = new BrowserHistoryStore({ stateDir });
		await expect(store.mergeImportedEntries(profileId, [
			{ url: "https://github.com/openai", title: "OpenAI", lastVisited: "2026-01-01T00:00:00.000Z", visitCount: 2 },
			{ url: "file:///secret", title: "Secret", lastVisited: "2026-01-02T00:00:00.000Z", visitCount: 1 },
		])).resolves.toEqual({ imported: 1, truncated: 0 });

		expect(await store.suggest(profileId, "openai")).toEqual([
			{ url: "https://github.com/openai", title: "OpenAI" },
		]);
		const reloaded = new BrowserHistoryStore({ stateDir });
		expect(await reloaded.suggest(profileId, "github")).toHaveLength(1);
		await reloaded.clear(profileId);
		expect(await new BrowserHistoryStore({ stateDir }).suggest(profileId, "github")).toEqual([]);
	});

	it("caps entry count and serialized output without retaining an oversized cache", async () => {
		const stateDir = await temporaryState();
		const store = new BrowserHistoryStore({ stateDir });
		const entries: BrowserHistoryEntry[] = Array.from({ length: 5_100 }, (_, index) => ({
			url: `https://example.com/${index}/${"x".repeat(900)}`,
			title: `Entry ${index}`,
			lastVisited: new Date(1_800_000_000_000 - index).toISOString(),
			visitCount: 1,
		}));
		const outcome = await store.mergeImportedEntries(profileId, entries);
		const file = path.join(stateDir, "browser-history", `${profileId}.json`);
		const metadata = await stat(file);
		const parsed = JSON.parse(await readFile(file, "utf8")) as { entries: BrowserHistoryEntry[] };
		expect(metadata.size).toBeLessThanOrEqual(4 * 1024 * 1024);
		expect(parsed.entries.length).toBe(outcome.imported);
		expect(parsed.entries.length).toBeLessThanOrEqual(5_000);
		expect(outcome.truncated).toBe(entries.length - outcome.imported);
		expect(await store.suggest(profileId, "entry 5099")).toEqual([]);
	});

	it("drains queued fire-and-forget records before shutdown completes", async () => {
		const stateDir = await temporaryState();
		const store = new BrowserHistoryStore({ stateDir });

		void store.record(profileId, "https://first.example/", "First", true);
		void store.record(profileId, "https://second.example/", "Second", true);
		await store.drain();

		const reloaded = new BrowserHistoryStore({ stateDir });
		expect(await reloaded.suggest(profileId, "example")).toHaveLength(2);
	});

	it("coalesces the events of a page load into one trailing compact write", async () => {
		const stateDir = await temporaryState();
		const file = path.join(stateDir, "browser-history", `${profileId}.json`);
		let tick = Date.parse("2026-03-01T00:00:00.000Z");
		const store = new BrowserHistoryStore({ stateDir, writeDelayMs: 60_000, now: () => new Date((tick += 1_000)) });

		await store.record(profileId, "https://first.example/", "", true);
		await store.record(profileId, "https://first.example/", "First", false);
		await store.record(profileId, "https://second.example/", "Second", true);
		await store.record(profileId, "https://first.example/", "First again", true);
		await expect(stat(file)).rejects.toMatchObject({ code: "ENOENT" });
		expect(await store.suggest(profileId, "first")).toEqual([{ url: "https://first.example/", title: "First again" }]);

		await store.drain();
		const raw = await readFile(file, "utf8");
		expect(raw).not.toContain("\n  ");
		const parsed = JSON.parse(raw) as { version: number; entries: BrowserHistoryEntry[] };
		expect(parsed.version).toBe(1);
		expect(parsed.entries.map(({ url, visitCount }) => ({ url, visitCount }))).toEqual([
			{ url: "https://first.example/", visitCount: 2 },
			{ url: "https://second.example/", visitCount: 1 },
		]);
	});

	it("keeps the newest entries that fit when a record pushes the file over the size cap", async () => {
		const stateDir = await temporaryState();
		const store = new BrowserHistoryStore({ stateDir, writeDelayMs: 60_000 });
		const entries: BrowserHistoryEntry[] = Array.from({ length: 5_000 }, (_, index) => ({
			url: `https://example.com/${index}/${"x".repeat(800)}`,
			lastVisited: new Date(1_700_000_000_000 - index * 1_000).toISOString(),
			visitCount: 1,
		}));
		const outcome = await store.mergeImportedEntries(profileId, entries);
		expect(outcome.imported).toBeLessThan(5_000);
		await store.record(profileId, "https://newest.example/", "Newest", true);
		await store.drain();

		const file = path.join(stateDir, "browser-history", `${profileId}.json`);
		expect((await stat(file)).size).toBeLessThanOrEqual(4 * 1024 * 1024);
		const parsed = JSON.parse(await readFile(file, "utf8")) as { entries: BrowserHistoryEntry[] };
		expect(parsed.entries[0]?.url).toBe("https://newest.example/");
		expect(parsed.entries.slice(1).map((entry) => entry.url)).toEqual(
			entries.slice(0, parsed.entries.length - 1).map((entry) => entry.url),
		);
		const reloaded = new BrowserHistoryStore({ stateDir });
		expect(await reloaded.suggest(profileId, "newest")).toEqual([{ url: "https://newest.example/", title: "Newest" }]);
	});

	it("drops a pending write when history is cleared", async () => {
		const stateDir = await temporaryState();
		const store = new BrowserHistoryStore({ stateDir, writeDelayMs: 10 });
		await store.record(profileId, "https://cleared.example/", "Cleared", true);
		await store.clear(profileId);
		await new Promise((resolve) => setTimeout(resolve, 30));
		await store.drain();
		await expect(stat(path.join(stateDir, "browser-history", `${profileId}.json`))).rejects.toMatchObject({ code: "ENOENT" });
		expect(await new BrowserHistoryStore({ stateDir }).suggest(profileId, "cleared")).toEqual([]);
	});
});
