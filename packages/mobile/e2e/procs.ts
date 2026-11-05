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

/** ps etime ([[dd-]hh:]mm:ss) to seconds. */
export function etimeSeconds(etime: string): number {
	const [days, rest] = etime.includes("-") ? etime.split("-") : ["0", etime];
	const parts = rest.split(":").map(Number);
	while (parts.length < 3) parts.unshift(0);
	return Number(days) * 86400 + parts[0] * 3600 + parts[1] * 60 + parts[2];
}

/** A hard-killed run (CI step timeout, SIGKILL) never reaches its teardown and
 * leaves detached chat hosts behind (B563). Orphans (PPID 1) whose command line
 * names an e2e scratch dir (tpe-bin-/tpe-run-) older than `minAgeSeconds` belong
 * to no live run, so the next run reaps them. `onlyContaining` narrows the
 * sweep (tests). Returns the pids it killed. */
export function reapStaleE2eProcesses(minAgeSeconds = 3600, onlyContaining = ""): number[] {
	const out = spawnSync("ps", ["-axo", "pid=,ppid=,etime=,command="], { encoding: "utf8", maxBuffer: 64 << 20 }).stdout ?? "";
	const reaped: number[] = [];
	for (const line of out.split("\n")) {
		const m = /^\s*(\d+)\s+(\d+)\s+(\S+)\s+(.*)$/.exec(line);
		if (!m || Number(m[2]) !== 1 || Number(m[1]) === process.pid) continue;
		if (!/\/tpe-(bin|run)-[^/\s]+\//.test(m[4]) || etimeSeconds(m[3]) < minAgeSeconds || !m[4].includes(onlyContaining)) continue;
		try { process.kill(Number(m[1]), "SIGKILL"); reaped.push(Number(m[1])); } catch { /* gone */ }
	}
	return reaped;
}
