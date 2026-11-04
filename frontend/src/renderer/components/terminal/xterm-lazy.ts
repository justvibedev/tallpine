/** Lazy xterm chunk loader shared by the terminal pane and the shell preloader. */
import { lazy } from "react";


// xterm and its WebGL addon (~700 KB) load on first terminal render instead of
// with the shell: the shell imports TerminalCacheProvider and this loader, so a
// static import of XtermTerminal here would put xterm on every route. preloadXtermTerminal() warms the
// chunk in idle time once the daemon is up.
const loadXtermTerminal = () => import("../XtermTerminal");
export const XtermTerminal = lazy(() => loadXtermTerminal().then((module) => ({ default: module.XtermTerminal })));

export function preloadXtermTerminal(): void {
	void loadXtermTerminal().catch(() => undefined);
}
