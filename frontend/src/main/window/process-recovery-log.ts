import { appendFileSync, mkdirSync, renameSync, statSync } from "node:fs";
import path from "node:path";

export const PROCESS_RECOVERY_LOG_MAX_BYTES = 256 * 1024;

/** Bounded, timestamped file sink under <stateDir>/logs: a Finder-launched app keeps no stderr. */
export function createProcessRecoveryLog(stateDir: string, appVersion: string, sink: (m: string) => void = console.warn) {
	const directory = path.join(stateDir, "logs");
	const file = path.join(directory, "process-recovery.log");
	return (message: string): void => {
		sink(`Tallpine: ${message}`);
		try {
			const line = `${new Date().toISOString()} v${appVersion} ${message}\n`;
			mkdirSync(directory, { recursive: true, mode: 0o700 });
			try {
				if (statSync(file).size + Buffer.byteLength(line) > PROCESS_RECOVERY_LOG_MAX_BYTES) renameSync(file, `${file}.1`);
			} catch (error) {
				if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
			}
			appendFileSync(file, line, { mode: 0o600 });
		} catch {
			// Diagnostics must never break recovery (e.g. disk full).
		}
	};
}
