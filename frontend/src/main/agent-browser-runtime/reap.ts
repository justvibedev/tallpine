/** Terminate agent-browser daemons left behind under a runtime socket dir (B679). */

import { execFile } from "node:child_process";
import { readFile, readdir } from "node:fs/promises";
import path from "node:path";

export type ProcessCommandLookup = (pid: number) => Promise<string>;
export type ProcessSignal = (pid: number, signal: NodeJS.Signals) => void;

const MAX_DEPTH = 4;

export const defaultProcessCommand: ProcessCommandLookup = (pid) =>
	new Promise((resolve) => {
		execFile("ps", ["-p", String(pid), "-o", "command="], (error, stdout) => resolve(error ? "" : stdout));
	});

async function findPidFiles(dir: string, depth = 0): Promise<string[]> {
	let entries;
	try {
		entries = await readdir(dir, { withFileTypes: true });
	} catch {
		return [];
	}
	const found: string[] = [];
	for (const entry of entries) {
		const full = path.join(dir, entry.name);
		if (entry.isFile() && entry.name.endsWith(".pid")) found.push(full);
		else if (entry.isDirectory() && depth < MAX_DEPTH) found.push(...(await findPidFiles(full, depth + 1)));
	}
	return found;
}

/**
 * SIGTERM every live agent-browser daemon whose pid file sits under `dir`.
 * The pid is only signalled when its command line mentions agent-browser, so a
 * stale pid file whose pid was reused is never killed.
 */
export async function reapAgentBrowserDaemons(
	dir: string,
	log: (message: string) => void = () => undefined,
	options: { command?: ProcessCommandLookup; signal?: ProcessSignal; filter?: (pidFile: string) => boolean } = {},
): Promise<void> {
	if (process.platform === "win32") return;
	const command = options.command ?? defaultProcessCommand;
	const signal = options.signal ?? ((pid, sig) => process.kill(pid, sig));
	for (const pidFile of await findPidFiles(dir)) {
		if (options.filter && !options.filter(pidFile)) continue;
		let pid: number;
		try {
			pid = Number.parseInt((await readFile(pidFile, "utf8")).trim(), 10);
		} catch {
			continue;
		}
		if (!Number.isSafeInteger(pid) || pid <= 1 || pid === process.pid) continue;
		try {
			if (!(await command(pid)).includes("agent-browser")) continue;
			signal(pid, "SIGTERM");
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code !== "ESRCH") log(`agent-browser daemon reap failed for pid ${pid}: ${String(error)}`);
		}
	}
}

export type ProcessEntry = { pid: number; ppid: number; command: string };
export type SocketLister = (pid: number) => Promise<string[]>;

export const defaultListProcesses = (): Promise<ProcessEntry[]> =>
	new Promise((resolve) => {
		execFile("ps", ["-axo", "pid=,ppid=,command="], { maxBuffer: 16 * 1024 * 1024 }, (error, stdout) => {
			if (error) return resolve([]);
			const entries: ProcessEntry[] = [];
			for (const line of stdout.split("\n")) {
				const match = /^\s*(\d+)\s+(\d+)\s+(.*)$/.exec(line);
				if (match) entries.push({ pid: Number(match[1]), ppid: Number(match[2]), command: match[3] });
			}
			resolve(entries);
		});
	});

export const defaultListSockets: SocketLister = (pid) =>
	new Promise((resolve) => {
		execFile("lsof", ["-nP", "-a", "-U", "-p", String(pid), "-Fn"], (_error, stdout) => {
			resolve((stdout ?? "").split("\n").filter((l) => l.startsWith("n")).map((l) => l.slice(1)));
		});
	});

const BR_SOCKET_OWNER = /\/(?:tp|tallpine|ao)-br-(\d+)-[0-9a-f]{12}\//;

/**
 * Fallback for daemons with no readable pid file: SIGTERM processes that run our
 * bundled agent-browser, are reparented to init, and hold a socket under a
 * `tp-br-<ownerPid>-<hex>` alias whose owner pid is dead. A daemon with no such
 * socket, or whose owner is alive, is never touched.
 */
export async function reapOrphanedAgentBrowserProcesses(options: {
	binaryPath: string;
	processAlive: (pid: number) => boolean;
	log?: (message: string) => void;
	listProcesses?: () => Promise<ProcessEntry[]>;
	listSockets?: SocketLister;
	signal?: ProcessSignal;
}): Promise<void> {
	if (process.platform === "win32") return;
	const log = options.log ?? (() => undefined);
	const signal = options.signal ?? ((pid, sig) => process.kill(pid, sig));
	const listSockets = options.listSockets ?? defaultListSockets;
	for (const proc of await (options.listProcesses ?? defaultListProcesses)()) {
		if (proc.ppid !== 1 || proc.pid === process.pid) continue;
		if (proc.command !== options.binaryPath && !proc.command.startsWith(`${options.binaryPath} `)) continue;
		const owners = (await listSockets(proc.pid)).map((s) => BR_SOCKET_OWNER.exec(s)?.[1]).filter((o): o is string => !!o);
		if (owners.length === 0 || owners.some((o) => options.processAlive(Number(o)))) continue;
		try {
			signal(proc.pid, "SIGTERM");
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code !== "ESRCH") log(`agent-browser orphan reap failed for pid ${proc.pid}: ${String(error)}`);
		}
	}
}
