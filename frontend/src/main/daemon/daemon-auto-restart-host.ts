// Supervisor side of daemon auto-restart (policy lives in daemon-auto-restart.ts).
import { readFile } from "node:fs/promises";
import { parseRunFile } from "../../shared/daemon-discovery";
import { ctx } from "../app-context";
import { daemonExitWasUngraceful, planDaemonAutoRestart } from "./daemon-auto-restart";
import { runFilePath, startDaemon } from "./daemon-lifecycle";

type RunFileState = { present: boolean; pid: number | null };
export type DaemonAutoRestartIntent = { generation: number; crashedPid: number; runFilePresent: boolean };

let exits: number[] = [];
let timer: ReturnType<typeof setTimeout> | null = null;
let lockReleaseTimer: ReturnType<typeof setInterval> | null = null;
let generation = 0;

export async function readRunFileState(): Promise<RunFileState> {
	const rfp = runFilePath();
	if (rfp === null) return { present: true, pid: null };
	try {
		return { present: true, pid: parseRunFile(await readFile(rfp, "utf8"))?.pid ?? null };
	} catch (error) {
		return { present: (error as NodeJS.ErrnoException).code !== "ENOENT", pid: null };
	}
}

function markerMatches(intent: DaemonAutoRestartIntent, runFile: RunFileState): boolean {
	return intent.runFilePresent ? runFile.present && runFile.pid === intent.crashedPid : !runFile.present;
}

export function isDaemonAutoRestartCurrent(intent: DaemonAutoRestartIntent): boolean {
	return intent.generation === generation && !ctx.browserQuitRequested;
}

// Cancel a pending respawn and forget the exit window. Called on explicit
// stop/restart/start and app quit so a stale respawn cannot race them.
export function cancelDaemonAutoRestart(): void {
	generation += 1;
	if (timer !== null) clearTimeout(timer);
	timer = null;
	exits = [];
	stopLockReleaseWatch();
}

/** How often a refused start checks whether the lock holder has exited. */
export const LOCK_RELEASE_POLL_MS = 1_000;

function stopLockReleaseWatch(): void {
	if (lockReleaseTimer !== null) clearInterval(lockReleaseTimer);
	lockReleaseTimer = null;
}

/** Whether `pid` still names a live process (EPERM means alive but not ours). */
export function processIsAlive(pid: number, kill: (pid: number, signal: 0) => unknown = process.kill): boolean {
	try {
		kill(pid, 0);
		return true;
	} catch (error) {
		return (error as NodeJS.ErrnoException).code === "EPERM";
	}
}

/**
 * After a daemon was refused the data-dir lock (exit 75), start a new one as
 * soon as the process holding the lock exits (B568). Without a holder PID there
 * is nothing to watch; the renderer's status poll still attaches to any daemon
 * that comes up, and the banner offers Retry. Cancelled with the auto-restart.
 */
export function watchForDataDirLockRelease(holderPid: number | undefined, isAlive: (pid: number) => boolean = processIsAlive): void {
	stopLockReleaseWatch();
	if (!holderPid || ctx.browserQuitRequested) return;
	const watchGeneration = generation;
	lockReleaseTimer = setInterval(() => {
		if (watchGeneration !== generation || ctx.browserQuitRequested || ctx.daemonProcess || ctx.daemonStatus.state === "ready") {
			stopLockReleaseWatch();
			return;
		}
		if (isAlive(holderPid)) return;
		stopLockReleaseWatch();
		void startDaemon().catch(() => undefined);
	}, LOCK_RELEASE_POLL_MS);
}

function schedule(crashedPid: number, runFilePresent: boolean): void {
	if (ctx.browserQuitRequested) return;
	if (timer !== null) clearTimeout(timer);
	const { plan, recentExits } = planDaemonAutoRestart(exits, Date.now());
	exits = recentExits;
	timer = null;
	if (plan.action === "give_up") return;
	const intent: DaemonAutoRestartIntent = { generation, crashedPid, runFilePresent };
	timer = setTimeout(() => {
		timer = null;
		if (!isDaemonAutoRestartCurrent(intent) || ctx.daemonProcess) return;
		void readRunFileState().then((runFile) => {
			if (!markerMatches(intent, runFile) || !isDaemonAutoRestartCurrent(intent) || ctx.daemonProcess) return;
			void startDaemon({ autoRestart: true }).catch(() => undefined);
		});
	}, plan.delayMs);
}

/** Call from the daemon child's unexpected-exit handler. */
export function noteUnexpectedDaemonExit(exitedPid: number | null, code: number | null, signal: string | null): void {
	const gen = generation;
	void readRunFileState().then((runFile) => {
		if (gen !== generation || ctx.browserQuitRequested || ctx.daemonProcess) return;
		if (exitedPid !== null && daemonExitWasUngraceful({ runFilePresent: runFile.present, runFilePid: runFile.pid, childPid: exitedPid, code, signal })) {
			schedule(exitedPid, runFile.present);
		}
	});
}
