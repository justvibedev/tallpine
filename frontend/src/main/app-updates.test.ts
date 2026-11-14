import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const test = vi.hoisted(() => ({ createAutoUpdater: vi.fn(), createMacUpdater: vi.fn() }));
vi.mock("electron", () => ({ app: { isPackaged: true, getVersion: () => "1.0.0", once: vi.fn() } }));
vi.mock("./auto-updater", () => ({ createAutoUpdater: test.createAutoUpdater }));
vi.mock("./mac-updater", () => ({ createMacUpdater: test.createMacUpdater }));
vi.mock("./updater-log", () => ({ createUpdaterLog: () => ({ log: vi.fn(), logger: {} }) }));

const realPlatform = process.platform;
const setPlatform = (platform: NodeJS.Platform) => Object.defineProperty(process, "platform", { value: platform, configurable: true });

async function init(platform: NodeJS.Platform) {
	setPlatform(platform);
	vi.resetModules();
	const { initAppUpdates } = await import("./app-updates");
	const handlers = new Map<string, (...args: unknown[]) => unknown>();
	initAppUpdates({
		ipcMain: { handle: (channel: string, fn: (...args: unknown[]) => unknown) => void handlers.set(channel, fn) } as never,
		stateDir: "/state",
		shell: () => null,
		onUpdateRestart: vi.fn(),
		onUpdateRestartFailed: vi.fn(),
		daemonPort: () => null,
	});
	return handlers;
}

beforeEach(() => {
	test.createAutoUpdater.mockReset().mockImplementation(() => ({ start: vi.fn(async () => undefined), getStatus: vi.fn(), dispose: vi.fn() }));
	test.createMacUpdater.mockReset().mockReturnValue({});
});
afterEach(() => setPlatform(realPlatform));

describe("initAppUpdates", () => {
	it("never builds the Mac updater on Windows and reports the platform as unsupported", async () => {
		const handlers = await init("win32");
		expect(test.createAutoUpdater).not.toHaveBeenCalled();
		expect(test.createMacUpdater).not.toHaveBeenCalled();
		expect(handlers.get("appUpdates:getStatus")?.()).toEqual({ state: "disabled", reason: "unsupported-platform" });
		expect(await handlers.get("appUpdates:getPreferences")?.()).toBeNull();
	});

	it("still builds the Mac updater on macOS", async () => {
		await init("darwin");
		expect(test.createAutoUpdater).toHaveBeenCalledTimes(1);
		test.createAutoUpdater.mock.calls[0][0].createUpdater();
		expect(test.createMacUpdater).toHaveBeenCalledWith("/state", {});
	});
});
