import { appendFileSync, chmodSync, mkdirSync, renameSync, statSync } from "node:fs";
import path from "node:path";

export const UPDATER_LOG_MAX_BYTES = 256 * 1024;

/** electron-updater's Logger shape. */
export interface UpdaterLogger {
	info(message?: unknown): void;
	warn(message?: unknown): void;
	error(message?: unknown): void;
	debug(message: string): void;
}

function describe(value: unknown): string {
	if (value instanceof Error) return value.stack || `${value.name}: ${value.message}`;
	if (typeof value === "string") return value;
	try {
		return JSON.stringify(value) ?? String(value);
	} catch {
		return String(value);
	}
}

/**
 * Persistent updater log at <stateDir>/logs/updater.log (one rotated .1 file).
 * Writing never throws: a full disk must not turn an update failure into a crash.
 */
export function createUpdaterLog(stateDir: string, write: (line: string) => void = () => undefined) {
	const directory = path.join(stateDir, "logs");
	const file = path.join(directory, "updater.log");
	const append = (level: string, message: unknown, error?: unknown) => {
		const text = error === undefined ? describe(message) : `${describe(message)}: ${describe(error)}`;
		const line = `${new Date().toISOString()} ${level} ${text}\n`;
		write(line);
		try {
			mkdirSync(directory, { recursive: true, mode: 0o700 });
			try {
				if (statSync(file).size + Buffer.byteLength(line) > UPDATER_LOG_MAX_BYTES) renameSync(file, `${file}.1`);
			} catch (e) {
				if ((e as NodeJS.ErrnoException).code !== "ENOENT") throw e;
			}
			appendFileSync(file, line, { mode: 0o600 });
			chmodSync(file, 0o600);
		} catch {
			// best effort
		}
	};
	const logger: UpdaterLogger = {
		info: (m) => append("INFO", m),
		warn: (m) => append("WARN", m),
		error: (m) => append("ERROR", m),
		debug: (m) => append("DEBUG", m),
	};
	return { file, logger, log: (message: string, error?: unknown) => append("ERROR", message, error) };
}
