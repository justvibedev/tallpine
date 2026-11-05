// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({ runFile: null as string | null }));
const startDaemon = vi.hoisted(() => vi.fn(async () => ({ state: "ready" })));
vi.mock("node:fs/promises", () => ({
	readFile: vi.fn(async () => {
		if (state.runFile === null) throw Object.assign(new Error("nope"), { code: "ENOENT" });
		return state.runFile;
	}),
}));
vi.mock("../app-context", () => ({ ctx: { browserQuitRequested: false, daemonProcess: null, daemonStatus: { state: "stopped" } } }));
vi.mock("./daemon-lifecycle", () => ({ runFilePath: () => "/run.json", startDaemon }));
vi.mock("../../shared/daemon-discovery", () => ({
	parseRunFile: (s: string) => (s ? { pid: Number(s) } : null),
}));

import { ctx } from "../app-context";
import {
	cancelDaemonAutoRestart,
	LOCK_RELEASE_POLL_MS,
	noteUnexpectedDaemonExit,
	processIsAlive,
	watchForDataDirLockRelease,
} from "./daemon-auto-restart-host";

const flush = async () => {
	for (let i = 0; i < 5; i++) await Promise.resolve();
};

describe("daemon auto-restart host", () => {
	beforeEach(() => {
		vi.useFakeTimers();
		startDaemon.mockClear();
		state.runFile = null;
		ctx.browserQuitRequested = false;
		cancelDaemonAutoRestart();
	});
	afterEach(() => vi.useRealTimers());

	it("respawns when the run-file still names the crashed child", async () => {
		state.runFile = "4242";
		noteUnexpectedDaemonExit(4242, 1, null);
		await flush();
		await vi.advanceTimersByTimeAsync(1_000);
		expect(startDaemon).toHaveBeenCalledWith({ autoRestart: true });
	});

	it("does not respawn after a graceful stop (run-file removed, no signal)", async () => {
		noteUnexpectedDaemonExit(4242, 0, null);
		await flush();
		await vi.advanceTimersByTimeAsync(60_000);
		expect(startDaemon).not.toHaveBeenCalled();
	});

	it("does not respawn when a successor owns the run-file", async () => {
		state.runFile = "999";
		noteUnexpectedDaemonExit(4242, 1, null);
		await flush();
		await vi.advanceTimersByTimeAsync(60_000);
		expect(startDaemon).not.toHaveBeenCalled();
	});

	it("stands down if a successor takes the run-file during the backoff", async () => {
		state.runFile = "4242";
		noteUnexpectedDaemonExit(4242, 1, null);
		await flush();
		state.runFile = "999";
		await vi.advanceTimersByTimeAsync(60_000);
		expect(startDaemon).not.toHaveBeenCalled();
	});

	it("explicit cancel drops a pending respawn", async () => {
		state.runFile = "4242";
		noteUnexpectedDaemonExit(4242, null, "SIGSEGV");
		await flush();
		cancelDaemonAutoRestart();
		await vi.advanceTimersByTimeAsync(60_000);
		expect(startDaemon).not.toHaveBeenCalled();
	});

	it("never respawns while the app is quitting", async () => {
		state.runFile = "4242";
		ctx.browserQuitRequested = true;
		noteUnexpectedDaemonExit(4242, 1, null);
		await flush();
		await vi.advanceTimersByTimeAsync(60_000);
		expect(startDaemon).not.toHaveBeenCalled();
	});
});

// B568: a start refused the data-dir lock (exit 75) recovers once the holder exits.
describe("data-dir lock release watch", () => {
	let holderAlive = true;
	const isAlive = vi.fn(() => holderAlive);
	beforeEach(() => {
		vi.useFakeTimers();
		startDaemon.mockClear();
		isAlive.mockClear();
		holderAlive = true;
		ctx.browserQuitRequested = false;
		(ctx as { daemonProcess: unknown }).daemonProcess = null;
		(ctx as { daemonStatus: { state: string } }).daemonStatus = { state: "stopped" };
		cancelDaemonAutoRestart();
	});
	afterEach(() => {
		cancelDaemonAutoRestart();
		vi.useRealTimers();
	});

	it("starts a daemon as soon as the holder has exited", async () => {
		watchForDataDirLockRelease(87127, isAlive);
		await vi.advanceTimersByTimeAsync(LOCK_RELEASE_POLL_MS * 5);
		expect(startDaemon).not.toHaveBeenCalled();
		expect(isAlive).toHaveBeenCalledWith(87127);

		holderAlive = false;
		await vi.advanceTimersByTimeAsync(LOCK_RELEASE_POLL_MS);
		expect(startDaemon).toHaveBeenCalledOnce();
		expect(startDaemon).toHaveBeenCalledWith();
		await vi.advanceTimersByTimeAsync(LOCK_RELEASE_POLL_MS * 5);
		expect(startDaemon).toHaveBeenCalledOnce();
	});

	it("stands down when an explicit restart or stop cancels it", async () => {
		watchForDataDirLockRelease(87127, isAlive);
		cancelDaemonAutoRestart();
		holderAlive = false;
		await vi.advanceTimersByTimeAsync(LOCK_RELEASE_POLL_MS * 5);
		expect(startDaemon).not.toHaveBeenCalled();
	});

	it("stands down once a daemon is running or attached", async () => {
		watchForDataDirLockRelease(87127, isAlive);
		(ctx as { daemonStatus: { state: string } }).daemonStatus = { state: "ready" };
		holderAlive = false;
		await vi.advanceTimersByTimeAsync(LOCK_RELEASE_POLL_MS * 5);
		expect(startDaemon).not.toHaveBeenCalled();
	});

	it("does not watch without a holder pid or while quitting", async () => {
		holderAlive = false;
		watchForDataDirLockRelease(undefined, isAlive);
		ctx.browserQuitRequested = true;
		watchForDataDirLockRelease(87127, isAlive);
		await vi.advanceTimersByTimeAsync(LOCK_RELEASE_POLL_MS * 5);
		expect(startDaemon).not.toHaveBeenCalled();
	});

	it("probes liveness with signal 0 and counts EPERM as alive", () => {
		expect(processIsAlive(1, () => true)).toBe(true);
		expect(processIsAlive(1, () => { throw Object.assign(new Error("perm"), { code: "EPERM" }); })).toBe(true);
		expect(processIsAlive(1, () => { throw Object.assign(new Error("gone"), { code: "ESRCH" }); })).toBe(false);
		expect(processIsAlive(process.pid)).toBe(true);
	});
});
