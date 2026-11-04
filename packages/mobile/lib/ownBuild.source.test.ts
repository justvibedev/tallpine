// Guards the "own build" contract: no runtime tie to the upstream Expo project,
// OTA, store nudge, Discord, or App Store listing; tokens only in SecureStore.
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

const root = resolve(__dirname, "..");
const read = (p: string) => readFileSync(join(root, p), "utf8");
const expo = JSON.parse(read("app.json")).expo;

function sources(dir: string, out: string[] = []): string[] {
	for (const name of readdirSync(join(root, dir))) {
		if (["node_modules", "ios", "android", "e2e"].includes(name)) continue;
		const rel = join(dir, name);
		if (statSync(join(root, rel)).isDirectory()) sources(rel, out);
		else if (/\.(ts|tsx)$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(rel);
	}
	return out;
}

describe("app.json", () => {
	it("has no upstream owner, projectId or updates URL, and updates are disabled", () => {
		expect(expo.owner).toBeUndefined();
		expect(expo.extra?.eas?.projectId).toBeUndefined();
		expect(expo.updates?.url).toBeUndefined();
		expect(expo.updates?.enabled).toBe(false);
		expect(read("app.json")).not.toMatch(/2d7dec61|u\.expo\.dev|untrivial/);
	});
	it("keeps cleartext traffic off for Android", () => {
		const plugin = expo.plugins.find((p: unknown) => Array.isArray(p) && p[0] === "expo-build-properties");
		expect(plugin[1].android.usesCleartextTraffic).toBe(false);
	});
	it("has no EAS config or OTA/store-update dependencies", () => {
		expect(existsSync(join(root, "eas.json"))).toBe(false);
		const pkg = JSON.parse(read("package.json"));
		expect(pkg.dependencies["expo-updates"]).toBeUndefined();
		expect(pkg.dependencies["expo-in-app-updates"]).toBeUndefined();
	});
});

describe("runtime sources", () => {
	const files = sources("app").concat(sources("lib"));
	it("never mention the upstream store listing, Discord, or expo-updates", () => {
		for (const f of files) {
			const text = read(f);
			expect(text, f).not.toMatch(/id6792552173|apps\.apple\.com|discord\.com|expo-updates|expo-in-app-updates/i);
		}
	});
	it("never mount the store nudge", () => {
		expect(existsSync(join(root, "lib/StoreUpdateManager.tsx"))).toBe(false);
		expect(read("app/_layout.tsx")).not.toMatch(/StoreUpdate|UpdatesManager|store-update/);
	});
	it("read and write tokens only via SecureStore, never AsyncStorage", () => {
		for (const f of files) {
			for (const line of read(f).split("\n")) {
				if (/AsyncStorage\.(set|get|multiSet)\w*\(/.test(line)) expect(line, f).not.toMatch(/token/i);
			}
		}
		for (const f of ["lib/hosts.ts", "lib/config.ts"]) expect(read(f), f).toMatch(/SecureStore/);
	});

});
