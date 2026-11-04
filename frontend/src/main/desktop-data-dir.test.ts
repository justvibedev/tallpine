import { mkdtempSync, readFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { bundledTmuxStagingPath, persistTerminalThemeHint, resolveDesktopDataDir, resolveDesktopStateFile } from "./desktop-data-dir";

const launchCwd = mkdtempSync(path.join(os.tmpdir(), "tp-launch-"));
const otherCwd = mkdtempSync(path.join(os.tmpdir(), "tp-other-"));

afterEach(() => vi.restoreAllMocks());

describe("one desktop data dir", () => {
	it.each([
		["unset", {}],
		["set", { TALLPINE_DATA_DIR: "" }],
	])("defaults under the home dir when TALLPINE_DATA_DIR is %s", (_n, env) => {
		expect(resolveDesktopDataDir(env, "/home/u", launchCwd, true)).toBe(path.resolve("/home/u/.tallpine/data"));
	});

	it("resolves a relative TALLPINE_DATA_DIR against the launch cwd, not the later cwd", () => {
		const dir = resolveDesktopDataDir({ TALLPINE_DATA_DIR: "rel/data" }, "/home/u", launchCwd, true);
		expect(dir).toBe(path.join(launchCwd, "rel/data"));
		vi.spyOn(process, "cwd").mockReturnValue(otherCwd);
		// the theme hint lands in the launch-cwd dir even after the cwd moved
		persistTerminalThemeHint(dir, "dark");
		expect(readFileSync(path.join(launchCwd, "rel/data/terminal-theme"), "utf8")).toBe("dark\n");
	});

	it("stages tmux under the same resolved dir for a relative override", () => {
		const dir = resolveDesktopDataDir({ TALLPINE_DATA_DIR: "rel/data" }, "/home/u", launchCwd, true);
		const staged = bundledTmuxStagingPath(dir, true, "1.2.3", "darwin", "arm64");
		expect(staged).toBe(path.join(launchCwd, "rel/data/runtime/tmux/1.2.3-darwin-arm64/tmux"));
		expect(path.isAbsolute(staged ?? "")).toBe(true);
	});

	it("does not stage tmux for an unpackaged build", () => {
		expect(bundledTmuxStagingPath("/d", false, "1", "darwin", "arm64")).toBeNull();
	});
});

describe("resolveDesktopStateFile", () => {
	it("defaults under ~/.tallpine and resolves a relative override against the launch cwd", () => {
		expect(resolveDesktopStateFile("remotes.json", {}, "/home/u", "/cwd")).toBe("/home/u/.tallpine/remotes.json");
		expect(resolveDesktopStateFile("remotes.json", { TALLPINE_DATA_DIR: "rel" }, "/home/u", "/cwd")).toBe(
			"/cwd/rel/remotes.json",
		);
		expect(resolveDesktopStateFile("remotes.json", { TALLPINE_DATA_DIR: "/abs" }, "/home/u", "/cwd")).toBe(
			"/abs/remotes.json",
		);
	});
});
