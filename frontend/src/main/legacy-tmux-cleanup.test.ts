import { mkdirSync, mkdtempSync, writeFileSync, existsSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { legacyStagedTmuxRoot, legacyStagedTmuxRootFor, removeLegacyStagedTmux } from "./legacy-tmux-cleanup";

function stage(home: string, rel: string[]): string {
	const file = path.join(home, ...rel, "tmux");
	mkdirSync(path.dirname(file), { recursive: true });
	writeFileSync(file, "x");
	return file;
}

describe("removeLegacyStagedTmux", () => {
	it("removes old version dirs but keeps the new copy and the in-use binary's dir", async () => {
		const home = mkdtempSync(path.join(os.tmpdir(), "tp-tmux-"));
		const root = legacyStagedTmuxRoot(home);
		const oldA = stage(home, [".tallpine", "runtime", "tmux", "1.0.0-darwin-arm64"]);
		const inUse = stage(home, [".tallpine", "runtime", "tmux", "1.1.0-darwin-arm64"]);
		const fresh = stage(home, [".tallpine", "data", "runtime", "tmux", "1.2.0-darwin-arm64"]);
		const removed = await removeLegacyStagedTmux(root, fresh, inUse);
		expect(removed).toEqual([path.dirname(oldA)]);
		expect(existsSync(oldA)).toBe(false);
		expect(existsSync(inUse)).toBe(true);
		expect(existsSync(fresh)).toBe(true);
	});

	it("removes everything and the empty runtime dirs when no daemon uses the old path", async () => {
		const home = mkdtempSync(path.join(os.tmpdir(), "tp-tmux-"));
		const root = legacyStagedTmuxRoot(home);
		stage(home, [".tallpine", "runtime", "tmux", "1.0.0-darwin-arm64"]);
		const fresh = stage(home, [".tallpine", "data", "runtime", "tmux", "1.2.0-darwin-arm64"]);
		await removeLegacyStagedTmux(root, fresh, null);
		expect(existsSync(root)).toBe(false);
		expect(existsSync(fresh)).toBe(true);
	});

	it("does nothing when the legacy root is the new root (override set to the state root)", async () => {
		const home = mkdtempSync(path.join(os.tmpdir(), "tp-tmux-"));
		const fresh = stage(home, [".tallpine", "runtime", "tmux", "1.2.0-darwin-arm64"]);
		expect(await removeLegacyStagedTmux(legacyStagedTmuxRoot(home), fresh)).toEqual([]);
		expect(existsSync(fresh)).toBe(true);
	});

	it("is a no-op when no legacy dir exists", async () => {
		const home = mkdtempSync(path.join(os.tmpdir(), "tp-tmux-"));
		expect(await removeLegacyStagedTmux(legacyStagedTmuxRoot(home), "/x/runtime/tmux/v/tmux")).toEqual([]);
	});

	it("has no legacy root when TALLPINE_DATA_DIR is set (staging never moved; the shared root is another install's)", () => {
		expect(legacyStagedTmuxRootFor({ TALLPINE_DATA_DIR: "/tmp/x" }, "/home/u")).toBeNull();
		expect(legacyStagedTmuxRootFor({ TALLPINE_DATA_DIR: "  " }, "/home/u")).toBe(path.join("/home/u", ".tallpine", "runtime", "tmux"));
		expect(legacyStagedTmuxRootFor({}, "/home/u")).toBe(path.join("/home/u", ".tallpine", "runtime", "tmux"));
	});
});
