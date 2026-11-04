import { tallpineBridge } from "./bridge";
import { isMacPlatform } from "./platform";

/** Retired Developer mode switch; only read once by the ui-store migration. */
export const legacyDeveloperModeStorageKey = "ao.developerMode";
export const windowTranslucencyStorageKey = "ao.windowTranslucency";

/** The saved Appearance preference, macOS only. */
export function readStoredWindowTranslucency(): boolean {
	if (typeof window === "undefined" || !window.localStorage || !isMacPlatform()) return false;
	return window.localStorage.getItem(windowTranslucencyStorageKey) === "true";
}

/**
 * Marks the document for the translucent styles (styles.css, "macOS window
 * transparency") and asks main for the native vibrancy behind them. Each new
 * document synchronizes the native window with its saved preference.
 */
export function applyWindowTranslucency(enabled: boolean): void {
	if (typeof document === "undefined") return;
	if (enabled) document.documentElement.dataset.windowTranslucent = "true";
	else delete document.documentElement.dataset.windowTranslucent;
	void tallpineBridge.theme?.setWindowTranslucent(enabled)?.catch(() => undefined);
}
