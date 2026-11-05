// Reads the data-dir lock story out of a spawned daemon's captured output (B568).
// A replacement daemon whose predecessor still holds <data dir>/daemon.lock logs
// that it is waiting (backend/internal/daemon/daemon.go lockDataDirWait), may
// wait for or kill a predecessor that is marked as stopping (datadir_reclaim.go,
// B564), and finally either acquires the lock or exits 75 with "refusing to
// start". The supervisor turns that into a status the renderer can explain.
import { PRODUCT_NAME } from "./brand";
import type { DaemonStatus } from "./daemon-status";

/** sysexits EX_TEMPFAIL: the daemon gave up waiting for the data-dir lock (backend/internal/cli/root.go). */
export const DAEMON_EXIT_DATADIR_LOCKED = 75;

export const DATADIR_LOCK_WAIT_PHASE = "datadir_lock_wait";

// The holder is a live daemon that never began to stop, not a predecessor on its way out.
const HOLDER_RUNNING_MARKER = "data dir holder has not started shutting down";
const WAITING_MARKERS = [
	"process to release the data dir",
	"data dir holder is still within its shutdown deadline",
	"data dir holder's stop deadline is too far off",
	HOLDER_RUNNING_MARKER,
];
const KILLING_MARKER = "data dir holder is hung past its shutdown deadline";
const ACQUIRED_MARKERS = ["data dir lock acquired", "data dir lock reclaimed"];
// datadirlock.ErrLocked's text; other "refusing to start" errors (daemon already
// running, session isolation) exit 1 and are not lock refusals.
const LOCKED_MARKER = "data dir is in use by another";
const REFUSED_MARKER = "refusing to start";
const MAX_DAEMON_ERROR_CHARS = 500;

export type DataDirLockProgress =
	| { phase: "none" }
	| { phase: "acquired" }
	| { phase: "waiting"; holderPid?: number; killingHolder: boolean }
	| { phase: "refused"; holderPid?: number; holderRunning: boolean; daemonError?: string };

/** The latest data-dir lock event in `output`; later lines win. */
export function scanDataDirLockOutput(output: string): DataDirLockProgress {
	let progress: DataDirLockProgress = { phase: "none" };
	let holderPid: number | undefined;
	let holderRunning = false;
	for (const line of output.split("\n")) {
		if (!isLockLine(line)) continue;
		holderPid = holderPidIn(line) ?? holderPid;
		holderRunning ||= line.includes(HOLDER_RUNNING_MARKER);
		if (line.includes(REFUSED_MARKER)) {
			progress = { phase: "refused", holderPid, holderRunning, daemonError: daemonErrorIn(line) };
		} else if (ACQUIRED_MARKERS.some((marker) => line.includes(marker))) {
			progress = { phase: "acquired" };
		} else if (line.includes(KILLING_MARKER)) {
			progress = { phase: "waiting", holderPid, killingHolder: true };
		} else if (WAITING_MARKERS.some((marker) => line.includes(marker))) {
			progress = { phase: "waiting", holderPid, killingHolder: false };
		}
	}
	return progress;
}

function isLockLine(line: string): boolean {
	return (
		(line.includes(LOCKED_MARKER) && line.includes(REFUSED_MARKER)) ||
		line.includes(KILLING_MARKER) ||
		ACQUIRED_MARKERS.some((marker) => line.includes(marker)) ||
		WAITING_MARKERS.some((marker) => line.includes(marker))
	);
}

function holderPidIn(line: string): number | undefined {
	const match = /\(pid (\d+)\)|holder_pid=(\d+)/.exec(line);
	const pid = match ? Number(match[1] ?? match[2]) : NaN;
	return Number.isSafeInteger(pid) && pid > 1 ? pid : undefined;
}

