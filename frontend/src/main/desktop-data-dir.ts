import { mkdirSync, renameSync, writeFileSync } from "node:fs";
import path from "node:path";
import { HOME_DIR_NAME } from "../shared/brand";
import { stableBundledTmuxBinaryPath } from "../shared/bundled-tmux";

export function resolveDesktopDataDir(
	env: Record<string, string | undefined>,
	homeDir: string,
	launchWorkingDirectory: string,
	isPackaged: boolean,
): string {
	const configured = env.TALLPINE_DATA_DIR?.trim();
	if (configured) return path.resolve(launchWorkingDirectory, configured);
	return path.resolve(homeDir, HOME_DIR_NAME, isPackaged ? "data" : path.join("dev", "data"));
}

/** A state file kept directly under the data dir; the default root is ~/.tallpine, and a relative override resolves against the launch cwd like resolveDesktopDataDir. */
export function resolveDesktopStateFile(
	name: string,
	env: Record<string, string | undefined>,
	homeDir: string,
	launchWorkingDirectory: string,
): string {
	const configured = env.TALLPINE_DATA_DIR?.trim();
	const root = configured ? path.resolve(launchWorkingDirectory, configured) : path.join(homeDir, HOME_DIR_NAME);
	return path.join(root, name);
}

/**
 * Cursor Agent reads TERM_THEME at process start; the daemon applies it from
 * this file when spawning a PTY. Takes the resolved desktop data dir.
 */
export function persistTerminalThemeHint(dataDir: string, scheme: "light" | "dark"): void {
	try {
		mkdirSync(dataDir, { recursive: true, mode: 0o750 });
		const target = path.join(dataDir, "terminal-theme");
		const temporary = `${target}.${process.pid}.tmp`;
		writeFileSync(temporary, `${scheme}\n`, { mode: 0o600 });
		renameSync(temporary, target);
	} catch (error) {
		console.warn("Tallpine: unable to persist terminal theme hint", error);
	}
}

/** Where the bundled tmux is staged: always under the one resolved data dir. */
export function bundledTmuxStagingPath(
	dataDir: string,
	isPackaged: boolean,
	appVersion: string,
	platform: NodeJS.Platform,
	arch: string,
): string | null {
	return stableBundledTmuxBinaryPath(isPackaged, dataDir, appVersion, platform, arch);
}
