/**
 * Finds the installed Google Chrome for Real Chrome mode (B849 q3): Stable,
 * else Beta, Dev, Canary at their standard install paths. Never downloads a
 * browser and never uses Chrome for Testing.
 */
import path from "node:path";

export type ChromeChannel = "stable" | "beta" | "dev" | "canary";

export type ChromeExecutable = { path: string; channel: ChromeChannel };

export const CHROME_CHANNELS: readonly ChromeChannel[] = ["stable", "beta", "dev", "canary"];

const MAC_APPS: Record<ChromeChannel, string> = {
	stable: "Google Chrome",
	beta: "Google Chrome Beta",
	dev: "Google Chrome Dev",
	canary: "Google Chrome Canary",
};

const WINDOWS_DIRS: Record<ChromeChannel, string> = {
	stable: "Chrome",
	beta: "Chrome Beta",
	dev: "Chrome Dev",
	canary: "Chrome SxS",
};

const LINUX_PATHS: Record<ChromeChannel, string[]> = {
	stable: ["/opt/google/chrome/chrome", "/usr/bin/google-chrome-stable", "/usr/bin/google-chrome"],
	beta: ["/opt/google/chrome-beta/chrome", "/usr/bin/google-chrome-beta"],
	dev: ["/opt/google/chrome-unstable/chrome", "/usr/bin/google-chrome-unstable"],
	canary: ["/opt/google/chrome-canary/chrome", "/usr/bin/google-chrome-canary"],
};

/** The standard install paths of one channel, most common first. */
export function chromeCandidatePaths(
	platform: NodeJS.Platform,
	channel: ChromeChannel,
	env: Record<string, string | undefined>,
): string[] {
	if (platform === "darwin") {
		const app = MAC_APPS[channel];
		const bundle = path.posix.join(`${app}.app`, "Contents", "MacOS", app);
		const home = env.HOME?.trim();
		return [path.posix.join("/Applications", bundle), ...(home ? [path.posix.join(home, "Applications", bundle)] : [])];
	}
	if (platform === "win32") {
		const roots = channel === "canary"
			? [envValue(env, "LOCALAPPDATA")]
			: [envValue(env, "PROGRAMFILES"), envValue(env, "PROGRAMFILES(X86)"), envValue(env, "LOCALAPPDATA")];
		return roots
			.map((root) => root?.trim())
			.filter((root): root is string => Boolean(root))
			.map((root) => path.win32.join(root, "Google", WINDOWS_DIRS[channel], "Application", "chrome.exe"));
	}
	return LINUX_PATHS[channel];
}

// Windows environment names are case-insensitive (ProgramFiles, PROGRAMFILES).
function envValue(env: Record<string, string | undefined>, name: string): string | undefined {
	const key = Object.keys(env).find((candidate) => candidate.toUpperCase() === name);
	return key === undefined ? undefined : env[key];
}

/** The first installed channel in Stable, Beta, Dev, Canary order, or null. */
export function findChromeExecutable(
	platform: NodeJS.Platform,
	env: Record<string, string | undefined>,
	exists: (file: string) => boolean,
): ChromeExecutable | null {
	for (const channel of CHROME_CHANNELS) {
		for (const candidate of chromeCandidatePaths(platform, channel, env)) {
			if (exists(candidate)) return { path: candidate, channel };
		}
	}
	return null;
}
