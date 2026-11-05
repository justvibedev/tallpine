// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";

const inner = vi.hoisted(() => vi.fn());
vi.mock("./daemon-start", () => ({ startDaemonInner: inner }));
vi.mock("electron", () => ({ app: { getAppPath: () => "/", isPackaged: false }, utilityProcess: {} }));
vi.mock("../app-bootstrap", () => ({ APP_DISPLAY_NAME: "T", desktopDataDir: () => "/", DEV_STATE_SUBDIR: "dev", isDev: false }));
vi.mock("../app-runtime-state", () => ({ daemonToken: "", getShellWebContents: () => null }));
vi.mock("../browser-runtime-link-host", () => ({}));
vi.mock("../bundled-runtime-staging", () => ({ appRunId: "x" }));
vi.mock("../ipc/daemon-ipc", () => ({ captureSideChatState: vi.fn() }));
vi.mock("../shell-env-host", () => ({ configuredLoginShell: () => "/bin/sh" }));
vi.mock("../window/renderer-assets", () => ({ rendererUrl: () => "" }));

import { ctx } from "../app-context";
import { startDaemon } from "./daemon-lifecycle";

describe("startDaemon vs in-flight automatic respawn", () => {
	beforeEach(() => {
		inner.mockReset();
		ctx.daemonStartPromise = null;
		ctx.daemonStartIsAutoRestart = false;
		ctx.browserQuitRequested = false;
	});

	it("an explicit start supersedes the in-flight auto-restart instead of reusing it", async () => {
		inner.mockImplementation(() => new Promise(() => undefined));
		void startDaemon({ autoRestart: true });
		const epoch = ctx.daemonStartEpoch;
		inner.mockImplementation(async () => ({ state: "ready" }));
		await startDaemon();
		expect(inner).toHaveBeenCalledTimes(2);
		expect(inner).toHaveBeenLastCalledWith(epoch + 1, false);
	});

	it("auto-restart calls still share one in-flight start", async () => {
		inner.mockImplementation(() => new Promise(() => undefined));
		void startDaemon({ autoRestart: true });
		void startDaemon({ autoRestart: true });
		expect(inner).toHaveBeenCalledTimes(1);
	});
});
