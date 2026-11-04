import { mkdir, mkdtemp, readdir, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { rewriteCookiesForEncryption, type CookieStore, type MigratableCookie } from "./cookie-encryption-migration";

const dirs: string[] = [];
afterEach(async () => {
	for (const dir of dirs.splice(0)) await rm(dir, { recursive: true, force: true });
});

async function userData(partitions: string[] = []): Promise<string> {
	const dir = await mkdtemp(path.join(os.tmpdir(), "vo-cookies-"));
	dirs.push(dir);
	for (const name of partitions) await mkdir(path.join(dir, "Partitions", name), { recursive: true });
	return dir;
}

function store(cookies: MigratableCookie[], fail = false): CookieStore & { set: ReturnType<typeof vi.fn> } {
	return {
		get: vi.fn(async () => cookies),
		set: vi.fn(async () => { if (fail) throw new Error("boom"); }),
		flushStore: vi.fn(async () => undefined),
	};
}

const session = { name: "sid", value: "secret", domain: ".example.com", path: "/", secure: true, httpOnly: true, expirationDate: 99, sameSite: "lax" as const };

describe("rewriteCookiesForEncryption", () => {
	it("sets every persistent cookie back once, keeping it, and skips temporary partitions", async () => {
		const dir = await userData(["vo-browser-main", "vo-browser-temporary-1"]);
		const main = store([]);
		const partition = store([session]);
		const temporary = store([session]);
		const stores: Record<string, CookieStore> = { "vo-browser-main": partition, "vo-browser-temporary-1": temporary };

		expect(await rewriteCookiesForEncryption({ userDataDir: dir, defaultCookies: main, partitionCookies: (name) => stores[name]! })).toBe(true);
		expect(partition.set).toHaveBeenCalledWith({ ...session, url: "https://example.com/" });
		expect(partition.flushStore).toHaveBeenCalled();
		expect(temporary.set).not.toHaveBeenCalled();

		const again = store([session]);
		expect(await rewriteCookiesForEncryption({ userDataDir: dir, defaultCookies: again, partitionCookies: () => again })).toBe(false);
		expect(again.get).not.toHaveBeenCalled();
	});

	it("sets a host-only cookie back without a domain, so it stays host-only", async () => {
		const dir = await userData();
		const hostOnly = { name: "__Host-sid", value: "v", domain: "app.example.com", path: "/", secure: true, hostOnly: true, session: true };
		const main = store([hostOnly]);
		await rewriteCookiesForEncryption({ userDataDir: dir, defaultCookies: main, partitionCookies: () => main });
		expect(main.set).toHaveBeenCalledWith({ name: "__Host-sid", value: "v", path: "/", secure: true, url: "https://app.example.com/" });
	});

	it("leaves no marker when a cookie could not be rewritten, so the next start retries", async () => {
		const dir = await userData();
		expect(await rewriteCookiesForEncryption({ userDataDir: dir, defaultCookies: store([session], true), partitionCookies: () => store([]) })).toBe(false);
		expect(await readdir(dir)).toEqual([]);
	});
});
