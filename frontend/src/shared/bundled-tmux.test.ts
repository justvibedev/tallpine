import { describe, expect, it } from "vitest";
import { bundledTmuxBinaryPath, stableBundledTmuxBinaryPath } from "./bundled-tmux";

describe("bundledTmuxBinaryPath", () => {
	it.each(["darwin", "linux"] as const)("uses the packaged tmux on %s", (platform) => {
		expect(bundledTmuxBinaryPath(true, "/opt/tallpine/resources", platform)).toBe(
			"/opt/tallpine/resources/tmux/bin/tmux",
		);
	});

	it("does not override tmux in development", () => {
		expect(bundledTmuxBinaryPath(false, "/opt/tallpine/resources", "darwin")).toBeNull();
	});

	it("does not require tmux on Windows", () => {
		expect(bundledTmuxBinaryPath(true, "C:\\Tallpine\\resources", "win32")).toBeNull();
	});
});

describe("stableBundledTmuxBinaryPath", () => {
	it.each(["darwin", "linux"] as const)("uses durable versioned Tallpine storage on %s", (platform) => {
		expect(stableBundledTmuxBinaryPath(true, "/home/me/.tallpine", "0.10.3", platform, "arm64")).toBe(
			`/home/me/.tallpine/runtime/tmux/0.10.3-${platform}-arm64/tmux`,
		);
	});

	it("sanitizes version components rather than allowing path traversal", () => {
		expect(stableBundledTmuxBinaryPath(true, "/home/me/.tallpine", "../next build", "linux", "x64")).toBe(
			"/home/me/.tallpine/runtime/tmux/.._next_build-linux-x64/tmux",
		);
	});

	it("does not stage a Windows binary", () => {
		expect(stableBundledTmuxBinaryPath(true, "C:\\Tallpine", "0.10.3", "win32", "x64")).toBeNull();
	});
});
