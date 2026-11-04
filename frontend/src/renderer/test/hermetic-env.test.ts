import { tmpdir } from "node:os";
import { describe, expect, it } from "vitest";
import { isScrubbedEnvName } from "./hermetic-env";

describe("hermetic test env", () => {
	it("scrubs session variables and keeps test opt-ins", () => {
		for (const name of ["TALLPINE_DATA_DIR", "TALLPINE_SESSION_ID", "AO_RUN_FILE", "CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "GH_TOKEN", "TMUX", "HISTFILE"]) {
			expect(isScrubbedEnvName(name), name).toBe(true);
		}
		for (const name of ["TALLPINE_TEST_FIXTURE", "TALLPINE_LIVE_CLAUDE_ACP", "PATH", "TMPDIR", "NODE_ENV"]) {
			expect(isScrubbedEnvName(name), name).toBe(false);
		}
	});

	it("runs every test file with a sandboxed HOME and no inherited session", () => {
		expect(process.env.HOME?.startsWith(tmpdir())).toBe(true);
		expect(process.env.HISTFILE).toBe("/dev/null");
		expect(Object.keys(process.env).filter(isScrubbedEnvName).filter((name) => name !== "HISTFILE" && name !== "GIT_CONFIG_NOSYSTEM")).toEqual([]);
	});
});
