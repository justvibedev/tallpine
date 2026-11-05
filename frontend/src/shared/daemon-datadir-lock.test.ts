import { describe, expect, it } from "vitest";
import {
	dataDirLockedStatus,
	dataDirLockWaitStatus,
	exitWasDataDirLocked,
	isDataDirLockWaitStatus,
	lastDaemonErrorLine,
	scanDataDirLockOutput,
} from "./daemon-datadir-lock";

// Lines as the daemon writes them to stderr (slog text handler, backend/internal/daemon/logging.go)
// from lockDataDirWait (daemon.go) and reclaimFromStoppingHolder (datadir_reclaim.go).
const WAITING =
	'time=2026-11-05T11:48:00.000+02:00 level=WARN msg="waiting for another Tallpine process to release the data dir" err="data dir is in use by another Tallpine process: /Users/me/.tallpine/data (pid 87127)" wait=1m48s\n';
const STOPPING =
	'time=2026-11-05T11:49:48.000+02:00 level=WARN msg="data dir holder is still within its shutdown deadline; waiting for it" holder_pid=87127 wait=12s\n';
const KILLING =
	'time=2026-11-05T11:50:00.000+02:00 level=ERROR msg="data dir holder is hung past its shutdown deadline; killing it" holder_pid=87127 stop_by=2026-11-05T11:49:59+02:00\n';
const RECLAIMED = 'time=2026-11-05T11:50:01.000+02:00 level=INFO msg="data dir lock reclaimed from a hung predecessor" holder_pid=87127\n';
const ACQUIRED = 'time=2026-11-05T11:48:05.000+02:00 level=INFO msg="data dir lock acquired" waited=5s\n';
const DAEMON_START_REFUSED =
	'time=2026-11-05T11:49:48.000+02:00 level=INFO msg="daemon start" pid=9001 guarded=false err="data dir is in use by another Tallpine process: /Users/me/.tallpine/data (pid 87127); waited 1m48s for it to exit, refusing to start"\n';
const CLI_REFUSED =
	"data dir is in use by another Tallpine process: /Users/me/.tallpine/data (pid 87127); waited 1m48s for it to exit, refusing to start\n";

describe("scanDataDirLockOutput", () => {
	it("reports nothing for ordinary startup output", () => {
		expect(scanDataDirLockOutput('time=x level=INFO msg="daemon listening" addr=127.0.0.1:3101\n')).toEqual({ phase: "none" });
	});

	it("reads the holder pid from the wait line", () => {
		expect(scanDataDirLockOutput(WAITING)).toEqual({ phase: "waiting", holderPid: 87127, killingHolder: false });
	});

	it("follows the B564 reclaim of a stopping predecessor", () => {
		expect(scanDataDirLockOutput(WAITING + STOPPING)).toMatchObject({ phase: "waiting", killingHolder: false });
		expect(scanDataDirLockOutput(WAITING + STOPPING + KILLING)).toEqual({ phase: "waiting", holderPid: 87127, killingHolder: true });
		expect(scanDataDirLockOutput(WAITING + KILLING + RECLAIMED)).toEqual({ phase: "acquired" });
	});

	it("treats an acquired lock as the end of the wait", () => {
		expect(scanDataDirLockOutput(WAITING + ACQUIRED)).toEqual({ phase: "acquired" });
	});

	it("returns the daemon's own refusal error", () => {
		expect(scanDataDirLockOutput(WAITING + DAEMON_START_REFUSED + CLI_REFUSED)).toEqual({
			phase: "refused",
			holderPid: 87127,
			holderRunning: false,
			daemonError: CLI_REFUSED.trim(),
		});
		// Without the CLI line, the err= value of the slog line is the error.
		expect(scanDataDirLockOutput(WAITING + DAEMON_START_REFUSED)).toMatchObject({ daemonError: CLI_REFUSED.trim() });
	});
});

describe("other refusals are not lock refusals", () => {
	it("ignores refusing-to-start errors and pids that are not about the data-dir lock", () => {
		// backend/internal/daemon/wire_storage.go and config/session_isolation.go
		expect(scanDataDirLockOutput("daemon already running (pid 5, port 1); refusing to start\n")).toEqual({ phase: "none" });
		expect(scanDataDirLockOutput("refusing to start a daemon on the real ~/.tallpine from a session\n")).toEqual({ phase: "none" });
		expect(scanDataDirLockOutput('time=t level=INFO msg="spawned helper" detail="(pid 99)"\n' + WAITING)).toMatchObject({ holderPid: 87127 });
	});
});

describe("lastDaemonErrorLine", () => {
	it("skips trailing informational log lines", () => {
		expect(lastDaemonErrorLine(`listen tcp 127.0.0.1:3101: bind: address already in use\n${ACQUIRED}\n`)).toBe(
			"listen tcp 127.0.0.1:3101: bind: address already in use",
		);
	});

	it("uses err= from a warning or error log line", () => {
		expect(lastDaemonErrorLine('time=x level=ERROR msg="open store" err="disk I/O error: \\"ao.db\\""\n')).toBe('disk I/O error: "ao.db"');
	});

	it("is undefined without output", () => {
		expect(lastDaemonErrorLine("  \n")).toBeUndefined();
	});
});

describe("data-dir lock statuses", () => {
	it("names the previous daemon while waiting", () => {
		const status = dataDirLockWaitStatus({ phase: "waiting", holderPid: 87127, killingHolder: false }, WAITING);
		expect(status).toMatchObject({
			state: "starting",
			message: "Waiting for the previous Tallpine daemon (pid 87127) to finish shutting down.",
			lockHolderPid: 87127,
			details: WAITING.trim(),
		});
		expect(status.code).toBeUndefined();
		expect(isDataDirLockWaitStatus(status)).toBe(true);
		expect(isDataDirLockWaitStatus({ state: "starting" })).toBe(false);
	});

	it("says when the stuck predecessor is being stopped", () => {
		expect(dataDirLockWaitStatus({ phase: "waiting", holderPid: 87127, killingHolder: true }, "").message).toBe(
			"The previous Tallpine daemon (pid 87127) is stuck shutting down. Tallpine is stopping it so the new daemon can start.",
		);
	});

	it("explains a refusal with the pid and the daemon's error", () => {
		expect(dataDirLockedStatus(WAITING + CLI_REFUSED, 75)).toMatchObject({
			state: "stopped",
			code: "datadir_locked",
			message: "The previous Tallpine daemon (pid 87127) is still shutting down, so a new daemon could not start.",
			lockHolderPid: 87127,
			daemonError: CLI_REFUSED.trim(),
			exitCode: 75,
		});
	});

	it("does not call a holder that never began to stop a shutting-down predecessor", () => {
		const running = 'time=t level=WARN msg="data dir holder has not started shutting down; leaving it running" holder_pid=87127\n';
		expect(dataDirLockedStatus(WAITING + running + CLI_REFUSED, 75).message).toBe(
			"Another Tallpine daemon (pid 87127) is still running on this data directory, so a new daemon could not start.",
		);
	});

	it("still explains exit 75 when no output was captured", () => {
		const status = dataDirLockedStatus("", 75);
		expect(status.message).toBe("Another Tallpine process is still using the data directory, so the daemon could not start.");
		expect(status.lockHolderPid).toBeUndefined();
		expect(status.daemonError).toBeUndefined();
	});

	it("recognizes a lock refusal by exit code 75 alone", () => {
		expect(exitWasDataDirLocked(75, null)).toBe(true);
		expect(exitWasDataDirLocked(1, null)).toBe(false);
		expect(exitWasDataDirLocked(null, "SIGKILL")).toBe(false);
	});
});
