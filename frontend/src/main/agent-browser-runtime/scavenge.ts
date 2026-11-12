/* Split out of agent-browser-runtime.ts (B297, P53). */

import { readFile, readdir, readlink, rm, stat } from "node:fs/promises";
import path from "node:path";
import { ProcessCommandLookup, ProcessSignal, reapAgentBrowserDaemons } from "./reap";
import { BROWSER_RUNTIME_RECLAIM_GRACE_MS, NativeProcessResult, RUNTIME_OWNER_FILE, RUNTIME_OWNER_MARKER, RUNTIME_ROOT_PATTERN, RuntimeOwner, SOCKET_ALIAS_PATTERN, STREAM_ALREADY_DISABLED_MESSAGE, defaultProcessAlive, isRecord, numberIsInteger, removePath } from "./common";

export async function scavengeBrowserRuntime(
	dataDir: string,
	processAlive: (pid: number) => boolean = defaultProcessAlive,
	log: (message: string) => void = () => undefined,
	reap: { command?: ProcessCommandLookup; signal?: ProcessSignal } = {},
): Promise<void> {
	let entries;
	try {
		entries = await readdir(dataDir, { withFileTypes: true });
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code === "ENOENT") return;
		log(`agent-browser runtime scan failed: ${String(error)}`);
		return;
	}

	const removals: Promise<void>[] = [];
	for (const entry of entries) {
		if (!entry.isDirectory()) continue;
		const match = RUNTIME_ROOT_PATTERN.exec(entry.name);
		if (!match) continue;
		const runDir = path.join(dataDir, entry.name);
		const ownerPath = path.join(runDir, RUNTIME_OWNER_FILE);
		let owner: RuntimeOwner;
		try {
			owner = JSON.parse(await readFile(ownerPath, "utf8")) as RuntimeOwner;
		} catch {
			// An unmarked directory may belong to another process that is between
			// mkdir and owner-marker creation. Preserve it rather than guessing.
			continue;
		}
		if (!validRuntimeOwner(owner, match[1])) continue;
		if (processAlive(owner.pid)) continue;
		try {
			const ownerStat = await stat(ownerPath);
			if (Date.now() - ownerStat.mtimeMs < BROWSER_RUNTIME_RECLAIM_GRACE_MS) continue;
		} catch (error) {
			log(`agent-browser runtime scan skipped ${entry.name}: ${String(error)}`);
			continue;
		}
		// The owner died without closing its native daemons; they outlive it as PPID 1.
		removals.push(
			reapAgentBrowserDaemons(path.join(runDir, "s"), log, reap).then(() => removePath(runDir, log, `stale runtime root ${entry.name}`)),
		);
	}
	await Promise.all(removals);
}

export function streamAlreadyDisabled(result: NativeProcessResult): boolean {
	return result.stderr.includes(STREAM_ALREADY_DISABLED_MESSAGE) || result.stdout.includes(STREAM_ALREADY_DISABLED_MESSAGE);
}

/** Remove only confirmed-dead frontend aliases owned by this Tallpine data root. */
export async function scavengeBrowserSocketAliases(
	dataDir: string,
	processAlive: (pid: number) => boolean = defaultProcessAlive,
	log: (message: string) => void = () => undefined,
	aliasRoot = "/tmp",
): Promise<void> {
	let entries;
	try {
		entries = await readdir(aliasRoot, { withFileTypes: true });
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code !== "ENOENT") {
			log(`agent-browser socket alias scan failed: ${String(error)}`);
		}
		return;
	}
	const resolvedDataDir = path.resolve(dataDir);
	for (const entry of entries) {
		const match = SOCKET_ALIAS_PATTERN.exec(entry.name);
		if (!match || !entry.isSymbolicLink()) continue;
		const pid = Number(match[1]);
		if (!Number.isSafeInteger(pid) || pid <= 0 || processAlive(pid)) continue;
		const aliasPath = path.join(aliasRoot, entry.name);
		try {
			const target = await readlink(aliasPath);
			const resolvedTarget = path.resolve(aliasRoot, target);
			const relative = path.relative(resolvedDataDir, resolvedTarget);
			const parts = relative.split(path.sep);
			if (
				relative.startsWith(`..${path.sep}`) ||
				path.isAbsolute(relative) ||
				parts.length !== 2 ||
				!/^r-[0-9a-f]{10}$/.test(parts[0]) ||
				parts[1] !== "s"
			) {
				continue;
			}
			await rm(aliasPath);
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code !== "ENOENT") {
				log(`agent-browser socket alias cleanup failed for ${entry.name}: ${String(error)}`);
			}
		}
	}
}

export function validRuntimeOwner(value: unknown, expectedPid?: string): value is RuntimeOwner {
	if (!isRecord(value)) return false;
	return (
		value.marker === RUNTIME_OWNER_MARKER &&
		numberIsInteger(value.pid) &&
		(!expectedPid || String(value.pid) === expectedPid) &&
		typeof value.startedAt === "string" &&
		/^[0-9a-f]{32}$/.test(String(value.token))
	);
}
