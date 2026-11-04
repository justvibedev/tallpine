// @vitest-environment node
import { describe, expect, it, vi } from "vitest";

// The team id is injected at build time (vite.main.config.ts); tests use a placeholder.
vi.hoisted(() => vi.stubGlobal("__TALLPINE_TEAM_ID__", "TESTTEAM01"));
import { EventEmitter } from "node:events";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { mkdtemp, readFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { CHECK_INTERVAL_MS, createAutoUpdater, FIRST_CHECK_DELAY_MS, IDLE_POLL_MS, type UpdaterLike } from "./auto-updater";
import { UPDATE_TEAM_ID } from "./update-feed";
import type { ManagedUpdatePolicy } from "./update-policy";
import type { UpdateEnvironment } from "./update-eligibility";
import { writeUpdateSettings, type UpdateStatus } from "./update-settings";

class FakeUpdater extends EventEmitter implements UpdaterLike {
	autoDownload = false;
	autoInstallOnAppQuit = false;
	allowPrerelease = false;
	allowDowngrade = true;
	disableDifferentialDownload = false;
	updateConfigPath: string | null = null;
	checks = 0;
	installs: unknown[][] = [];
	onCheck: () => void = () => undefined;
	async checkForUpdates() {
		this.checks += 1;
		this.emit("checking-for-update");
		this.onCheck();
		return null;
	}
	quitAndInstall(...args: unknown[]) {
		this.installs.push(args);
	}
}

const releaseEnv: UpdateEnvironment = { packaged: true, platform: "darwin", version: "1.0.0", buildDirty: "false", teamId: UPDATE_TEAM_ID };

async function setup(options: { env?: Partial<UpdateEnvironment>; policy?: ManagedUpdatePolicy } = {}) {
	vi.useFakeTimers();
	const stateDir = await mkdtemp(path.join(os.tmpdir(), "tp-updater-"));
	const fake = new FakeUpdater();
	const statuses: UpdateStatus[] = [];
	let policy = options.policy ?? {};
	const created = vi.fn(() => fake);
	const beforeInstall = vi.fn();
	const installFailed = vi.fn();
	const controller = createAutoUpdater({
		stateDir,
		createUpdater: created,
		environment: async () => ({ ...releaseEnv, ...options.env }),
		readPolicy: async () => policy,
		onStatus: (s) => statuses.push(s),
		beforeInstall,
		installFailed,
		log: () => undefined,
	});
	await controller.start();
	return { controller, fake, statuses, stateDir, created, beforeInstall, installFailed, setPolicy: (p: ManagedUpdatePolicy) => { policy = p; } };
}

const flush = () => vi.advanceTimersByTimeAsync(0);

describe("auto-updater controller", () => {
	it("checks after the launch delay and then every 6 hours", async () => {
		const { fake } = await setup();
		expect(fake.checks).toBe(0);
		await vi.advanceTimersByTimeAsync(FIRST_CHECK_DELAY_MS);
		expect(fake.checks).toBe(1);
		await vi.advanceTimersByTimeAsync(CHECK_INTERVAL_MS);
		expect(fake.checks).toBe(2);
		vi.useRealTimers();
	});

	it("points electron-updater at the GitHub feed under the state dir, full downloads, no downgrades", async () => {
		const { fake, stateDir } = await setup();
		expect(fake.updateConfigPath).toBe(path.join(stateDir, "updater", "app-update.yml"));
		expect(await readFile(fake.updateConfigPath!, "utf8")).toContain('provider: "github"\nowner: "justvibedev"\nrepo: "tallpine"');
		expect(fake.autoDownload).toBe(true);
		expect(fake.allowDowngrade).toBe(false);
		expect(fake.disableDifferentialDownload).toBe(true);
		// Squirrel is fed only by quitAndInstall, never staged ahead of a quit.
		expect(fake.autoInstallOnAppQuit).toBe(false);
		vi.useRealTimers();
	});

	it("never builds an updater for a dev run", async () => {
		const { created, statuses, controller } = await setup({ env: { packaged: false } });
		await vi.advanceTimersByTimeAsync(CHECK_INTERVAL_MS * 2);
		expect(created).not.toHaveBeenCalled();
		expect(statuses.at(-1)).toEqual({ state: "disabled", reason: "unpackaged" });
		expect(controller.install()).toBe(false);
		vi.useRealTimers();
	});

	it("never builds an updater for a local build", async () => {
		const { created, statuses } = await setup({ env: { buildDirty: "true" } });
		await vi.advanceTimersByTimeAsync(CHECK_INTERVAL_MS);
		expect(created).not.toHaveBeenCalled();
		expect(statuses.at(-1)).toEqual({ state: "disabled", reason: "not-a-release" });
		vi.useRealTimers();
	});

	it("stays quiet when the feed does not exist", async () => {
		const { fake, statuses } = await setup();
		fake.onCheck = () => fake.emit("error", new Error("HttpError: 404 Not Found \"https://github.com/justvibedev/tallpine/releases.atom\""));
		await vi.advanceTimersByTimeAsync(FIRST_CHECK_DELAY_MS);
		expect(statuses.at(-1)).toEqual({ state: "idle" });
		expect(statuses.some((s) => s.state === "error")).toBe(false);
		vi.useRealTimers();
	});

	it("reports a real failure", async () => {
		const { fake, statuses } = await setup();
		fake.onCheck = () => fake.emit("error", new Error("code failed to satisfy specified code requirement(s)"));
		await vi.advanceTimersByTimeAsync(FIRST_CHECK_DELAY_MS);
		expect(statuses.at(-1)).toMatchObject({ state: "error", message: "code failed to satisfy specified code requirement(s)" });
		vi.useRealTimers();
	});

	it("restarts into a downloaded update only when one is staged", async () => {
		const { fake, controller, beforeInstall } = await setup();
		expect(controller.install()).toBe(false);
		fake.emit("update-downloaded", { version: "1.1.0" });
		expect(controller.getStatus()).toMatchObject({ state: "downloaded", version: "1.1.0" });
		expect(controller.install()).toBe(true);
		expect(beforeInstall).toHaveBeenCalledOnce();
		expect(fake.installs).toEqual([[false, true]]);
		// Staged: no further checks would flip the status away from "ready".
		await vi.advanceTimersByTimeAsync(CHECK_INTERVAL_MS * 2);
		expect(fake.checks).toBe(0);
		vi.useRealTimers();
	});

	it("stops checking when the user turns updates off, and resumes when back on", async () => {
		const { fake, controller, statuses } = await setup();
		await controller.setSettings({ enabled: false, channel: "stable" });
		expect(statuses.at(-1)).toEqual({ state: "disabled", reason: "user" });
		await vi.advanceTimersByTimeAsync(CHECK_INTERVAL_MS * 2);
		expect(fake.checks).toBe(0);
		await controller.setSettings({ enabled: true, channel: "beta" });
		expect(fake.allowPrerelease).toBe(true);
		await vi.advanceTimersByTimeAsync(FIRST_CHECK_DELAY_MS);
		expect(fake.checks).toBe(1);
		vi.useRealTimers();
	});

	it("reports a failed install, clears the restart mark, and fetches again", async () => {
		const { fake, controller, statuses, installFailed } = await setup();
		fake.emit("update-downloaded", { version: "1.1.0" });
		expect(controller.install()).toBe(true);
		expect(controller.getStatus().state).toBe("installing");
		// Repeat clicks while Squirrel prepares do nothing.
		expect(controller.install()).toBe(false);
		expect(fake.installs).toHaveLength(1);
		fake.emit("error", new Error("Code signature at URL did not pass validation"));
		expect(statuses.at(-1)).toMatchObject({ state: "error", message: "Code signature at URL did not pass validation" });
		expect(installFailed).toHaveBeenCalledOnce();
		await vi.advanceTimersByTimeAsync(FIRST_CHECK_DELAY_MS);
		expect(fake.checks).toBe(1);
		vi.useRealTimers();
	});

	it("reports even a network-looking failure while installing", async () => {
		const { fake, controller, statuses } = await setup();
		fake.emit("update-downloaded", { version: "1.1.0" });
		controller.install();
		fake.emit("error", new Error("net::ERR_CONNECTION_REFUSED"));
		expect(statuses.at(-1)?.state).toBe("error");
		vi.useRealTimers();
	});

	it("turning updates off mid-download neither offers nor installs it", async () => {
		const { fake, controller, statuses } = await setup();
		fake.emit("update-available", { version: "1.1.0" });
		await controller.setSettings({ enabled: false, channel: "stable" });
		fake.emit("update-downloaded", { version: "1.1.0" });
		expect(statuses.at(-1)).toEqual({ state: "disabled", reason: "user" });
		expect(controller.install()).toBe(false);
		expect(fake.installs).toEqual([]);
		vi.useRealTimers();
	});

	it("turning updates off after a download drops it without handing it to Squirrel", async () => {
		const { fake, controller } = await setup();
		fake.emit("update-downloaded", { version: "1.1.0" });
		await controller.setSettings({ enabled: false, channel: "stable" });
		expect(fake.autoInstallOnAppQuit).toBe(false);
		expect(controller.getStatus()).toEqual({ state: "disabled", reason: "user" });
		expect(controller.install()).toBe(false);
		expect(fake.installs).toEqual([]);
		vi.useRealTimers();
	});

	it("never stages for install-on-quit, also after turning updates back on", async () => {
		const { fake, controller } = await setup();
		await controller.setSettings({ enabled: false, channel: "stable" });
		await controller.setSettings({ enabled: true, channel: "stable" });
		expect(fake.autoInstallOnAppQuit).toBe(false);
		vi.useRealTimers();
	});

	it("starts disabled when the user turned updates off earlier", async () => {
		vi.useRealTimers();
		const stateDir = await mkdtemp(path.join(os.tmpdir(), "tp-updater-"));
		await writeUpdateSettings(stateDir, { enabled: false, channel: "stable" });
		const created = vi.fn(() => new FakeUpdater());
		const controller = createAutoUpdater({ stateDir, createUpdater: created, environment: async () => releaseEnv, readPolicy: async () => ({}), onStatus: () => undefined, beforeInstall: () => undefined, installFailed: () => undefined });
		await controller.start();
		expect(controller.getStatus()).toEqual({ state: "disabled", reason: "user" });
		expect(created).not.toHaveBeenCalled();
	});

	it("locks the settings when managed config decides", async () => {
		const { controller } = await setup({ policy: { enabled: false } });
		await expect(controller.setSettings({ enabled: true, channel: "stable" })).rejects.toThrow(/managed/);
		expect(await controller.getPreferences()).toEqual({ settings: { enabled: false, channel: "stable" }, locked: true });
		expect(controller.getStatus()).toEqual({ state: "disabled", reason: "managed" });
		vi.useRealTimers();
	});

	it("follows a managed pin on github.com", async () => {
		const { fake, controller } = await setup({ policy: { pinnedVersion: "1.2.0" } });
		expect(await readFile(fake.updateConfigPath!, "utf8")).toContain("https://github.com/justvibedev/tallpine/releases/download/v1.2.0");
		expect(await controller.getPreferences()).toMatchObject({ locked: true, pinnedVersion: "1.2.0" });
		await flush();
		vi.useRealTimers();
	});
});

describe("no update telemetry", () => {
	const mainDir = path.dirname(new URL(import.meta.url).pathname);
	const updaterSources = readdirSync(mainDir)
		.filter((f) => /^(auto-updater|app-updates|update-[a-z-]+)\.ts$/.test(f) && !f.endsWith(".test.ts"))
		.map((f) => path.join(mainDir, f));

	it("has no telemetry module", () => {
		expect(existsSync(path.join(mainDir, "..", "shared", "update-telemetry.ts"))).toBe(false);
	});

	it("talks to no server but github.com", () => {
		expect(updaterSources.length).toBeGreaterThanOrEqual(6);
		for (const file of updaterSources) {
			const source = readFileSync(file, "utf8");
			expect(source, file).not.toMatch(/update-telemetry|trackUpdate|posthog|sentry|\bfetch\(|net\.request|https?\.request/i);
			for (const url of source.match(/https?:\/\/[^\s"'`)]+/g) ?? []) expect(url, file).toMatch(/^https:\/\/github\.com\//);
		}
	});
});

describe("restart when idle", () => {
	async function staged(busy: { value: boolean | "throw" }, policy?: ManagedUpdatePolicy) {
		vi.useFakeTimers();
		const stateDir = await mkdtemp(path.join(os.tmpdir(), "tp-updater-"));
		const fake = new FakeUpdater();
		let currentPolicy: ManagedUpdatePolicy = policy ?? {};
		const statuses: UpdateStatus[] = [];
		const beforeInstall = vi.fn();
		const probes = { count: 0 };
		const controller = createAutoUpdater({
			stateDir,
			createUpdater: () => fake,
			environment: async () => releaseEnv,
			readPolicy: async () => currentPolicy,
			onStatus: (s) => statuses.push(s),
			beforeInstall,
			installFailed: vi.fn(),
			agentsBusy: async () => { probes.count += 1; if (busy.value === "throw") throw new Error("down"); return busy.value; },
			log: () => undefined,
		});
		await controller.start();
		fake.emit("update-downloaded", { version: "2.0.0" });
		return { controller, fake, beforeInstall, statuses, stateDir, probes, setPolicy: (p: ManagedUpdatePolicy) => { currentPolicy = p; } };
	}

	it("waits while a turn or review is active, then restarts once idle", async () => {
		const busy = { value: true as boolean | "throw" };
		const { controller, fake, beforeInstall } = await staged(busy);
		expect(controller.installWhenIdle()).toBe(true);
		expect(controller.getStatus().restartWhenIdle).toBe(true);
		await vi.advanceTimersByTimeAsync(IDLE_POLL_MS * 3);
		expect(fake.installs).toHaveLength(0);
		busy.value = false;
		// idleTick awaits real file reads (apply()) that fake timers don't drive, and a
		// tick still in flight makes later polls no-ops; so keep ticking until it lands.
		await vi.waitFor(async () => {
			await vi.advanceTimersByTimeAsync(IDLE_POLL_MS);
			expect(fake.installs).toEqual([[false, true]]);
		});
		expect(beforeInstall).toHaveBeenCalledTimes(1);
		expect(controller.getStatus().state).toBe("installing");
		expect(controller.getStatus().restartWhenIdle).toBeUndefined();
	});

	it("restarts immediately when already idle", async () => {
		const { controller, fake } = await staged({ value: false });
		controller.installWhenIdle();
		await vi.waitFor(() => expect(fake.installs).toHaveLength(1));
	});

	it("treats an unreadable answer as busy", async () => {
		const { controller, fake } = await staged({ value: "throw" });
		controller.installWhenIdle();
		await vi.advanceTimersByTimeAsync(IDLE_POLL_MS * 2);
		expect(fake.installs).toHaveLength(0);
	});

	it("cancel stops the watch", async () => {
		const busy = { value: true as boolean | "throw" };
		const { controller, fake } = await staged(busy);
		controller.installWhenIdle();
		controller.cancelInstallWhenIdle();
		expect(controller.getStatus().restartWhenIdle).toBeUndefined();
		busy.value = false;
		await vi.advanceTimersByTimeAsync(IDLE_POLL_MS * 3);
		expect(fake.installs).toHaveLength(0);
		expect(controller.getStatus().state).toBe("downloaded");
	});

	it("turning updates off drops the pending restart", async () => {
		const busy = { value: true as boolean | "throw" };
		const { controller, fake, stateDir } = await staged(busy);
		controller.installWhenIdle();
		await writeUpdateSettings(stateDir, { enabled: false, channel: "stable" });
		await controller.setSettings({ enabled: false, channel: "stable" });
		busy.value = false;
		await vi.advanceTimersByTimeAsync(IDLE_POLL_MS * 3);
		expect(fake.installs).toHaveLength(0);
		expect(controller.getStatus().state).toBe("disabled");
		expect(controller.installWhenIdle()).toBe(false);
	});

	it("stops the idle poll when updates are turned off", async () => {
		const { controller } = await staged({ value: true });
		controller.installWhenIdle();
		expect(vi.getTimerCount()).toBe(1);
		await controller.setSettings({ enabled: false, channel: "stable" });
		expect(vi.getTimerCount()).toBe(0);
	});

	it("honours a managed policy that switches updates off while the restart is pending", async () => {
		const busy = { value: true as boolean | "throw" };
		const { controller, fake, setPolicy, probes } = await staged(busy);
		controller.installWhenIdle();
		await vi.waitFor(() => expect(probes.count).toBe(1));
		setPolicy({ enabled: false });
		busy.value = false;
		await vi.waitFor(async () => {
			await vi.advanceTimersByTimeAsync(IDLE_POLL_MS);
			expect(controller.getStatus()).toMatchObject({ state: "disabled" });
		});
		expect(fake.installs).toHaveLength(0);
	});

	it("does not install when updates are turned off while the idle check is in flight", async () => {
		let release: (busy: boolean) => void = () => undefined;
		vi.useFakeTimers();
		const stateDir = await mkdtemp(path.join(os.tmpdir(), "tp-updater-"));
		const fake = new FakeUpdater();
		const controller = createAutoUpdater({
			stateDir,
			createUpdater: () => fake,
			environment: async () => releaseEnv,
			readPolicy: async () => ({}),
			onStatus: () => undefined,
			beforeInstall: vi.fn(),
			installFailed: vi.fn(),
			agentsBusy: () => new Promise<boolean>((resolve) => { release = resolve; }),
			log: () => undefined,
		});
		await controller.start();
		fake.emit("update-downloaded", { version: "2.0.0" });
		controller.installWhenIdle();
		await flush();
		await controller.setSettings({ enabled: false, channel: "stable" });
		release(false);
		await flush();
		expect(fake.installs).toHaveLength(0);
	});

	it("is refused with nothing staged", async () => {
		const { controller, fake } = await setup();
		expect(controller.installWhenIdle()).toBe(false);
		expect(fake.installs).toHaveLength(0);
	});
});
