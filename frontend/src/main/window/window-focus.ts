/* Split out of main-window.ts (B297, P53). */
import {
BaseWindow,
type WebContents
} from "electron";
import { ctx } from "../app-context";
import { createWindow } from "./main-window";

// BaseWindow has no ready-to-show event, so show the hidden window when the
// shell's document has loaded (or failed to), with a fallback so a renderer that
// never finishes cannot leave the app invisible. Anything that needs the window
// sooner (focusMainWindow, deep links, the dock) can still show it directly.
export const WINDOW_SHOW_FALLBACK_MS = 2_500;

export function showWhenRendererLoaded(window: BaseWindow, contents: WebContents): void {
	let shown = false;
	const show = () => {
		if (shown) return;
		shown = true;
		clearTimeout(fallback);
		if (window.isDestroyed() || window.isVisible()) return;
		window.show();
	};
	const fallback = setTimeout(show, WINDOW_SHOW_FALLBACK_MS);
	contents.once("did-finish-load", show);
	contents.once("did-fail-load", show);
}

export function focusMainWindow(): void {
	if (!ctx.mainWindow) {
		createWindow();
		return;
	}
	if (ctx.mainWindow.isMinimized()) ctx.mainWindow.restore();
	ctx.mainWindow.show();
	ctx.mainWindow.focus();
}
