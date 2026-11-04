import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import path from "node:path";

/** stable follows the newest full release; beta also takes prereleases. */
export type UpdateChannel = "stable" | "beta";

export interface UpdateSettings {
	/** Check for, download and stage new releases. On by default. */
	enabled: boolean;
	channel: UpdateChannel;
}

/** Live state of the updater, streamed to the renderer. */
export type UpdateState = "idle" | "checking" | "downloading" | "downloaded" | "installing" | "error" | "disabled";

/** Why automatic updates do not run on this install. */
export type UpdateDisabledReason = "unpackaged" | "unsupported-platform" | "not-a-release" | "unsigned" | "user" | "managed";

export interface UpdateStatus {
	state: UpdateState;
	/** Version being downloaded or staged. */
	version?: string;
	percent?: number;
	/** A staged update waits for the agents to go idle, then restarts. */
	restartWhenIdle?: boolean;
	/** Epoch ms of the last check that reached the feed. */
	checkedAt?: number;
	reason?: UpdateDisabledReason;
	/** Last failure, kept short; quiet failures (no feed, offline) never set it. */
	message?: string;
}

/** Settings plus what managed config (MDM) forces, for the Settings page. */
export interface UpdatePreferences {
	settings: UpdateSettings;
	/** True when an administrator decides auto-update; the controls are read-only. */
	locked: boolean;
	pinnedVersion?: string;
}

/** File holding the user's auto-update preferences under the ~/.tallpine state dir. */
export const UPDATE_SETTINGS_FILE_NAME = "update-settings.json";

export const DEFAULT_UPDATE_SETTINGS: UpdateSettings = { enabled: true, channel: "stable" };

let settingsOperationQueue: Promise<void> = Promise.resolve();

export function coerceUpdateSettings(raw: unknown): UpdateSettings {
	const o = (raw && typeof raw === "object" ? raw : {}) as Record<string, unknown>;
	return {
		// Only an explicit false turns updates off; a missing or odd value keeps the default.
		enabled: o.enabled !== false,
		channel: o.channel === "beta" ? "beta" : "stable",
	};
}

/** Read update settings, tolerating a missing or corrupt file (returns defaults). */
export async function readUpdateSettings(stateDir: string): Promise<UpdateSettings> {
	let raw: string;
	try {
		raw = await readFile(path.join(stateDir, UPDATE_SETTINGS_FILE_NAME), "utf8");
	} catch {
		return { ...DEFAULT_UPDATE_SETTINGS };
	}
	try {
		return coerceUpdateSettings(JSON.parse(raw));
	} catch {
		return { ...DEFAULT_UPDATE_SETTINGS };
	}
}

async function writeUnlocked(stateDir: string, settings: UpdateSettings): Promise<void> {
	await mkdir(stateDir, { recursive: true, mode: 0o750 });
	const file = path.join(stateDir, UPDATE_SETTINGS_FILE_NAME);
	const tmp = path.join(stateDir, `.update-settings-${process.pid}-${Date.now()}.json`);
	await writeFile(tmp, `${JSON.stringify(coerceUpdateSettings(settings), null, 2)}\n`, { mode: 0o600 });
	await rename(tmp, file);
}

/** Atomically and serially write update settings (temp file + rename). */
export function writeUpdateSettings(stateDir: string, settings: UpdateSettings): Promise<void> {
	const queued = settingsOperationQueue.then(() => writeUnlocked(stateDir, settings));
	settingsOperationQueue = queued.catch(() => undefined);
	return queued;
}
