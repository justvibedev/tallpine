/* Split out of daemon-start.ts (B297, P53). */
import { readFile, rm } from "node:fs/promises";
import { parseRunFile } from "../../shared/daemon-discovery";
import { portHolderPidToReplace } from "../../shared/daemon-takeover";
import { readProcessStartMs } from "../process-start";
import { resolvedDaemonPort, runFilePath } from "./daemon-lifecycle";
import { processAlive, readDaemonProbe } from "./daemon-probe";

/** Kill and replace a wedged daemon still holding the port. */
export async function reclaimOrphanedPortHolder(): Promise<void> {
	// Wedged-orphan kill+replace: both attach paths returned null, but a process
	// may still be holding the port: a hung daemon that bound it and then stopped
	// answering /healthz (e.g. one that deadlocked), or one that started answering
	// since the checks above. A run-file PID that is merely alive is not enough to
	// signal: after a crash or reboot the OS hands that PID to an unrelated
	// process, so portHolderPidToReplace names only a daemon it can identify. Two
	// cases are intentionally NOT handled: an identity-mismatched but healthy Tallpine
	// daemon is already surfaced as an error status upstream by
	// resolveDaemonFromPort (not killed here), and a foreign non-Tallpine process holding
	// the port is not replaced (out of scope). When no holder is identified, skip
	// straight to spawn (the new daemon overwrites a stale run-file itself); a live
	// process that is left alone is logged below.
	const orphanProbe = await readDaemonProbe(resolvedDaemonPort(), "healthz");
	const runFilePath_ = runFilePath();
	let runFile: ReturnType<typeof parseRunFile> = null;
	if (runFilePath_) {
		try {
			runFile = parseRunFile(await readFile(runFilePath_, "utf8"));
		} catch {
			// run-file absent or unreadable; proceed without a PID.
		}
	}
	const holder = await portHolderPidToReplace({
		probe: orphanProbe,
		runFile,
		processStartMs: readProcessStartMs,
	});
	// Declining to signal is otherwise silent, so say why a live process named by
	// running.json is left alone (a hung daemon that is not replaced shows up here).
	if (holder.pid === null && runFile && processAlive(runFile.pid)) {
		console.warn(`Tallpine: not stopping process ${runFile.pid} named by running.json: ${holder.reason}`);
	}
	const holderPid = holder.pid;
	if (holderPid !== null) {
		try {
			process.kill(-holderPid, "SIGTERM");
		} catch {
			try {
				process.kill(holderPid, "SIGTERM");
			} catch {
				// process already gone; proceed
			}
		}
		// Poll until the port is free (probe returns null) or 8 s elapses.
		const TAKEOVER_TIMEOUT_MS = 8_000;
		const TAKEOVER_POLL_MS = 200;
		const deadline = Date.now() + TAKEOVER_TIMEOUT_MS;
		while (Date.now() < deadline) {
			const still = await readDaemonProbe(resolvedDaemonPort(), "healthz");
			if (!still) break;
			await new Promise<void>((r) => setTimeout(r, TAKEOVER_POLL_MS));
		}
		// Remove the stale run-file so the new daemon can write a fresh one.
		if (runFilePath_) {
			await rm(runFilePath_, { force: true });
		}
	}
}
