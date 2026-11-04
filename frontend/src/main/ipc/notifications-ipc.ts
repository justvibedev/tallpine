/* Split out of main.ts (B297, P52). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import {
app,
Notification as ElectronNotification,
nativeImage,
shell
} from "electron";
import {
TRAY_RENDERER_READY_CHANNEL,
TRAY_SET_ATTENTION_STATE_CHANNEL,
} from "../../shared/tray";
import { ctx } from "../app-context";
import { getShellWebContents, ipcMain, OPEN_FOLDER_PATH_CHANNEL, trayLifecycle } from "../app-runtime-state";
import {
dockBounceType,
shouldReplaceBounce,
shouldSignalAttention,
shouldToast,
toastSilent,
} from "../notification-signals";
import { windowIconPath } from "../window/renderer-assets";



// Plays the bundled notification sound through the renderer (main has no
// audio output). `shell.beep()` is only the fallback for when no shell is
// alive to play it: on Linux it is a silent no-op for desktop-launched apps
// (Electron writes `\a` to /dev/console or /dev/tty, neither of which such an
// app can open), which is why the renderer owns playback (#5514).
export function playNotificationSound(): void {
	const shellContents = getShellWebContents();
	if (shellContents && !shellContents.isDestroyed()) {
		shellContents.send("notifications:playSound");
		return;
	}
	shell.beep();
}


export function cancelDockBounce(): void {
	if (ctx.pendingBounce === null) return;
	const { id } = ctx.pendingBounce;
	ctx.pendingBounce = null;
	app.dock?.cancelBounce(id);
}

export function registerNotificationsIpc(): void {


	ipcMain.handle(
		"notifications:show",
		(_event, notification: { id: string; title: string; body?: string; type?: string; urgency?: string; watched?: boolean }) => {
			if (!notification.id || !ctx.mainWindow) return;
			// "Already looking" = the window has focus, or the renderer reports the
			// prompt itself is on screen (`watched`). Visual signals are skipped then.
			const looking = ctx.mainWindow.isFocused() || notification.watched === true;
			// On Linux the sound ignores that guess: Wayland gives apps no reliable
			// visibility, so a window parked on another workspace still reports
			// focused/visible and would otherwise stay silent. macOS and Windows
			// report focus accurately and keep the quieter behaviour.
			const playsSound =
				shouldSignalAttention(notification.urgency) &&
				ctx.soundNotificationsEnabled &&
				(process.platform === "linux" || !looking);
			if (playsSound) playNotificationSound();
			if (looking) return;
			// OS toast: a native banner the user can click to jump straight back to the
			// session. Fires for every backend notification type (see shouldToast), so a
			// new type in notification.go never silently loses its toast.
			if (shouldToast(notification, ElectronNotification.isSupported())) {
				const toast = new ElectronNotification({
					title: notification.title,
					body: notification.body,
					// Mute the OS chime when our sound replaces it or the user turned
					// sound notifications off. Honoured on macOS/Windows only; Linux
					// notification daemons ignore it (see toastSilent).
					silent: toastSilent(process.platform, ctx.soundNotificationsEnabled, playsSound),
					// Tallpine logo as the notification icon on Windows/Linux. Omitted on macOS,
					// where a custom icon renders only as a redundant right-side content image —
					// macOS uses the app-bundle icon (the Tallpine logo in a packaged build) as the
					// single main icon.
					icon: process.platform === "darwin" ? undefined : windowIconPath(),
				});
				toast.on("click", () => {
					if (!ctx.mainWindow) return;
					if (ctx.mainWindow.isMinimized()) ctx.mainWindow.restore();
					ctx.mainWindow.show();
					ctx.mainWindow.focus();
					getShellWebContents()?.send("notifications:click", notification.id);
				});
				toast.show();
			}

			// Dock (macOS) / taskbar (Windows/Linux) attention signal. On macOS every
			// notification bounces the dock — urgency is carried by the bounce type,
			// so a merged PR bounces once while a blocked agent keeps bouncing. On
			// Windows/Linux only the actionable types flash (see
			// shouldSignalAttention), preserving the pre-existing behavior there.
			if (process.platform === "darwin" && app.dock) {
				// A pending critical bounce (agent blocked on the user) is never
				// replaced: a later informational notification must not downgrade it
				// to a one-shot bounce.
				if (shouldReplaceBounce(ctx.pendingBounce)) {
					// A focus listener from an earlier un-cancelled bounce still works for
					// the new id, so only attach one at a time.
					const hadPendingBounce = ctx.pendingBounce !== null;
					cancelDockBounce();
					const bounceType = dockBounceType(notification.urgency);
					const id = app.dock.bounce(bounceType);
					if (typeof id === "number" && id >= 0) {
						ctx.pendingBounce = { id, critical: bounceType === "critical" };
						if (!hadPendingBounce) ctx.mainWindow.once("focus", cancelDockBounce);
					}
				}
			} else if ((process.platform === "win32" || process.platform === "linux") && shouldSignalAttention(notification.urgency)) {
				if (!ctx.isFlashing) {
					ctx.isFlashing = true;
					ctx.mainWindow.flashFrame(true);
					ctx.mainWindow.once("focus", () => {
						ctx.isFlashing = false;
						ctx.mainWindow?.flashFrame(false);
					});
				}
			}
		},
	);


	// The renderer could not decode or start the bundled sound. Beep so the
	// notification still makes a sound where the OS beep works at all.
	ipcMain.on("notifications:soundFailed", (event) => {
		if (event.sender !== getShellWebContents()) return;
		shell.beep();
	});


	// Dev-only: force attention signal regardless of window focus (for testing)
	if (!app.isPackaged) {
		ipcMain.handle("notifications:devBounce", () => {
			if (!ctx.mainWindow) return;
			if (process.platform === "darwin") {
				const id = app.dock?.bounce("critical");
				setTimeout(() => {
					if (id !== undefined) app.dock?.cancelBounce(id);
				}, 2000);
			} else if (process.platform === "win32" || process.platform === "linux") {
				ctx.mainWindow.flashFrame(true);
				setTimeout(() => {
					ctx.mainWindow?.flashFrame(false);
				}, 2000);
			}
			if (ctx.soundNotificationsEnabled) {
				playNotificationSound();
			}
		});
	}


	ipcMain.handle("notifications:setBadge", (_event, count: number) => {
		if (!ctx.mainWindow) return { error: "no mainWindow" };
		const n = Number.isFinite(count) && count > 0 ? Math.floor(count) : 0;
		if (process.platform === "darwin") {
			const dock = app.dock;
			if (!dock) return { error: "no app.dock" };
			try {
				dock.setBadge(n > 0 ? String(n) : "");
			} catch (e) {
				return { error: String(e) };
			}
		} else if (process.platform === "win32") {
			if (n > 0) {
				// Pre-built red dot PNG overlay — indicates unread without needing Canvas.
				const badgeDataUrl =
					"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAAAN0lEQVR42mNgoAW4o6b2HxumSDNRhhDSjNcQYjVjNYRUzRiGjBpABQMojkaqJCSqJGWqZCZSAQASKE/UCaPI8AAAAABJRU5ErkJggg==";
				const icon = nativeImage.createFromDataURL(badgeDataUrl);
				ctx.mainWindow.setOverlayIcon(icon, `${n} unread`);
			} else {
				ctx.mainWindow.setOverlayIcon(null, "");
			}
		} else if (process.platform === "linux") {
			// setBadgeCount drives a numeric launcher badge on Unity only, and only
			// when the app ships a matching .desktop entry. Other Linux launchers
			// (KDE, GNOME) expose no equivalent API, so the call is a no-op there.
			// Surface the boolean result so the renderer can tell the badge was not
			// applied instead of assuming unconditional success.
			return { ok: app.setBadgeCount(n) };
		}
		return { ok: true };
	});


	ipcMain.on(TRAY_SET_ATTENTION_STATE_CHANNEL, (event, state) => trayLifecycle.handleSetAttentionState(event, state));


	ipcMain.on(TRAY_RENDERER_READY_CHANNEL, (event) => {
		trayLifecycle.handleRendererReady(event);

		if (ctx.pendingFolderPath && event.sender === getShellWebContents()) {
			event.sender.send(OPEN_FOLDER_PATH_CHANNEL, ctx.pendingFolderPath);
			ctx.pendingFolderPath = null;
		}
	});
}
