// @vitest-environment node
import { describe, expect, it, vi } from "vitest";

// The team id is injected at build time (vite.main.config.ts); tests use a placeholder.
vi.hoisted(() => vi.stubGlobal("__TALLPINE_TEAM_ID__", "TESTTEAM01"));
import { mkdtemp, mkdir, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { feedConfig, feedConfigYaml, UPDATE_TEAM_ID } from "./update-feed";
import { isManagedUpdatePolicy, parseManagedUpdatePolicy, readManagedUpdatePolicy } from "./update-policy";
import { planUpdates, readBuildDirty, readBuildKind, type UpdateEnvironment } from "./update-eligibility";
import { isQuietUpdateError } from "./update-errors";
import { coerceUpdateSettings, readUpdateSettings, writeUpdateSettings, DEFAULT_UPDATE_SETTINGS } from "./update-settings";

const releaseEnv: UpdateEnvironment = { packaged: true, platform: "darwin", version: "1.4.0", buildDirty: "false", teamId: UPDATE_TEAM_ID };

describe("feed config", () => {
	it("uses the GitHub provider for justvibedev/tallpine", () => {
		expect(feedConfig({})).toEqual({ provider: "github", owner: "justvibedev", repo: "tallpine", updaterCacheDirName: "updater" });
	});

	it("reads a pinned release from that exact tag on github.com", () => {
		expect(feedConfig({ pinnedVersion: "1.2.3" })).toEqual({
			provider: "generic",
			url: "https://github.com/justvibedev/tallpine/releases/download/v1.2.3",
			updaterCacheDirName: "updater",
		});
	});

	it("serializes as app-update.yml", () => {
		expect(feedConfigYaml(feedConfig({}))).toBe('provider: "github"\nowner: "justvibedev"\nrepo: "tallpine"\nupdaterCacheDirName: "updater"\n');
	});
});

describe("managed policy", () => {
	it("reads AutoUpdate, AutoUpdateChannel and AutoUpdatePinnedVersion", () => {
		expect(parseManagedUpdatePolicy({ AutoUpdate: false, AutoUpdateChannel: "beta", AutoUpdatePinnedVersion: "v2.0.1", rules: {} })).toEqual({
			enabled: false,
			channel: "beta",
			pinnedVersion: "2.0.1",
		});
	});

	it("ignores the agent policy keys and is not a lock on its own", () => {
		const policy = parseManagedUpdatePolicy({ schemaVersion: 2, rules: { permissionCap: { max: "auto" } } });
		expect(policy).toEqual({});
		expect(isManagedUpdatePolicy(policy)).toBe(false);
	});

	it("turns updates off for a pin that is not a version", () => {
		expect(parseManagedUpdatePolicy({ AutoUpdatePinnedVersion: "latest please" })).toEqual({ enabled: false });
	});

	it("is empty off macOS and when no profile is installed", async () => {
		expect(await readManagedUpdatePolicy("linux")).toEqual({});
		expect(await readManagedUpdatePolicy("darwin", "/x", async () => "{}", async () => false)).toEqual({});
	});

	it("fails closed when the profile cannot be converted", async () => {
		const policy = await readManagedUpdatePolicy("darwin", "/x", async () => { throw new Error("plutil"); }, async () => true);
		expect(policy).toEqual({ invalid: true });
		expect(planUpdates(releaseEnv, DEFAULT_UPDATE_SETTINGS, policy)).toEqual({ active: false, reason: "managed" });
	});
});

describe("planUpdates", () => {
	it("updates a signed clean release build by default", () => {
		expect(planUpdates(releaseEnv, DEFAULT_UPDATE_SETTINGS, {})).toEqual({ active: true, channel: "stable" });
	});

	it("never updates dev runs", () => {
		expect(planUpdates({ ...releaseEnv, packaged: false }, DEFAULT_UPDATE_SETTINGS, { enabled: true })).toEqual({ active: false, reason: "unpackaged" });
	});

	it("never updates dirty, unstamped or non-release builds", () => {
		for (const env of [{ ...releaseEnv, buildDirty: "true" }, { ...releaseEnv, buildDirty: undefined }, { ...releaseEnv, version: "dev" }, { ...releaseEnv, version: "1.4.0-dev.abcd1234", buildKind: "local" }]) {
			expect(planUpdates(env, DEFAULT_UPDATE_SETTINGS, {})).toEqual({ active: false, reason: "not-a-release" });
		}
	});

	it("requires the running app to be signed by our team", () => {
		expect(planUpdates({ ...releaseEnv, teamId: null }, DEFAULT_UPDATE_SETTINGS, {})).toEqual({ active: false, reason: "unsigned" });
		expect(planUpdates({ ...releaseEnv, teamId: "OTHERTEAM1" }, DEFAULT_UPDATE_SETTINGS, {})).toEqual({ active: false, reason: "unsigned" });
	});

	it("honours the user toggle unless managed config decides", () => {
		const off = { enabled: false, channel: "stable" as const };
		expect(planUpdates(releaseEnv, off, {})).toEqual({ active: false, reason: "user" });
		expect(planUpdates(releaseEnv, off, { enabled: true })).toEqual({ active: true, channel: "stable" });
		expect(planUpdates(releaseEnv, DEFAULT_UPDATE_SETTINGS, { enabled: false })).toEqual({ active: false, reason: "managed" });
	});

	it("takes the managed channel and pin over the user's", () => {
		expect(planUpdates(releaseEnv, { enabled: true, channel: "stable" }, { channel: "beta", pinnedVersion: "1.5.0" })).toEqual({
			active: true,
			channel: "beta",
			pinnedVersion: "1.5.0",
		});
	});
});

describe("readBuildDirty", () => {
	it("reads VOBuildDirty from the bundle's Info.plist", async () => {
		const contents = await mkdtemp(path.join(os.tmpdir(), "tp-update-"));
		await mkdir(path.join(contents, "Resources"));
		await writeFile(path.join(contents, "Info.plist"), "<dict><key>VOBuildDirty</key>\n\t<string>false</string></dict>");
		expect(await readBuildDirty(path.join(contents, "Resources"))).toBe("false");
		expect(await readBuildDirty(path.join(contents, "missing", "Resources"))).toBeUndefined();
	});
});

describe("readBuildKind", () => {
	it("reads TallpineBuildKind from the bundle's Info.plist", async () => {
		const contents = await mkdtemp(path.join(os.tmpdir(), "tp-update-"));
		await mkdir(path.join(contents, "Resources"));
		const resources = path.join(contents, "Resources");
		await writeFile(path.join(contents, "Info.plist"), "<dict><key>TallpineBuildKind</key>\n\t<string>local</string></dict>");
		expect(await readBuildKind(resources)).toBe("local");
		await writeFile(path.join(contents, "Info.plist"), "<dict><key>VOBuildDirty</key><string>false</string></dict>");
		expect(await readBuildKind(resources)).toBeUndefined();
		expect(await readBuildKind(path.join(contents, "missing", "Resources"))).toBeUndefined();
	});
});

describe("quiet update errors", () => {
	it.each([
		new Error("HttpError: 404 Not Found"),
		new Error("No published versions on GitHub"),
		new Error("net::ERR_INTERNET_DISCONNECTED"),
		Object.assign(new Error("getaddrinfo ENOTFOUND github.com"), { code: "ENOTFOUND" }),
		new Error("Cannot find latest-mac.yml in the latest release artifacts"),
	])("stays quiet on %s", (error) => {
		expect(isQuietUpdateError(error)).toBe(true);
	});

	it("reports a signature failure", () => {
		expect(isQuietUpdateError(new Error("Code signature at URL file:///… did not pass validation: code failed to satisfy specified code requirement(s)"))).toBe(false);
	});
});

describe("update settings", () => {
	it("defaults to on, stable", async () => {
		const dir = await mkdtemp(path.join(os.tmpdir(), "tp-update-"));
		expect(await readUpdateSettings(dir)).toEqual({ enabled: true, channel: "stable" });
		expect(coerceUpdateSettings({ enabled: "nope" })).toEqual({ enabled: true, channel: "stable" });
	});

	it("round-trips an explicit opt-out", async () => {
		const dir = await mkdtemp(path.join(os.tmpdir(), "tp-update-"));
		await writeUpdateSettings(dir, { enabled: false, channel: "beta" });
		expect(await readUpdateSettings(dir)).toEqual({ enabled: false, channel: "beta" });
	});
});

describe("managed key names", () => {
	it("come from the generated registry list, not literals in update-policy", async () => {
		const keys = (await import("../shared/managed-keys.json")).default;
		const plist = Object.fromEntries(keys.map((k) => [k.key, k.plistKey]));
		expect(plist["app.update.enabled"]).toBe("AutoUpdate");
		expect(plist["app.update.channel"]).toBe("AutoUpdateChannel");
		expect(plist["app.update.pinnedVersion"]).toBe("AutoUpdatePinnedVersion");
		expect(parseManagedUpdatePolicy({ [plist["app.update.enabled"]]: false, [plist["app.update.channel"]]: "beta" })).toEqual({ enabled: false, channel: "beta" });
	});
});
