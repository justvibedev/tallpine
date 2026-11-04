import { spawnSync } from "node:child_process";
import { realpathSync } from "node:fs";

/** Processes whose command line mentions `dir` (macOS /tmp and /private/tmp
 * spellings both count). Chat hosts and their ACP shims are detached
 * (setsid) from the daemon, so a process-group kill misses them; their command
 * line always carries the scratch data dir, which is how a run finds them. */
export function processesUnder(dir: string): { pid: number; command: string }[] {
	const needles = new Set([dir]);
	try { needles.add(realpathSync(dir)); } catch { /* already removed */ }
	const out = spawnSync("ps", ["-axo", "pid=,command="], { encoding: "utf8", maxBuffer: 64 << 20 }).stdout ?? "";
	const found: { pid: number; command: string }[] = [];
	for (const line of out.split("\n")) {
		const m = /^\s*(\d+)\s+(.*)$/.exec(line);
		if (!m) continue;
		const pid = Number(m[1]);
		if (pid === process.pid) continue;
		if ([...needles].some((n) => m[2].includes(n))) found.push({ pid, command: m[2] });
	}
	return found;
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** SIGTERM then SIGKILL every process under `dir`; returns what it had to kill. */
export async function killProcessesUnder(dir: string): Promise<{ pid: number; command: string }[]> {
	const killed = processesUnder(dir);
	for (const sig of ["SIGTERM", "SIGKILL"] as const) {
		for (const p of processesUnder(dir)) { try { process.kill(p.pid, sig); } catch { /* gone */ } }
		for (let i = 0; i < 30 && processesUnder(dir).length > 0 && sig === "SIGTERM"; i++) await sleep(100);
	}
	return killed;
}
