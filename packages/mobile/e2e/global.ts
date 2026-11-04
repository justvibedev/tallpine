import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { buildDaemon } from "./harness";
import { killProcessesUnder, processesUnder } from "./procs";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** Builds the daemon once before any test file. Every scratch daemon dir lives
 * under one run dir; the teardown removes it and the binary, pass or fail. */
export default function setup(): () => Promise<void> {
	const bin = buildDaemon();
	const scratch = mkdtempSync(join(tmpdir(), "tpe-run-"));
	process.env.TALLPINE_E2E_DAEMON_BIN = bin;
	process.env.TALLPINE_E2E_SCRATCH = scratch;
	return async () => {
		delete process.env.TALLPINE_E2E_DAEMON_BIN;
		delete process.env.TALLPINE_E2E_SCRATCH;
		// Nothing a run started may outlive it (B330): chat hosts and dsh shims
		// are detached from the daemon, so check by their data dir.
		const survivors = processesUnder(scratch);
		rmSync(dirname(bin), { recursive: true, force: true });
		// Daemon helpers can write under their HOME for a moment after exit.
		rmSync(scratch, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
		await sleep(1000);
		rmSync(scratch, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
		if (survivors.length > 0) {
			await killProcessesUnder(scratch);
			throw new Error(`e2e left ${survivors.length} process(es) running:\n${survivors.map((p) => `${p.pid} ${p.command.slice(0, 160)}`).join("\n")}`);
		}
	};
}
