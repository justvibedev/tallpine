import type { Session } from "electron";

/**
 * Permissions the shell window itself uses, granted only to its main frame:
 *  - clipboard-sanitized-write: navigator.clipboard.writeText / write in the
 *    renderer (copy buttons, diff selection menu).
 *  - fullscreen: the terminal pane's requestFullscreen button.
 * Everything else (camera, mic, geolocation, notifications, clipboard-read,
 * midi, usb, ...) is denied. Subframes, including agent-authored render pages
 * and session-link previews, never get anything: Electron otherwise grants all.
 */
const SHELL_ALLOWED_PERMISSIONS: ReadonlySet<string> = new Set(["clipboard-sanitized-write", "fullscreen"]);

export function isShellPermissionAllowed(permission: string, isMainFrame: boolean | undefined): boolean {
	return isMainFrame === true && SHELL_ALLOWED_PERMISSIONS.has(permission);
}

export function installShellPermissionPolicy(target: Pick<Session, "setPermissionRequestHandler" | "setPermissionCheckHandler">): void {
	target.setPermissionCheckHandler((_contents, permission, _origin, details) =>
		isShellPermissionAllowed(permission, details?.isMainFrame),
	);
	target.setPermissionRequestHandler((_contents, permission, callback, details) =>
		callback(isShellPermissionAllowed(permission, details?.isMainFrame)),
	);
}
