import { describe, expect, it, vi } from "vitest";

import { ARCHIVE_FAILED_TITLE, archiveSession } from "./archive-session";

describe("archiveSession", () => {
	it("reports success without alerting", async () => {
		const report = vi.fn();
		await expect(archiveSession(async () => {}, report)).resolves.toBe(true);
		expect(report).not.toHaveBeenCalled();
	});

	it("surfaces a refused kill instead of swallowing it", async () => {
		const report = vi.fn();
		await expect(archiveSession(async () => { throw new Error("worktree has uncommitted changes"); }, report)).resolves.toBe(false);
		expect(report).toHaveBeenCalledTimes(1);
		expect(report.mock.calls[0][0]).toBe(ARCHIVE_FAILED_TITLE);
		expect(typeof report.mock.calls[0][1]).toBe("string");
	});
});

describe("archive entry points", () => {
	it.each(["lib/worker-board-list.tsx", "lib/chat/useConversationActionsMenu.ts"])("%s routes archive through archiveSession", async (file) => {
		const { readFileSync } = await import("node:fs");
		const src = readFileSync(file, "utf8");
		expect(src).toContain("archiveSession(");
		expect(src).not.toMatch(/kill\([^)]*\)\.catch\(\(\) => \{\}\)/);
	});
});
