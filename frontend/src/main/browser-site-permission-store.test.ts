import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { BrowserSitePermissionStore } from "./browser-site-permission-store";

const PROFILE = "11111111-1111-4111-8111-111111111111";
const OTHER = "22222222-2222-4222-8222-222222222222";
const ORIGIN = "https://meet.example";
const FILE = "browser-site-permissions.json";
const temporaryDirectories: string[] = [];

afterEach(async () => {
	await Promise.all(temporaryDirectories.splice(0).map((directory) => rm(directory, { recursive: true, force: true })));
});

async function stateDir(): Promise<string> {
	const directory = await mkdtemp(path.join(os.tmpdir(), "tallpine-site-permissions-"));
	temporaryDirectories.push(directory);
	return directory;
}

describe("BrowserSitePermissionStore", () => {
	it("remembers answers per profile and origin across reopening", async () => {
		const dir = await stateDir();
		const store = await BrowserSitePermissionStore.open(dir);
		await store.set(PROFILE, ORIGIN, ["camera", "microphone"], "allow");
		await store.set(PROFILE, ORIGIN, ["notifications"], "block");
		await store.set(OTHER, ORIGIN, ["camera"], "block");

		const reopened = await BrowserSitePermissionStore.open(dir);
		expect(reopened.list(PROFILE, ORIGIN)).toEqual([
			{ kind: "camera", decision: "allow" },
			{ kind: "microphone", decision: "allow" },
			{ kind: "notifications", decision: "block" },
		]);
		expect(reopened.get(OTHER, ORIGIN, "camera")).toBe("block");
		expect(reopened.get(PROFILE, "https://other.example", "camera")).toBeUndefined();
	});

	it("keeps a temporary partition's answers in memory only", async () => {
		const dir = await stateDir();
		const store = await BrowserSitePermissionStore.open(dir);
		await store.set("persist:tallpine-temp-1", ORIGIN, ["camera"], "allow");
		expect(store.get("persist:tallpine-temp-1", ORIGIN, "camera")).toBe("allow");
		await expect(readFile(path.join(dir, FILE), "utf8")).rejects.toMatchObject({ code: "ENOENT" });

		await store.clear("persist:tallpine-temp-1");
		expect(store.get("persist:tallpine-temp-1", ORIGIN, "camera")).toBeUndefined();
	});

	it("resets one kind, all kinds, or a whole deleted profile", async () => {
		const dir = await stateDir();
		const store = await BrowserSitePermissionStore.open(dir);
		await store.set(PROFILE, ORIGIN, ["camera", "microphone"], "allow");
		await store.reset(PROFILE, ORIGIN, "camera");
		expect(store.list(PROFILE, ORIGIN)).toEqual([{ kind: "microphone", decision: "allow" }]);
		await store.reset(PROFILE, ORIGIN);
		expect(store.list(PROFILE, ORIGIN)).toEqual([]);

		await store.set(PROFILE, ORIGIN, ["camera"], "block");
		await store.clear(PROFILE);
		expect((await BrowserSitePermissionStore.open(dir)).list(PROFILE, ORIGIN)).toEqual([]);
	});

	it("ignores malformed entries and never stores a non-web origin", async () => {
		const dir = await stateDir();
		await writeFile(
			path.join(dir, FILE),
			JSON.stringify({
				version: 1,
				profiles: {
					[PROFILE]: {
						[ORIGIN]: { camera: "allow", geolocation: "allow", microphone: "maybe" },
						"file://": { camera: "allow" },
						"https://meet.example/path": { camera: "allow" },
					},
					"not-a-profile": { [ORIGIN]: { camera: "allow" } },
				},
			}),
		);
		const store = await BrowserSitePermissionStore.open(dir);
		expect(store.list(PROFILE, ORIGIN)).toEqual([{ kind: "camera", decision: "allow" }]);
		expect(store.get("not-a-profile", ORIGIN, "camera")).toBeUndefined();
		expect(store.get(PROFILE, "file://", "camera")).toBeUndefined();
		expect(store.get(PROFILE, "https://meet.example/path", "camera")).toBeUndefined();

		await store.set(PROFILE, "mailto:", ["camera"], "allow");
		expect(store.list(PROFILE, "mailto:")).toEqual([]);
	});

	it("starts empty when the file is not JSON", async () => {
		const dir = await stateDir();
		await writeFile(path.join(dir, FILE), "{not json");
		const store = await BrowserSitePermissionStore.open(dir);
		expect(store.list(PROFILE, ORIGIN)).toEqual([]);
		await store.set(PROFILE, ORIGIN, ["camera"], "allow");
		expect(JSON.parse(await readFile(path.join(dir, FILE), "utf8")).profiles[PROFILE][ORIGIN]).toEqual({ camera: "allow" });
	});
});
