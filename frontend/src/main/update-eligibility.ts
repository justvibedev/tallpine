import { execFile } from "node:child_process";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { promisify } from "node:util";
import type { ManagedUpdatePolicy } from "./update-policy";
import { UPDATE_TEAM_ID } from "./update-feed";
import type { UpdateChannel, UpdateDisabledReason, UpdateSettings } from "./update-settings";

export interface UpdateEnvironment {
	packaged: boolean;
	platform: NodeJS.Platform;
	version: string;
	/** VOBuildDirty from Info.plist: "false" for a clean release build. */
	buildDirty: string | undefined;
	/** TallpineBuildKind from Info.plist: "local" for a non-public build; absent on releases. */
	buildKind?: string | undefined;
	/** TeamIdentifier of the running app's signature, null when ad hoc or unsigned. */
	teamId: string | null;
}

export type UpdatePlan =
	| { active: false; reason: UpdateDisabledReason }
	| { active: true; channel: UpdateChannel; pinnedVersion?: string };

const RELEASE_VERSION = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/;

/** Decide whether this install updates itself, and from which feed. */
export function planUpdates(env: UpdateEnvironment, settings: UpdateSettings, policy: ManagedUpdatePolicy): UpdatePlan {
	// Never on dev runs: there is nothing to replace.
	if (!env.packaged) return { active: false, reason: "unpackaged" };
	if (env.platform !== "darwin") return { active: false, reason: "unsupported-platform" };
	if (policy.invalid || policy.enabled === false) return { active: false, reason: "managed" };
	// Local or dirty builds are not releases; an update would replace them with
	// something unrelated to what the developer built.
	if (!RELEASE_VERSION.test(env.version) || env.buildDirty !== "false" || env.buildKind === "local") return { active: false, reason: "not-a-release" };
	// Squirrel.Mac accepts an update only if it satisfies the running app's
	// designated requirement. For a Developer ID build that pins the team; for
	// an ad hoc one it pins a hash no release matches. Require our team so the
	// check that stays on actually means "signed by us".
	if (!UPDATE_TEAM_ID || env.teamId !== UPDATE_TEAM_ID) return { active: false, reason: "unsigned" };
	if (policy.enabled !== true && !settings.enabled) return { active: false, reason: "user" };
	return {
		active: true,
		channel: policy.channel ?? settings.channel,
		...(policy.pinnedVersion ? { pinnedVersion: policy.pinnedVersion } : {}),
	};
}

/** The Contents dir of the running bundle (Resources' parent). */
function contentsDir(resourcesPath: string): string {
	return path.dirname(resourcesPath);
}

/** VOBuildDirty from the bundle's Info.plist ("true"/"false"), undefined when unstamped. */
export async function readBuildDirty(resourcesPath: string): Promise<string | undefined> {
	try {
		const plist = await readFile(path.join(contentsDir(resourcesPath), "Info.plist"), "utf8");
		return /<key>VOBuildDirty<\/key>\s*<string>([^<]*)<\/string>/.exec(plist)?.[1];
	} catch {
		return undefined;
	}
}

/** TallpineBuildKind from the bundle's Info.plist, undefined when unstamped. */
export async function readBuildKind(resourcesPath: string): Promise<string | undefined> {
	try {
		const plist = await readFile(path.join(contentsDir(resourcesPath), "Info.plist"), "utf8");
		return /<key>TallpineBuildKind<\/key>\s*<string>([^<]*)<\/string>/.exec(plist)?.[1];
	} catch {
		return undefined;
	}
}

/** TeamIdentifier of the running app's signature, or null. */
export async function readSigningTeam(bundlePath: string): Promise<string | null> {
	try {
		// codesign -dv writes its report to stderr.
		const { stderr } = await promisify(execFile)("/usr/bin/codesign", ["-dv", bundlePath], { timeout: 10_000 });
		const team = /^TeamIdentifier=(.+)$/m.exec(stderr)?.[1]?.trim();
		return team && team !== "not set" ? team : null;
	} catch {
		return null;
	}
}
