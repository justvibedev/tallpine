import { readFileSync } from "node:fs";
import path from "node:path";
import { brandEnv, DEV_DAEMON_PORT, TMUX_SOCKET_NAME } from "../../shared/brand";
import { expectedDaemonPort } from "../../shared/daemon-attach";
import { buildDaemonEnv, devDaemonAllowedOrigins } from "../../shared/shell-env";

/** Everything daemonEnv needs from the Electron app, injected so it can be tested. */
export interface DaemonEnvInputs {
	env: NodeJS.ProcessEnv;
	platform: NodeJS.Platform;
	isDev: boolean;
	isPackaged: boolean;
	appPath: string;
	resourcesPath: string;
	/** The single resolved data dir (resolveDesktopDataDir). */
	dataDir: string;
	appRunId: string;
	forceKeep: boolean;
	stagedTmuxBinary: string | null;
	runFilePath: string | null;
	rendererUrl: string;
	cachedShellEnv: Record<string, string> | null;
	loginShell: Parameters<typeof buildDaemonEnv>[3];
}

export function buildDesktopDaemonEnv(i: DaemonEnvInputs): NodeJS.ProcessEnv {
	// TALLPINE_OWNER is the daemon's durable spawn-mode record: a keep-alive
	// daemon is "persistent" (never re-linked, survives app quit); a normal
	// app-owned daemon is "app"; headless `tallpine start` sets none.
	// TALLPINE_APP_RUN_ID scopes temporary command/auth terminals to this launch.
	const ownerTag = {
		TALLPINE_OWNER: i.forceKeep ? "persistent" : "app",
		TALLPINE_DATA_DIR: i.dataDir,
		TALLPINE_APP_RUN_ID: i.appRunId,
		...(i.platform === "darwin"
			? {
					TALLPINE_WAKE_HELPER: i.isPackaged
						? path.join(i.resourcesPath, "wake-helper", "tallpine-wake")
						: path.join(i.appPath, "wake-helper", "tallpine-wake"),
				}
			: {}),
		// The browser runtime token travels over the child's private stdin pipe;
		// never in the daemon environment, where a same-UID worker could read it.
		TALLPINE_BROWSER_RUNTIME_TOKEN: "",
		TALLPINE_BROWSER_RUNTIME_TOKEN_STDIN: "1",
		// Under AppImage, APPIMAGE is the stable outer file path; the daemon echoes
		// it so the identity check recognises its own daemon across a relaunch.
		...(i.env.APPIMAGE ? { TALLPINE_APPIMAGE: i.env.APPIMAGE } : {}),
		// Claude Code Chat uses Tallpine's packaged ACP adapter + Node runtime.
		TALLPINE_ACP_RUNTIME_DIR:
			i.env.TALLPINE_ACP_RUNTIME_DIR ??
			(i.isPackaged ? path.join(i.resourcesPath, "acp-runtime") : path.join(i.appPath, "resources", "acp-runtime")),
		...(i.stagedTmuxBinary
			? { TALLPINE_TMUX_BINARY: i.stagedTmuxBinary, TALLPINE_TMUX_SOCKET_NAME: TMUX_SOCKET_NAME }
			: {}),
		// Packaged macOS ships cloudflared at a fixed path (applied last: env cannot override it).
		...(i.isPackaged && i.platform === "darwin"
			? { [brandEnv("CLOUDFLARED_PATH")]: path.join(i.resourcesPath, "cloudflared", "cloudflared") }
			: {}),
	};
	// Dev isolation defaults; user-set env vars win. The data dir is already
	// resolved (dev subdir included) by resolveDesktopDataDir.
	const devExtras: Record<string, string> = {};
	if (i.isDev) {
		if (!i.env.TALLPINE_PORT) devExtras.TALLPINE_PORT = String(DEV_DAEMON_PORT);
		if (!i.env.TALLPINE_RUN_FILE) devExtras.TALLPINE_RUN_FILE = i.runFilePath ?? "";
		devExtras.TALLPINE_ALLOWED_ORIGINS = devDaemonAllowedOrigins(i.env.TALLPINE_ALLOWED_ORIGINS, i.rendererUrl);
	}
	// Windows keeps native environment semantics, overlaying the login-shell probe.
	if (i.platform === "win32") {
		return { ...i.env, ...(i.cachedShellEnv ?? {}), ...devExtras, ...ownerTag };
	}
	return buildDaemonEnv(i.env, i.cachedShellEnv, { ...devExtras, ...ownerTag }, i.loginShell);
}

export function resolveDaemonPort(isDev: boolean, env: NodeJS.ProcessEnv): number {
	return isDev && !env.TALLPINE_PORT ? DEV_DAEMON_PORT : expectedDaemonPort(env);
}

/** Environment the daemon binary is launched with; Windows dev reads the build manifest. */
export function daemonLaunchEnv(isDev: boolean, platform: NodeJS.Platform, appPath: string, env: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
	if (!isDev || platform !== "win32") return env;
	try {
		const daemonDir = path.resolve(appPath, "daemon");
		const parsed = JSON.parse(readFileSync(path.join(daemonDir, "dev-daemon.json"), "utf8")) as { path?: unknown };
		if (typeof parsed.path === "string") {
			const daemonPath = path.resolve(parsed.path.trim());
			const relativePath = path.relative(daemonDir, daemonPath);
			if (relativePath && !relativePath.startsWith("..") && !path.isAbsolute(relativePath)) {
				return { ...env, TALLPINE_DEV_DAEMON_BINARY: daemonPath };
			}
		}
	} catch {
		// Fall back to the legacy fixed dev path; build-daemon writes the manifest.
	}
	return env;
}
