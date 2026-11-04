/* Split out of main.ts (B297, P52). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import {
nativeTheme
} from "electron";
import { desktopDataDir } from "../app-bootstrap";
import { ctx } from "../app-context";
import { ipcMain } from "../app-runtime-state";
import { persistTerminalThemeHint as persistDesktopTerminalThemeHint } from "../desktop-data-dir";
import { syncNativeWindowBackground } from "../window/main-window";



export function persistTerminalThemeHint(scheme: "light" | "dark"): void {
	persistDesktopTerminalThemeHint(desktopDataDir, scheme);
}

export function registerThemeIpc(): void {


	// Drive Electron's nativeTheme from the app's theme preference so embedded
	// preview WebContentsViews (which follow prefers-color-scheme) flip in step with
	// the shell. The three preference values map 1:1 onto themeSource; "system" keeps
	// both the preview and the shell's own matchMedia following the OS.
	ipcMain.handle("theme:set", (_event, preference: "light" | "dark" | "system") => {
		if (preference === "light" || preference === "dark" || preference === "system") {
			nativeTheme.themeSource = preference;
			syncNativeWindowBackground();
		}
	});


	ipcMain.handle("theme:set-window-background", (_event, color: unknown) => {
		if (typeof color === "string" && /^#[0-9a-f]{6}$/i.test(color)) {
			ctx.rendererWindowBackground = color;
			syncNativeWindowBackground();
		}
	});


	ipcMain.handle("theme:set-window-translucent", (_event, enabled: unknown) => {
		// No early return when the value is unchanged: each new shell document asks
		// again, and re-applying is cheap and recovers any native state that drifted.
		if (process.platform !== "darwin" || typeof enabled !== "boolean") return;
		if (!ctx.mainWindow || ctx.mainWindow.isDestroyed()) return;
		ctx.windowTranslucent = enabled;
		syncNativeWindowBackground();
	});


	ipcMain.handle("theme:persist-terminal",(_event, scheme: unknown) => {
		if (scheme === "light" || scheme === "dark") {
			persistTerminalThemeHint(scheme);
		}
	});
}
