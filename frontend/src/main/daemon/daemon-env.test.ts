import path from "node:path";
import { describe, expect, it } from "vitest";
import { buildDesktopDaemonEnv, daemonLaunchEnv, resolveDaemonPort, type DaemonEnvInputs } from "./daemon-env";

const base: DaemonEnvInputs = {
	env: {},
	platform: "win32", // win32 returns the merged env verbatim, no shell probing
	isDev: false,
	isPackaged: true,
	appPath: "/app",
	resourcesPath: "/res",
	dataDir: "/data",
	appRunId: "run-1",
	forceKeep: false,
	stagedTmuxBinary: null,
	runFilePath: "/run.json",
	rendererUrl: "app://renderer/",
	cachedShellEnv: null,
	loginShell: undefined,
};

describe("buildDesktopDaemonEnv", () => {
	it("with TALLPINE_* unset: stamps owner, data dir, run id and bundled ACP runtime", () => {
		const env = buildDesktopDaemonEnv(base);
		expect(env.TALLPINE_OWNER).toBe("app");
		expect(env.TALLPINE_DATA_DIR).toBe("/data");
		expect(env.TALLPINE_APP_RUN_ID).toBe("run-1");
		expect(env.TALLPINE_ACP_RUNTIME_DIR).toBe(path.join("/res", "acp-runtime"));
		expect(env.TALLPINE_BROWSER_RUNTIME_TOKEN).toBe("");
		expect(env.TALLPINE_TMUX_BINARY).toBeUndefined();
	});

	it("with TALLPINE_* set: the resolved data dir still wins; the ACP dir override is honoured", () => {
		const env = buildDesktopDaemonEnv({
			...base,
			env: { TALLPINE_DATA_DIR: "relative", TALLPINE_ACP_RUNTIME_DIR: "/acp", TALLPINE_OWNER: "x" },
			forceKeep: true,
			stagedTmuxBinary: "/data/runtime/tmux/v/tmux",
		});
		expect(env.TALLPINE_DATA_DIR).toBe("/data");
		expect(env.TALLPINE_OWNER).toBe("persistent");
		expect(env.TALLPINE_ACP_RUNTIME_DIR).toBe("/acp");
		expect(env.TALLPINE_TMUX_BINARY).toBe("/data/runtime/tmux/v/tmux");
	});

	it("dev: isolates port and run file unless set, and never overrides the resolved data dir", () => {
		const unset = buildDesktopDaemonEnv({ ...base, isDev: true, isPackaged: false });
		expect(unset.TALLPINE_PORT).toBeDefined();
		expect(unset.TALLPINE_RUN_FILE).toBe("/run.json");
		expect(unset.TALLPINE_DATA_DIR).toBe("/data");
		const set = buildDesktopDaemonEnv({ ...base, isDev: true, isPackaged: false, env: { TALLPINE_PORT: "4000", TALLPINE_RUN_FILE: "/mine" } });
		expect(set.TALLPINE_PORT).toBe("4000");
		expect(set.TALLPINE_RUN_FILE).toBe("/mine");
	});
});

describe("daemon port and launch env", () => {
	it("dev uses the dev port only when TALLPINE_PORT is unset", () => {
		expect(resolveDaemonPort(true, {})).not.toBe(resolveDaemonPort(true, { TALLPINE_PORT: "4123" }));
		expect(resolveDaemonPort(true, { TALLPINE_PORT: "4123" })).toBe(4123);
	});
	it("returns the env untouched outside Windows dev", () => {
		const env = { A: "1" };
		expect(daemonLaunchEnv(true, "darwin", "/app", env)).toBe(env);
		expect(daemonLaunchEnv(false, "win32", "/app", env)).toBe(env);
	});
});
