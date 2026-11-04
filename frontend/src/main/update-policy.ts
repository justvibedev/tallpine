import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { BUNDLE_ID } from "../shared/brand";
import managedKeys from "../shared/managed-keys.json";
import type { UpdateChannel } from "./update-settings";

/**
 * Auto-update keys an administrator sets in the managed preferences domain
 * (the same `ee.justdev.tallpine` profile that carries the agent policy; the
 * daemon reads only its `rules` and ignores these). See docs/agent-policy.md.
 */
export interface ManagedUpdatePolicy {
	/** AutoUpdate: false turns updates off for everyone; true forces them on. */
	enabled?: boolean;
	/** AutoUpdateChannel: "stable" or "beta". */
	channel?: UpdateChannel;
	/** AutoUpdatePinnedVersion: stay on (or move up to) exactly this release. */
	pinnedVersion?: string;
	/** The file exists but could not be parsed: updates stay off (fail closed). */
	invalid?: boolean;
}

export const MANAGED_PREFERENCES_PATH = `/Library/Managed Preferences/${BUNDLE_ID}.plist`;

/** Plist name of an Electron-owned managed key; the list is generated from the Go settings registry. */
function plistKey(key: string, fallback: string): string {
	// A missing entry must not stop the main process from loading; the drift test
	// in the daemon keeps the list complete, and the fallback is the shipped name.
	return managedKeys.find((k) => k.key === key)?.plistKey ?? fallback;
}

const KEY_ENABLED = plistKey("app.update.enabled", "AutoUpdate");
const KEY_CHANNEL = plistKey("app.update.channel", "AutoUpdateChannel");
const KEY_PINNED = plistKey("app.update.pinnedVersion", "AutoUpdatePinnedVersion");

const RELEASE_VERSION = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/;

/** Parse the JSON form of the managed plist. Unknown or wrong-typed keys are ignored. */
export function parseManagedUpdatePolicy(doc: unknown): ManagedUpdatePolicy {
	if (!doc || typeof doc !== "object" || Array.isArray(doc)) return { invalid: true };
	const o = doc as Record<string, unknown>;
	const policy: ManagedUpdatePolicy = {};
	if (typeof o[KEY_ENABLED] === "boolean") policy.enabled = o[KEY_ENABLED];
	const channel = o[KEY_CHANNEL];
	if (channel === "stable" || channel === "beta") policy.channel = channel;
	const pinned = o[KEY_PINNED];
	if (typeof pinned === "string") {
		const pin = pinned.trim().replace(/^v/, "");
		// A pin that is not a version cannot be honoured; fail closed rather than
		// silently tracking the newest release the admin meant to hold back.
		if (RELEASE_VERSION.test(pin)) policy.pinnedVersion = pin;
		else policy.enabled = false;
	}
	return policy;
}

/** True when any managed key decides auto-update, so the UI is read-only. */
export function isManagedUpdatePolicy(policy: ManagedUpdatePolicy): boolean {
	return policy.invalid === true || policy.enabled !== undefined || policy.channel !== undefined || policy.pinnedVersion !== undefined;
}

type ReadPlist = (file: string) => Promise<string>;

const plutilToJson: ReadPlist = async (file) =>
	(await promisify(execFile)("/usr/bin/plutil", ["-convert", "json", "-o", "-", file], { timeout: 5_000 })).stdout;

/**
 * Read the managed update policy. A missing file is no policy; a present one
 * that cannot be read or parsed turns updates off, like the daemon's
 * strictest-on-invalid rule for the agent policy.
 */
export async function readManagedUpdatePolicy(
	platform: NodeJS.Platform = process.platform,
	file = MANAGED_PREFERENCES_PATH,
	readPlist: ReadPlist = plutilToJson,
	exists: (file: string) => Promise<boolean> = defaultExists,
): Promise<ManagedUpdatePolicy> {
	if (platform !== "darwin") return {};
	if (!(await exists(file))) return {};
	try {
		return parseManagedUpdatePolicy(JSON.parse(await readPlist(file)));
	} catch {
		return { invalid: true };
	}
}

async function defaultExists(file: string): Promise<boolean> {
	const { access } = await import("node:fs/promises");
	try {
		await access(file);
		return true;
	} catch (error) {
		// Present but unreadable still counts as an admin's intent.
		return (error as NodeJS.ErrnoException).code !== "ENOENT";
	}
}
