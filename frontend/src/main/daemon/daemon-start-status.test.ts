// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import { SLOW_DAEMON_START_MESSAGE } from "../../shared/daemon-startup-status";
import { daemonExitStatus, dataDirLockProgressStatus } from "./daemon-start-status";

const WAITING =
	'time=t level=WARN msg="waiting for another Tallpine process to release the data dir" err="data dir is in use by another Tallpine process: /d (pid 4242)" wait=1m48s\n';
const ACQUIRED = 'time=t level=INFO msg="data dir lock acquired" waited=3s\n';
const REFUSED = "data dir is in use by another Tallpine process: /d (pid 4242); waited 1m48s for it to exit, refusing to start\n";
const slow = () => ({ state: "starting" as const, message: SLOW_DAEMON_START_MESSAGE });

describe("dataDirLockProgressStatus", () => {
	it("switches a starting daemon to the lock-wait status", () => {
		expect(dataDirLockProgressStatus({ state: "starting" }, WAITING, slow)).toMatchObject({
			state: "starting",
			phase: "datadir_lock_wait",
			lockHolderPid: 4242,
		});
	});

	it("replaces the generic slow-startup status while waiting", () => {
		expect(dataDirLockProgressStatus(slow(), WAITING, slow)?.phase).toBe("datadir_lock_wait");
	});

	it("does not republish an unchanged wait status", () => {
		const waiting = dataDirLockProgressStatus({ state: "starting" }, WAITING, slow)!;
		expect(dataDirLockProgressStatus(waiting, WAITING, slow)).toBeNull();
	});

	it("returns to the slow-startup status once the lock is acquired", () => {
		const waiting = dataDirLockProgressStatus({ state: "starting" }, WAITING, slow)!;
		const startupStatus = vi.fn(slow);
		expect(dataDirLockProgressStatus(waiting, WAITING + ACQUIRED, startupStatus)).toEqual(slow());
		expect(startupStatus).toHaveBeenCalledOnce();
	});

	it("keeps the wait status until the refused daemon exits", () => {
		const waiting = dataDirLockProgressStatus({ state: "starting" }, WAITING, slow)!;
		expect(dataDirLockProgressStatus(waiting, WAITING + REFUSED, slow)).toBeNull();
	});

	it("leaves ordinary and non-starting statuses alone", () => {
		expect(dataDirLockProgressStatus({ state: "starting" }, "hello\n", slow)).toBeNull();
		expect(dataDirLockProgressStatus({ state: "ready", port: 3101 }, WAITING, slow)).toBeNull();
	});
});

describe("daemonExitStatus", () => {
	it("reports a lock refusal (exit 75) as datadir_locked", () => {
		expect(daemonExitStatus(75, null, WAITING + REFUSED)).toMatchObject({
			code: "datadir_locked",
			lockHolderPid: 4242,
			daemonError: REFUSED.trim(),
		});
	});

	it("carries the daemon's own error on any other exit", () => {
		expect(daemonExitStatus(1, null, "listen tcp: bind: address already in use\n")).toEqual({
			state: "stopped",
			message: "Daemon exited with code 1",
			details: "listen tcp: bind: address already in use",
			code: "exited",
			exitCode: 1,
			signal: null,
			daemonError: "listen tcp: bind: address already in use",
		});
		// Another "refusing to start" error exits 1: a plain exit that still auto-restarts.
		expect(daemonExitStatus(1, null, "daemon already running (pid 5, port 1); refusing to start\n")).toMatchObject({
			code: "exited",
			daemonError: "daemon already running (pid 5, port 1); refusing to start",
		});
		expect(daemonExitStatus(1, null, "daemon already running (pid 5, port 1); refusing to start\n").lockHolderPid).toBeUndefined();
		expect(daemonExitStatus(null, "SIGKILL", "")).toMatchObject({ code: "exited", message: "Daemon exited with SIGKILL" });
	});
});
