/* Split out of main.ts (B297, P52). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import {
app,
dialog,
Menu,
webContents
} from "electron";
import {
KEYBOARD_SHORTCUTS_HELP_CHANNEL
} from "../../shared/shortcuts";
import { aboutDetail, APP_DISPLAY_NAME } from "../app-bootstrap";
import { ctx } from "../app-context";
import { toggleAppDevTools } from "../app-devtools";
import { getShellWebContents, ipcMain } from "../app-runtime-state";
import { buildLinuxAppMenuTemplate, buildWindowsAppMenuTemplate } from "../menu";
import { syncMacWindowButtons } from "../window-chrome";

// Menu installed on Windows where the native menu bar is hidden. The bar stays
// out of sight, but the roles keep their accelerators alive (Reload, zoom, full
// screen, edit commands). DevTools uses the Tallpine browser toggle so the focused
// Browser panel opens the same native Chromium surface as the toolbar.
export function buildWindowsAppMenu(): Menu {
	return Menu.buildFromTemplate(
		buildWindowsAppMenuTemplate(() => {
			const fallback = () => getShellWebContents()?.toggleDevTools();
			void ctx.browserViewHost?.toggleDevToolsForLastFocused().then((state) => {
				if (!state) fallback();
			}).catch(fallback);
		}),
	);
}

// Menu installed on Linux where the native menu bar is hidden by default.
// The role-based menu preserves standard accelerators (Reload, DevTools, zoom,
// full screen, edit commands) while routing DevTools through Tallpine's guarded handler.
export function buildLinuxAppMenu(): Menu {
	return Menu.buildFromTemplate(
		buildLinuxAppMenuTemplate(() => {
			void toggleAppDevTools(ctx.browserViewHost, getShellWebContents);
		}),
	);
}

export function registerAppMenu(): void {

	// Backs the custom title-bar menu (WindowTitlebar). Each item maps to the same
	// action the native default menu would have performed.
	ipcMain.handle("menu:action", (_event, action: string) => {
		const win = ctx.mainWindow;
		if (!win) return;
		// Clicking this shell-painted menu moves focus off the panel, so prefer the last-focused panel, else the focused contents, else the shell.
		const focused = webContents.getFocusedWebContents();
		const shell = getShellWebContents();
		const wc =
			(focused && focused !== shell ? focused : ctx.browserViewHost?.getLastFocusedPanelContents()) ?? shell;
		if (!wc) return;
		switch (action) {
			case "edit.undo":
				return wc.undo();
			case "edit.redo":
				return wc.redo();
			case "edit.cut":
				return wc.cut();
			case "edit.copy":
				return wc.copy();
			case "edit.paste":
				return wc.paste();
			case "edit.selectAll":
				return wc.selectAll();
			case "view.reload":
				// An agent tab's Reload is the agent's page, not its viewer's.
				if (ctx.browserViewHost?.reloadAgentTab(wc)) return;
				return wc.reload();
			case "view.devtools":
				if (ctx.browserViewHost) {
					return ctx.browserViewHost.toggleDevToolsForLastFocused().then((state) => {
						if (!state) wc?.toggleDevTools();
					}).catch(() => wc?.toggleDevTools());
				}
				return wc?.toggleDevTools();
			case "view.zoomIn":
				wc.setZoomLevel(wc.getZoomLevel() + 0.5);
				if (process.platform === "darwin" && wc === getShellWebContents()) syncMacWindowButtons(win, wc);
				wc.send("window:zoom", wc.getZoomFactor());
				return;
			case "view.zoomOut":
				wc.setZoomLevel(wc.getZoomLevel() - 0.5);
				if (process.platform === "darwin" && wc === getShellWebContents()) syncMacWindowButtons(win, wc);
				wc.send("window:zoom", wc.getZoomFactor());
				return;
			case "view.zoomReset":
				wc.setZoomLevel(0);
				if (process.platform === "darwin" && wc === getShellWebContents()) syncMacWindowButtons(win, wc);
				wc.send("window:zoom", wc.getZoomFactor());
				return;
			case "view.fullscreen":
				return win.setFullScreen(!win.isFullScreen());
			case "window.minimize":
				return win.minimize();
			case "window.maximize":
				return win.isMaximized() ? win.unmaximize() : win.maximize();
			case "window.close":
				return win.close();
			case "app.quit":
				return app.quit();
			case "help.shortcuts":
				shell?.focus();
				return shell?.send(KEYBOARD_SHORTCUTS_HELP_CHANNEL);
			case "help.about":
				void dialog.showMessageBox(win, {
					type: "info",
					title: `About ${APP_DISPLAY_NAME}`,
					message: APP_DISPLAY_NAME,
					detail: aboutDetail(),
					buttons: ["OK"],
				});
				return;
		}
	});
}