// A slog text line carries the error in err="…"; the CLI's final line is the bare error.
function daemonErrorIn(line: string): string | undefined {
	const trimmed = line.trim();
	if (!trimmed) return undefined;
	if (/^time=\S+ level=/.test(trimmed)) {
		const err = /\berr="((?:[^"\\]|\\.)*)"/.exec(trimmed)?.[1]?.replace(/\\(.)/g, "$1");
		const msg = /\bmsg="((?:[^"\\]|\\.)*)"/.exec(trimmed)?.[1]?.replace(/\\(.)/g, "$1");
		const text = err ?? msg;
		return text ? text.slice(0, MAX_DAEMON_ERROR_CHARS) : undefined;
	}
	return trimmed.slice(0, MAX_DAEMON_ERROR_CHARS);
}

/** The daemon's own last error: its final non-informational output line. */
export function lastDaemonErrorLine(output: string): string | undefined {
	const lines = output.split("\n").map((line) => line.trim()).filter(Boolean);
	for (let index = lines.length - 1; index >= 0; index -= 1) {
		const line = lines[index];
		if (/^time=\S+ level=(INFO|DEBUG)\b/.test(line)) continue;
		return daemonErrorIn(line);
	}
	return undefined;
}

export function isDataDirLockWaitStatus(status: DaemonStatus): boolean {
	return status.state === "starting" && status.phase === DATADIR_LOCK_WAIT_PHASE;
}

/** While the spawned daemon waits for its predecessor to release the data dir. */
export function dataDirLockWaitStatus(progress: Extract<DataDirLockProgress, { phase: "waiting" }>, output: string): DaemonStatus {
	const who = previousDaemon(progress.holderPid);
	return {
		state: "starting",
		phase: DATADIR_LOCK_WAIT_PHASE,
		message: progress.killingHolder
			? `${who} is stuck shutting down. ${PRODUCT_NAME} is stopping it so the new daemon can start.`
			: `Waiting for ${lowerFirst(who)} to finish shutting down.`,
		...(progress.holderPid ? { lockHolderPid: progress.holderPid } : {}),
		...(output.trim() ? { details: output.trim() } : {}),
	};
}

/** Whether an exited daemon was refused the data-dir lock: only that error exits 75 (backend/internal/cli/root.go). */
export function exitWasDataDirLocked(code: number | null, signal: string | null): boolean {
	return signal === null && code === DAEMON_EXIT_DATADIR_LOCKED;
}

/** After the daemon exited because another process still holds the data dir. */
export function dataDirLockedStatus(output: string, exitCode: number | null): DaemonStatus {
	const progress = scanDataDirLockOutput(output);
	const holderPid = progress.phase === "refused" || progress.phase === "waiting" ? progress.holderPid : undefined;
	const daemonError = (progress.phase === "refused" ? progress.daemonError : undefined) ?? lastDaemonErrorLine(output);
	return {
		state: "stopped",
		code: "datadir_locked",
		message: refusalMessage(holderPid, progress.phase === "refused" && progress.holderRunning),
		exitCode,
		signal: null,
		...(holderPid ? { lockHolderPid: holderPid } : {}),
		...(daemonError ? { daemonError } : {}),
		...(output.trim() ? { details: output.trim() } : {}),
	};
}

function refusalMessage(holderPid: number | undefined, holderRunning: boolean): string {
	if (!holderPid) return `Another ${PRODUCT_NAME} process is still using the data directory, so the daemon could not start.`;
	if (holderRunning) {
		return `Another ${PRODUCT_NAME} daemon (pid ${holderPid}) is still running on this data directory, so a new daemon could not start.`;
	}
	return `${previousDaemon(holderPid)} is still shutting down, so a new daemon could not start.`;
}

function previousDaemon(holderPid: number | undefined): string {
	return holderPid ? `The previous ${PRODUCT_NAME} daemon (pid ${holderPid})` : `The previous ${PRODUCT_NAME} daemon`;
}

function lowerFirst(text: string): string {
	return text.charAt(0).toLowerCase() + text.slice(1);
}

