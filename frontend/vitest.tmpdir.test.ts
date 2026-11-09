// @vitest-environment node
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readdirSync, rmSync, utimesSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useRunScopedTmpdir, useSessionTmpdir } from "./vitest.tmpdir";

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

describe("useRunScopedTmpdir", () => {
	const DAY_MS = 24 * 60 * 60 * 1000;
	function base() {
		const dir = mkdtempSync(path.join(tmpdir(), "scope"));
		bases.push(dir);
		return dir;
	}
	const bases: string[] = [];
	afterEach(() => {
		for (const dir of bases.splice(0)) rmSync(dir, { recursive: true, force: true });
	});

	it("gives the run its own TMPDIR and removes it, contents included, on exit", () => {
		const root = base();
		const env: NodeJS.ProcessEnv = { TMPDIR: root };
		let cleanup: (() => void) | undefined;
		useRunScopedTmpdir(env, (fn) => (cleanup = fn));
		const dir = env.TMPDIR as string;
		expect(path.dirname(dir)).toBe(root);
		expect(existsSync(dir)).toBe(true);
		// What a leaking test leaves behind.
		mkdirSync(path.join(dir, "tp-updater-abc"));
		writeFileSync(path.join(dir, "tp-updater-abc", "state.json"), "{}");
		cleanup?.();
		expect(readdirSync(root)).toEqual([]);
	});

	it("does not let a failing removal escape the exit handler", () => {
		const root = base();
		const env: NodeJS.ProcessEnv = { TMPDIR: root };
		let cleanup: (() => void) | undefined;
		useRunScopedTmpdir(env, (fn) => (cleanup = fn));
		const dir = env.TMPDIR as string;
		const child = path.join(dir, "locked");
		mkdirSync(child);
		writeFileSync(path.join(child, "file"), "x");
		chmodSync(child, 0o500);
		try {
			expect(() => cleanup?.()).not.toThrow();
		} finally {
			chmodSync(child, 0o700);
		}
	});

	it("uses the OS temp dir when TMPDIR is unset", () => {
		// This suite runs inside a run dir of its own, which the nesting guard
		// would refuse, so give os.tmpdir() a plain base.
		const root = base();
		vi.stubEnv("TMPDIR", root);
		try {
			const env: NodeJS.ProcessEnv = {};
			let cleanup: (() => void) | undefined;
			useRunScopedTmpdir(env, (fn) => (cleanup = fn));
			expect(path.dirname(env.TMPDIR as string)).toBe(root);
			cleanup?.();
			expect(existsSync(env.TMPDIR as string)).toBe(false);
		} finally {
			vi.unstubAllEnvs();
		}
	});

	it("sweeps only this runner's dirs from crashed runs, and only stale ones", () => {
		const root = base();
		const now = Date.now();
		const old = new Date(now - 2 * DAY_MS);
		for (const name of ["tp-vt-crashed", "tp-vt-concurrent", "someone-elses"]) mkdirSync(path.join(root, name));
		utimesSync(path.join(root, "tp-vt-crashed"), old, old);
		utimesSync(path.join(root, "someone-elses"), old, old);
		const env: NodeJS.ProcessEnv = { TMPDIR: root };
		useRunScopedTmpdir(env, () => {}, now);
		const left = readdirSync(root);
		expect(left).not.toContain("tp-vt-crashed");
		expect(left).toContain("tp-vt-concurrent");
		expect(left).toContain("someone-elses");
	});

	it("does not nest under a run dir", () => {
		const nested = path.join(base(), "tp-vt-outer");
		mkdirSync(nested);
		const env: NodeJS.ProcessEnv = { TMPDIR: nested };
		const onExit = vi.fn();
		useRunScopedTmpdir(env, onExit);
		expect(env.TMPDIR).toBe(nested);
		expect(onExit).not.toHaveBeenCalled();
		expect(readdirSync(nested)).toEqual([]);
	});
});
