/**
 * Single source of truth for the product identity of this fork (Tallpine). Anything that could collide with an official Agent
 * Orchestrator install on the same machine — bundle id, data root, daemon port,
 * CLI name, tmux socket, URL schemes, launchd labels — is defined here and read
 * by the rest of the Electron app. The backend mirrors these values in
 * backend/internal/brand/brand.go; keep the two files in sync (brand.test.ts
 * and backend/internal/brand/brand_test.go pin the values).
 *
 * Environment variables use the TALLPINE_ prefix; the legacy VO_ spelling is
 * aliased to it at startup (aliasLegacyEnv).
 *
 * This module must stay free of Node/Electron imports so the renderer can use it.
 */

/** Prefix of every environment variable this product reads (mirrors brand.EnvPrefix). */
export const ENV_PREFIX = "TALLPINE_";
/** Full env var name for a suffix, e.g. brandEnv("DAEMON_COMMAND"). */
export function brandEnv(suffix: string): string {
	return ENV_PREFIX + suffix;
}

export const PRODUCT_NAME = "Tallpine";
/** Canonical CLI binary name (`tp` alias). */
export const CLI_NAME = "tallpine";
/** Short alias linked next to CLI_NAME. */
export const CLI_ALIAS = "tp";
/** macOS bundle id, Windows AppUserModelID and Linux app id. */
export const BUNDLE_ID = "ee.justdev.tallpine";
/** Packaged executable name (no extension). */
export const EXECUTABLE_NAME = "tallpine";
/** Per-user state root directory name under the home directory. */
export const HOME_DIR_NAME = ".tallpine";
/** Default loopback daemon port (the official install uses 3001). */
export const DEFAULT_DAEMON_PORT = 3101;
/** Default loopback port for unpackaged dev daemons (the official dev app uses 3002). */
export const DEV_DAEMON_PORT = 3102;
/** Private tmux server socket (`tmux -L tallpine`). */
export const TMUX_SOCKET_NAME = "tallpine";
/** OS-registered URL scheme handled by the desktop app (tallpine://...). */
export const URL_SCHEME = "tallpine";
/** Renderer-only link scheme (tallpine://sessions/{project}/{session}). */
export const IN_APP_LINK_SCHEME = "tallpine";
/** launchd label prefix and the closed-lid wake helper label. */
export const LAUNCHD_LABEL_PREFIX = "ee.justdev.tallpine";
export const MOBILE_WAKE_LABEL = `${LAUNCHD_LABEL_PREFIX}.mobile-wake`;
/** Probe identity reported by the daemon's /healthz. */
export const DAEMON_SERVICE_NAME = "tallpine-daemon";
/** Windows named-pipe base name for the supervisor/watchdog pipe. */
export const SUPERVISE_PIPE_BASENAME = "tallpine-supervise";
/** Daemon binary file name for a platform. */
export function daemonBinaryName(platform: string): string {
	return platform === "win32" ? `${CLI_NAME}.exe` : CLI_NAME;
}

/** Legacy (pre-rename) identity, still recognised during the transition. */
export const LEGACY_HOME_DIR_NAME = ".vo";
/** Pre-rename CLI name (recognised in old process names and CLI links). */
export const LEGACY_CLI_NAME = "vo";
/** Pre-rename product name; its app bundle holds the old CLI. */
export const LEGACY_PRODUCT_NAME = "Vibe Orchestrator";
export const LEGACY_URL_SCHEME = "vo-app";
export const LEGACY_IN_APP_LINK_SCHEME = "vo";
/** Mobile app id and scheme (iOS + Android). */
export const MOBILE_BUNDLE_ID = `${BUNDLE_ID}.mobile`;
export const MOBILE_URL_SCHEME = "tallpine-mobile";
/** Store listings for the Tallpine mobile app; null until they exist. The
 *  desktop hides "Get the app" while both are null rather than pointing at the
 *  official AO apps, which cannot pair with Tallpine. */
export const MOBILE_IOS_STORE_URL: string | null = null;
export const MOBILE_ANDROID_STORE_URL: string | null = null;
