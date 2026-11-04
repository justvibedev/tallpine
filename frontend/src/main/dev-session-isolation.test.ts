import path from "node:path";
import { describe, expect, it } from "vitest";
import { isolateDevSessionEnv } from "./dev-session-isolation";

const homeDir = "/Users/dev";
const real = path.join(homeDir, ".tallpine");

// What a worker inherits from the production app plus its session wiring.
function sessionEnv(extra: Record<string, string> = {}): Record<string, string | undefined> {
	return {
		PATH: "/usr/bin",
		TALLPINE_SESSION_ID: "aox-7",
		TALLPINE_SESSION_TOKEN: "scoped",
		TALLPINE_DATA_DIR: path.join(real, "data"),
		TALLPINE_RUN_FILE: path.join(real, "running.json"),
		TALLPINE_PORT: "3101",
		TALLPINE_TMUX_SOCKET_NAME: "tallpine",
		TALLPINE_APP_RUN_ID: "apprun-prod",
		...extra,
	};
}

describe("isolateDevSessionEnv", () => {
	it("leaves a dev build outside any session alone", () => {
		const env = { PATH: "/usr/bin", TALLPINE_DATA_DIR: path.join(real, "data") };
		expect(isolateDevSessionEnv(env, { isPackaged: false, homeDir, checkoutDir: "/w/a/frontend" })).toBeNull();
		expect(env.TALLPINE_DATA_DIR).toBe(path.join(real, "data"));
	});

	it("moves a dev build inside a session off the inherited production state", () => {
		const env = sessionEnv({ TALLPINE_DEV_ELECTRON_DIR: "" });
		const isolation = isolateDevSessionEnv(env, { isPackaged: false, homeDir, checkoutDir: "/w/a/frontend" });
		expect(isolation?.sessionId).toBe("aox-7");
		const stateDir = isolation?.stateDir ?? "";
		expect(stateDir.startsWith(path.join(real, "dev", "checkouts") + path.sep)).toBe(true);
		expect(env.TALLPINE_DATA_DIR).toBe(path.join(stateDir, "data"));
		expect(env.TALLPINE_RUN_FILE).toBe(path.join(stateDir, "running.json"));
		expect(env.TALLPINE_DEV_ELECTRON_DIR).toBe(path.join(stateDir, "electron"));
		const port = Number(env.TALLPINE_PORT);
		expect(port).toBeGreaterThanOrEqual(3200);
		expect(port).toBeLessThan(3700);
		for (const name of ["TALLPINE_SESSION_ID", "TALLPINE_SESSION_TOKEN", "TALLPINE_TMUX_SOCKET_NAME", "TALLPINE_APP_RUN_ID"]) {
			expect(env[name], name).toBeUndefined();
		}
		expect(env.PATH).toBe("/usr/bin");
	});

	it("gives each checkout its own state and a stable port", () => {
		const opts = { isPackaged: false, homeDir };
		const a = isolateDevSessionEnv(sessionEnv(), { ...opts, checkoutDir: "/w/a/frontend" });
		const again = isolateDevSessionEnv(sessionEnv(), { ...opts, checkoutDir: "/w/a/frontend" });
		const b = isolateDevSessionEnv(sessionEnv(), { ...opts, checkoutDir: "/w/b/frontend" });
		expect(again).toEqual(a);
		expect(b?.stateDir).not.toBe(a?.stateDir);
	});

	it("keeps explicit scratch overrides and dev knobs", () => {
		const env = sessionEnv({
			TALLPINE_DATA_DIR: "/tmp/vo-lab/data",
			TALLPINE_RUN_FILE: path.join(real, "dev", "lab", "running.json"),
			TALLPINE_PORT: "3999",
			TALLPINE_DEV_ELECTRON_DIR: "/tmp/vo-lab/electron",
			TALLPINE_OPEN_DEVTOOLS: "1",
		});
		isolateDevSessionEnv(env, { isPackaged: false, homeDir, checkoutDir: "/w/a/frontend" });
		expect(env.TALLPINE_DATA_DIR).toBe("/tmp/vo-lab/data");
		expect(env.TALLPINE_RUN_FILE).toBe(path.join(real, "dev", "lab", "running.json"));
		expect(env.TALLPINE_PORT).toBe("3999");
		expect(env.TALLPINE_DEV_ELECTRON_DIR).toBe("/tmp/vo-lab/electron");
		expect(env.TALLPINE_OPEN_DEVTOOLS).toBe("1");
	});

	it("keeps scratch state under the session TMPDIR inside the real root", () => {
		const tmp = path.join(real, "data", "tmp", "agent-1", "dv");
		const env = sessionEnv({ TALLPINE_DATA_DIR: path.join(tmp, "data"), TALLPINE_RUN_FILE: path.join(tmp, "running.json") });
		isolateDevSessionEnv(env, { isPackaged: false, homeDir, checkoutDir: "/w/a/frontend" });
		expect(env.TALLPINE_DATA_DIR).toBe(path.join(tmp, "data"));
		expect(env.TALLPINE_RUN_FILE).toBe(path.join(tmp, "running.json"));
	});

	it("isolates inside an official AO session too", () => {
		const env: Record<string, string | undefined> = { AO_SESSION_ID: "tallpine-77" };
		const isolation = isolateDevSessionEnv(env, { isPackaged: false, homeDir, checkoutDir: "/w/a/frontend" });
		expect(isolation?.sessionId).toBe("tallpine-77");
		expect(env.AO_SESSION_ID).toBeUndefined();
		expect(env.TALLPINE_DATA_DIR).toBe(path.join(isolation?.stateDir ?? "", "data"));
	});

	it("honours TALLPINE_DEV_REAL_DATA=1", () => {
		const env = sessionEnv({ TALLPINE_DEV_REAL_DATA: "1" });
		expect(isolateDevSessionEnv(env, { isPackaged: false, homeDir, checkoutDir: "/w/a/frontend" })).toBeNull();
		expect(env.TALLPINE_DATA_DIR).toBe(path.join(real, "data"));
	});

	it("only drops the session markers for a packaged app", () => {
		const env = sessionEnv({ AO_SESSION_ID: "tallpine-77" });
		expect(isolateDevSessionEnv(env, { isPackaged: true, homeDir, checkoutDir: "/Applications/x" })).toBeNull();
		expect(env.TALLPINE_SESSION_ID).toBeUndefined();
		expect(env.AO_SESSION_ID).toBeUndefined();
		expect(env.TALLPINE_DATA_DIR).toBe(path.join(real, "data"));
		expect(env.TALLPINE_TMUX_SOCKET_NAME).toBe("tallpine");
	});
});
