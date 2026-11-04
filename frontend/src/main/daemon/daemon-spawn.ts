/* Split out of daemon-start.ts (B297, P53). */
import { spawn, type ChildProcess } from "node:child_process";
import { closeSync, openSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { HOME_DIR_NAME } from "../../shared/brand";
import type { DaemonLaunchSpec } from "../../shared/daemon-launch";
import { daemonEnv } from "./daemon-lifecycle";

/**
 * Spawn the daemon detached (own process group). A keep-alive daemon cannot
 * inherit Electron-owned pipes, so its stdio goes to ~/.tallpine/daemon.log.
 */
export function spawnDaemonProcess(launch: DaemonLaunchSpec, keep: boolean): ChildProcess {
	let keepDaemonLogFd: number | undefined;
	let stdio: "pipe" | "ignore" | ["pipe", number | "ignore", number | "ignore"] = "pipe";
	if (keep) {
		const logPath = path.join(os.homedir(), HOME_DIR_NAME, "daemon.log");
		try {
			keepDaemonLogFd = openSync(logPath, "a");
			stdio = ["pipe", keepDaemonLogFd, keepDaemonLogFd];
		} catch {
			// Log redirect failed (e.g. ~/.tallpine not creatable, permission denied):
			// fall back to "ignore" so the daemon still runs, but warn — otherwise
			// a long-lived keep-alive daemon would run with zero log output.
			console.warn(`Tallpine: keep-daemon log redirect failed; daemon will run with stdio disabled: ${logPath}`);
			keepDaemonLogFd = undefined;
			stdio = ["pipe", "ignore", "ignore"];
		}
	}
	let child: ChildProcess;
	try {
		child = spawn(launch.command, launch.args, {
			cwd: launch.cwd,
			env: daemonEnv(keep),
			shell: launch.shell,
			detached: true,
			// Hide the daemon's console on a Windows GUI launch (no flashing terminal).
			windowsHide: true,
			stdio,
		});
	} catch (err) {
		// spawn can throw synchronously for invalid args; don't leak the log fd.
		if (keepDaemonLogFd !== undefined) {
			try {
				closeSync(keepDaemonLogFd);
			} catch {
				// best-effort — the throw below is the real error
			}
		}
		throw err;
	}
	// The child inherited its own copy of the log fd via stdio at spawn time;
	// close the parent's copy now so each keep-alive start/stop cycle does not
	// accumulate open descriptors until the process limit.
	if (keepDaemonLogFd !== undefined) {
		try {
			closeSync(keepDaemonLogFd);
		} catch {
			// best-effort — the child still holds its inherited copy
		}
		keepDaemonLogFd = undefined;
	}
	return child;
}
