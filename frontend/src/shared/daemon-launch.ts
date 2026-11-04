import { PRODUCT_NAME, HOME_DIR_NAME, brandEnv, daemonBinaryName } from "./brand";

export type DaemonLaunchSpec = {
	command: string;
	args: string[];
	cwd: string;
	shell: boolean;
	source: "configured" | "bundled" | "dev";
};

function joinPath(...segments: string[]): string {
	return segments.map((segment) => segment.replace(/[/\\]+$/, "")).join("/");
}

export function bundledDaemonBinaryName(platform: NodeJS.Platform): string {
	return daemonBinaryName(platform);
}

export function resolveDaemonLaunch(
	env: Record<string, string | undefined>,
	isPackaged: boolean,
	resourcesPath: string,
	appPath: string,
	homeDir: string,
	platform: NodeJS.Platform,
): DaemonLaunchSpec | null {
	// Dev-only override. A packaged build must not run a command taken from the
	// environment: a same-user process could plant it (launchctl setenv) and get
	// a child that inherits the app's TCC grants and the daemon token.
	const configuredCommand = isPackaged ? undefined : env[brandEnv("DAEMON_COMMAND")]?.trim();
	if (configuredCommand) {
		// No shell: split on whitespace into argv.
		const [command, ...args] = configuredCommand.split(/\s+/);
		return { command, args, cwd: appPath, shell: false, source: "configured" };
	}

	if (!isPackaged) {
		// Run the daemon predev already built rather than `go run`, so the daemon's
		// parent is Electron itself, as in the packaged app; the diagnostics view
		// counts the daemon's parent tree as the rest of Tallpine.
		return {
			command: env.TALLPINE_DEV_DAEMON_BINARY?.trim() || joinPath(appPath, "daemon", bundledDaemonBinaryName(platform)),
			args: ["daemon"],
			cwd: appPath,
			shell: false,
			source: "dev",
		};
	}

	return {
		command: joinPath(resourcesPath, "daemon", bundledDaemonBinaryName(platform)),
		args: ["daemon"],
		cwd: joinPath(homeDir, HOME_DIR_NAME),
		shell: false,
		source: "bundled",
	};
}

export type BundledDaemonProbe = {
	executablePath?: string;
	appImagePath?: string;
};

/**
 * Identity check for a bundled daemon. Under AppImage the executable path is a
 * random /tmp/.mount_* path regenerated on every launch, so identity is the
 * stable outer .AppImage file path the daemon reports (TALLPINE_APPIMAGE, echoed as
 * appImagePath) — compared against this process's own APPIMAGE. Outside
 * AppImage the packaged executable path is stable and compared directly.
 *
 * Returns an error message, or null when the probed daemon belongs to this
 * install. A probe that cannot prove its identity (missing field) fails closed.
 */
export function bundledDaemonIdentityError(
	probe: BundledDaemonProbe,
	expectedCommand: string,
	appImagePath: string | undefined,
	samePath: (a: string, b: string) => boolean,
): string | null {
	if (appImagePath) {
		if (!probe.appImagePath) {
			return `An older ${PRODUCT_NAME} daemon is already running, but it does not report its install identity. Stop it and restart this app.`;
		}
		if (!samePath(probe.appImagePath, appImagePath)) {
			return `Another ${PRODUCT_NAME} daemon is already running from ${probe.appImagePath}; expected ${appImagePath}. Stop the other daemon before using this app.`;
		}
		return null;
	}
	if (!probe.executablePath) {
		return `An older ${PRODUCT_NAME} daemon is already running, but it does not report its binary path. Stop it and restart this app.`;
	}
	if (!samePath(probe.executablePath, expectedCommand)) {
		return `Another ${PRODUCT_NAME} daemon is already running from ${probe.executablePath}; expected ${expectedCommand}. Stop the other daemon before using this app.`;
	}
	return null;
}
