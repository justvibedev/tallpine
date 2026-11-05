// Status the supervisor derives from a spawned daemon's output and exit (B568).
import {
	dataDirLockedStatus,
	dataDirLockWaitStatus,
	exitWasDataDirLocked,
	isDataDirLockWaitStatus,
	lastDaemonErrorLine,
	scanDataDirLockOutput,
} from "../../shared/daemon-datadir-lock";
import type { DaemonStatus } from "../../shared/daemon-status";

/**
 * The status a still-starting daemon should show after new output, or null to
 * keep the current one: the lock-wait status while it waits for its
 * predecessor, then the normal slow-startup status once it has the lock.
 */
export function dataDirLockProgressStatus(
	current: DaemonStatus,
	output: string,
	slowStartupStatus: () => DaemonStatus,
): DaemonStatus | null {
	if (current.state !== "starting") return null;
	const progress = scanDataDirLockOutput(output);
	if (progress.phase === "waiting") {
		const next = dataDirLockWaitStatus(progress, output);
		return next.message === current.message && next.details === current.details ? null : next;
	}
	// A refusal is reported by the exit handler; keep waiting until the exit lands.
	if (progress.phase === "refused") return null;
	return isDataDirLockWaitStatus(current) ? slowStartupStatus() : null;
}

/** The status for a daemon that exited without being asked to stop. */
export function daemonExitStatus(code: number | null, signal: string | null, output: string): DaemonStatus {
	if (exitWasDataDirLocked(code, signal)) return dataDirLockedStatus(output, code);
	const daemonError = lastDaemonErrorLine(output);
	return {
		state: "stopped",
		message: signal ? `Daemon exited with ${signal}` : `Daemon exited with code ${code ?? "unknown"}`,
		details: output.trim() || undefined,
		code: "exited",
		exitCode: code,
		signal,
		...(daemonError ? { daemonError } : {}),
	};
}
