// @vitest-environment node
import { describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => {
	let release: () => void = () => undefined;
	return {
		spawn: vi.fn(),
		gate: new Promise<void>((r) => { release = r; }),
		release: () => release(),
	};
});
vi.mock("electron", () => ({ app: { isPackaged: false, getAppPath: () => "/" } }));
vi.mock("node:fs", () => ({ existsSync: () => true }));
vi.mock("node:fs/promises", () => ({ mkdir: vi.fn(), readFile: vi.fn() }));
vi.mock("../../shared/daemon-launch", () => ({
	resolveDaemonLaunch: () => ({ source: "dev", command: "/d", cwd: "/", args: [] }),
}));
vi.mock("../../shared/daemon-attach", () => ({ resolveDaemonFromPort: async () => null }));
vi.mock("../app-bootstrap", () => ({ APP_DISPLAY_NAME: "T" }));
vi.mock("../browser-runtime-link-host", () => ({ establishSupervisorLink: vi.fn() }));
vi.mock("../bundled-runtime-staging", () => ({
	appRunId: "x",
	browserRuntimeToken: "",
	ensureBundledTmuxStaged: () => h.gate,
	helperSignatureOptions: () => ({}),
}));
vi.mock("../shell-env-host", () => ({ ensureShellEnv: async () => ({}) }));
vi.mock("../adopt-signature", () => ({ adoptionSignatureError: vi.fn(), bundledHelperPaths: vi.fn() }));
vi.mock("../helper-signature", () => ({ verifyBundledHelpers: vi.fn() }));
vi.mock("../legacy-tmux-cleanup", () => ({ legacyStagedTmuxRootFor: vi.fn(), removeLegacyStagedTmux: vi.fn() }));
vi.mock("./daemon-lifecycle", () => ({
	appendDaemonOutput: vi.fn(),
	daemonIdentityError: vi.fn(),
	daemonLaunchEnv: () => ({}),
	gracefullyReplaceDaemonForBrowser: vi.fn(),
	inspectExistingDaemon: async () => null,
	PORT_DISCOVERY_TIMEOUT_MS: 1,
	repairDevAcpRuntime: async () => undefined,
	resolvedDaemonPort: () => 3101,
	RUN_FILE_FRESHNESS_SKEW_MS: 1,
	RUN_FILE_POLL_MS: 1,
	runFilePath: () => null,
	setDaemonStatus: vi.fn(),
	startDaemonForRestart: vi.fn(),
}));
vi.mock("./daemon-port-takeover", () => ({ reclaimOrphanedPortHolder: vi.fn() }));
vi.mock("./daemon-probe", () => ({ readDaemonProbe: vi.fn() }));
vi.mock("./daemon-spawn", () => ({ spawnDaemonProcess: h.spawn }));
vi.mock("./daemon-auto-restart-host", () => ({ noteUnexpectedDaemonExit: vi.fn() }));

import { ctx } from "../app-context";
import { startDaemonInner } from "./daemon-start";

describe("startDaemonInner supersede", () => {
	it("does not spawn when superseded during the late staging await", async () => {
		const run = startDaemonInner(ctx.daemonStartEpoch, true);
		await vi.waitFor(() => expect(h.spawn).not.toHaveBeenCalled());
		await new Promise((r) => setTimeout(r, 20));
		ctx.forceReplaceAdoptedRequested = true;
		ctx.daemonStartEpoch += 1;
		h.release();
		await run;
		expect(h.spawn).not.toHaveBeenCalled();
		expect(ctx.forceReplaceAdoptedRequested).toBe(true);
	});
});
