/**
 * Where updates come from. The only outbound traffic of the updater is
 * github.com (the release feed and its assets); there is no other server and
 * no telemetry.
 */
export const UPDATE_REPO_OWNER = "justvibedev";
export const UPDATE_REPO_NAME = "tallpine";
declare const __TALLPINE_TEAM_ID__: string | undefined;

/**
 * Developer ID team every build must be signed by (Squirrel.Mac enforces the
 * match). Injected at build time from the signing identity (vite.main.config.ts);
 * empty for unsigned builds, where updates stay off.
 */
export const UPDATE_TEAM_ID = typeof __TALLPINE_TEAM_ID__ === "undefined" ? "" : __TALLPINE_TEAM_ID__;
/** Download cache directory name, under ~/.tallpine/caches. */
export const UPDATER_CACHE_DIR_NAME = "updater";

export interface FeedOptions {
	pinnedVersion?: string;
}

/**
 * The electron-updater config (app-update.yml) for this install. Without a pin
 * it is the GitHub provider (latest release, or newest prerelease on beta);
 * with one it reads latest-mac.yml from that exact release tag, still on
 * github.com.
 */
export function feedConfig({ pinnedVersion }: FeedOptions): Record<string, string> {
	if (pinnedVersion) {
		return {
			provider: "generic",
			url: `https://github.com/${UPDATE_REPO_OWNER}/${UPDATE_REPO_NAME}/releases/download/v${pinnedVersion}`,
			updaterCacheDirName: UPDATER_CACHE_DIR_NAME,
		};
	}
	return {
		provider: "github",
		owner: UPDATE_REPO_OWNER,
		repo: UPDATE_REPO_NAME,
		updaterCacheDirName: UPDATER_CACHE_DIR_NAME,
	};
}

/** Serialize a flat config as YAML (values are fixed identifiers and URLs). */
export function feedConfigYaml(config: Record<string, string>): string {
	return `${Object.entries(config).map(([key, value]) => `${key}: ${JSON.stringify(value)}`).join("\n")}\n`;
}
