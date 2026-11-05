import { mkdtempSync, readFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { createRequire } from "node:module";
import { describe, expect, it } from "vitest";

// electron-updater is CommonJS and calls require("electron") itself, which
// vi.mock does not reach; seed Node's require cache with a fake instead.
const nodeRequire = createRequire(import.meta.url);
const electronPath = nodeRequire.resolve("electron");
const fakeElectron = {
	app: { getVersion: () => "0.13.5", isPackaged: true, getAppPath: () => "/x", getPath: () => os.tmpdir(), name: "Tallpine", on: () => undefined },
	autoUpdater: { on: () => undefined },
	net: { request: () => ({ on: () => undefined, end: () => undefined }) },
};
nodeRequire.cache[electronPath] = { id: electronPath, filename: electronPath, loaded: true, exports: fakeElectron } as unknown as NodeJS.Module;

import { createMacUpdater } from "./mac-updater";
import { createUpdaterLog } from "./updater-log";

describe("createMacUpdater", () => {
	it("gives the updater an HTTP executor so feed requests do not hit a null executor", () => {
		const updater = createMacUpdater(os.tmpdir()) as unknown as { httpExecutor: { request?: unknown } | null };
		expect(updater.httpExecutor).not.toBeNull();
		expect(typeof updater.httpExecutor?.request).toBe("function");
	});
});

describe("createUpdaterLog", () => {
	it("persists errors under <stateDir>/logs/updater.log", () => {
		const dir = mkdtempSync(path.join(process.env.TMPDIR ?? os.tmpdir(), "updlog-"));
		const log = createUpdaterLog(dir);
		log.log("update failed", new TypeError("boom"));
		log.logger.warn("proxy");
		const text = readFileSync(path.join(dir, "logs", "updater.log"), "utf8");
		expect(text).toContain("ERROR update failed: TypeError: boom");
		expect(text).toContain("WARN proxy");
	});
});
