// @vitest-environment node
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { useSessionTmpdir } from "./vitest.tmpdir";

describe("useSessionTmpdir", () => {
	it("points TMPDIR at an existing CLAUDE_CODE_TMPDIR", () => {
		const dir = mkdtempSync(path.join(tmpdir(), "sess"));
		const env: NodeJS.ProcessEnv = { TMPDIR: "/tmp/claude-501", CLAUDE_CODE_TMPDIR: dir };
		useSessionTmpdir(env);
		expect(env.TMPDIR).toBe(dir);
	});
	it.each([undefined, "", "relative/dir", "/no/such/dir-b462"])("leaves TMPDIR alone for CLAUDE_CODE_TMPDIR=%j", (v) => {
		const env: NodeJS.ProcessEnv = { TMPDIR: "/tmp/x", CLAUDE_CODE_TMPDIR: v };
		useSessionTmpdir(env);
		expect(env.TMPDIR).toBe("/tmp/x");
	});
});
