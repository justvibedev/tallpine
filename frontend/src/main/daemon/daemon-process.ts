import type { ChildProcess } from "node:child_process";

/** SIGTERM the daemon's process group (reaches PTY children); fall back to the child itself. */
export function killDaemon(child: Pick<ChildProcess, "pid" | "kill">, killPid: (pid: number, signal: NodeJS.Signals) => void = process.kill): void {
	if (child.pid === undefined) return;
	try {
		killPid(-child.pid, "SIGTERM");
	} catch {
		child.kill("SIGTERM");
	}
}
