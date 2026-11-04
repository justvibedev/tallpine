/* Split out of main-window.ts (B297, P53). */
import { app } from "electron";
import { MAC_TITLEBAR_HEIGHT, MAC_WINDOW_BUTTON_RADIUS, MAC_WINDOW_BUTTON_X } from "../../shared/window-chrome";
import { APP_DISPLAY_NAME, } from "../app-bootstrap";
import { NATIVE_WINDOW_BACKGROUND_DARK } from "../app-runtime-state";
import { windowIconPath } from "./renderer-assets";

/** Native window options for the main window, per platform. */
export function mainWindowOptions(): Electron.BaseWindowConstructorOptions {
	const options: Electron.BaseWindowConstructorOptions = {
		width: 1320,
		height: 860,
		minWidth: 960,
		minHeight: 640,
		title: app.isPackaged ? APP_DISPLAY_NAME : `${APP_DISPLAY_NAME} (dev)`,
		icon: windowIconPath(),
		backgroundColor: NATIVE_WINDOW_BACKGROUND_DARK,
		// Shown once the renderer has loaded (showWhenRendererLoaded below), so the
		// first thing on screen is the app instead of an empty native frame.
		show: false,
		// Windows goes frameless and the renderer paints the whole titlebar,
		// including custom min/max/close controls. macOS keeps the inset
		// traffic-light chrome, and Linux uses standard frame decorations.
		...(process.platform === "win32"
			? {
					titleBarStyle: "hidden" as const,
					// Hide the native menu bar. A role-based menu is still installed (for
					// accelerators) below; the visible menu is painted by WindowTitlebar.
					autoHideMenuBar: true,
				}
			: process.platform === "linux"
				? {
						// Auto-hide the native menu bar strip. Accelerators stay active
						// via the application menu; pressing Alt reveals the menu bar.
						autoHideMenuBar: true,
					}
				: {
						titleBarStyle: "hiddenInset" as const,
						// Window transparency: an opaque window keeps Chromium's compositor
						// opaque, hiding the vibrancy view. Transparent windows still paint
						// the theme colour via setBackgroundColor unless window transparency
						// is enabled in Appearance.
						transparent: true,
						// Keep the window-transparency blur vivid when the window loses focus.
						visualEffectState: "active" as const,
						// Center on the shared header; zoom/resize synchronization follows below.
						trafficLightPosition: { x: MAC_WINDOW_BUTTON_X, y: MAC_TITLEBAR_HEIGHT / 2 - MAC_WINDOW_BUTTON_RADIUS },
					}),
	};
	return options;
}
