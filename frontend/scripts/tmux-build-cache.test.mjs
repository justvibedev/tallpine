// @vitest-environment node
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { buildCacheRoot, createManifest, deriveCacheKey, publishBundle, sha256File, sweepStaleDirs, verifyManifest } from "./tmux-build-cache.mjs";

const base = {
	platform: "darwin",
	arch: "arm64",
	sources: [{ name: "tmux", version: "3.8", sha256: "aa" }],
	scriptHash: "s1",
	toolchain: "clang-17;target=11.0",
};

describe("deriveCacheKey", () => {
	it("is stable and changes with every input", () => {
		const k = deriveCacheKey(base);
		expect(deriveCacheKey({ ...base })).toBe(k);
		expect(deriveCacheKey({ ...base, arch: "x64" })).not.toBe(k);
		expect(deriveCacheKey({ ...base, scriptHash: "s2" })).not.toBe(k);
		expect(deriveCacheKey({ ...base, toolchain: "clang-18;target=11.0" })).not.toBe(k);
		expect(deriveCacheKey({ ...base, sources: [{ name: "tmux", version: "3.9", sha256: "aa" }] })).not.toBe(k);
		expect(deriveCacheKey({ ...base, sources: [{ name: "tmux", version: "3.8", sha256: "bb" }] })).not.toBe(k);
	});
});

describe("buildCacheRoot", () => {
	it("defaults under ~/.tallpine and honours the override", () => {
		expect(buildCacheRoot({}, "/h")).toBe("/h/.tallpine/build-cache/bundled-tmux");
		expect(buildCacheRoot({ TALLPINE_BUILD_CACHE_DIR: "/x" }, "/h")).toBe("/x/bundled-tmux");
	});
});

describe("verifyManifest", () => {
	function stored(key = "k") {
		const dir = mkdtempSync(join(tmpdir(), "tmux-cache-"));
		mkdirSync(join(dir, "bin"));
		writeFileSync(join(dir, "bin", "tmux"), "binary");
		writeFileSync(join(dir, "manifest.json"), createManifest(key, { "bin/tmux": sha256File(join(dir, "bin", "tmux")) }));
		return dir;
	}
	it("accepts an intact entry", () => {
		expect(verifyManifest(stored(), "k", ["bin/tmux"])).toBe(true);
	});
	it("rejects a tampered cached file", () => {
		const dir = stored();
		writeFileSync(join(dir, "bin", "tmux"), "tampered");
		expect(verifyManifest(dir, "k", ["bin/tmux"])).toBe(false);
	});
	it("rejects a missing file, wrong key, missing manifest, or unlisted required file", () => {
		const dir = stored();
		expect(verifyManifest(dir, "other", ["bin/tmux"])).toBe(false);
		expect(verifyManifest(dir, "k", ["bin/tmux", "licenses/tmux.txt"])).toBe(false);
		expect(verifyManifest(join(dir, "nope"), "k", ["bin/tmux"])).toBe(false);
	});
});

describe("sweepStaleDirs", () => {
	it("removes work/staging dirs of dead pids only", () => {
		const root = mkdtempSync(join(tmpdir(), "tmux-sweep-"));
		for (const n of ["work-1", "staging-2", "work-3", "keep-4", "abc"]) mkdirSync(join(root, n));
		const removed = sweepStaleDirs(root, (pid) => pid === 3);
		expect(removed.sort()).toEqual(["staging-2", "work-1"]);
		expect(existsSync(join(root, "work-3"))).toBe(true);
		expect(existsSync(join(root, "keep-4"))).toBe(true);
	});
});

describe("publishBundle", () => {
	function entry(root, name, body) {
		const dir = join(root, name);
		mkdirSync(join(dir, "bin"), { recursive: true });
		writeFileSync(join(dir, "bin", "tmux"), body);
		writeFileSync(join(dir, "manifest.json"), createManifest("k", { "bin/tmux": sha256File(join(dir, "bin", "tmux")) }));
		return dir;
	}
	it("keeps a valid entry published by a concurrent build", () => {
		const root = mkdtempSync(join(tmpdir(), "tmux-pub-"));
		const winner = entry(root, "bundle", "winner");
		const staging = entry(root, "staging-9", "loser");
		expect(publishBundle(staging, winner, "k", ["bin/tmux"])).toBe("existing");
		expect(readFileSync(join(winner, "bin", "tmux"), "utf8")).toBe("winner");
		expect(existsSync(staging)).toBe(false);
	});
	it("replaces an invalid entry", () => {
		const root = mkdtempSync(join(tmpdir(), "tmux-pub-"));
		const bad = entry(root, "bundle", "x");
		writeFileSync(join(bad, "bin", "tmux"), "tampered");
		const staging = entry(root, "staging-9", "fresh");
		expect(publishBundle(staging, bad, "k", ["bin/tmux"])).toBe("published");
		expect(readFileSync(join(bad, "bin", "tmux"), "utf8")).toBe("fresh");
	});
});
