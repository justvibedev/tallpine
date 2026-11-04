import type { ThemePreference } from "../lib/theme";
import type { Theme } from "../lib/theme";
import { useEffect } from "react";
import { resolveCssColorToHex } from "../lib/css-color";
import { applyWindowTranslucency } from "../lib/window-translucency";
import { tallpineBridge } from "../lib/bridge";
import { isMac } from "./platform";

export function useShellAppearance({ themePreference, resolvedTheme, windowTranslucency, syncSystemTheme }: {
	themePreference: ThemePreference;
	resolvedTheme: Theme;
	windowTranslucency: boolean;
	syncSystemTheme: () => void;
}) {
	// Keep Electron's nativeTheme in step with the shell so the embedded preview
	// WebContentsView (which follows prefers-color-scheme) flips at the same time.
	// Send the preference, not the resolved theme, so "system" keeps both surfaces
	// following the OS instead of freezing matchMedia to a forced value.
	useEffect(() => {
		void tallpineBridge.theme?.set(themePreference);
	}, [themePreference]);

	// Cursor Agent reads TERM_THEME at spawn from this file. Persist the same
	// resolved light/dark scheme the terminal uses, not nativeTheme alone.
	useEffect(() => {
		void tallpineBridge.theme?.persistTerminal(resolvedTheme);
	}, [resolvedTheme]);

	// Match the native window background to the sidebar fill. Deferred a frame so
	// data-theme has been applied before the token is read.
	useEffect(() => {
		const frame = requestAnimationFrame(() => {
			const color = resolveCssColorToHex("--sidebar");
			if (color) void tallpineBridge.theme?.setWindowBackground(color);
		});
		return () => cancelAnimationFrame(frame);
	}, [resolvedTheme]);

	// macOS Appearance preference: the shell becomes one
	// translucent plate over the native vibrancy blur (styles.css, "macOS window
	// transparency").
	const windowTranslucent = isMac && windowTranslucency;
	useEffect(() => {
		if (!isMac) return;
		applyWindowTranslucency(windowTranslucent);
	}, [windowTranslucent]);

	// Follow OS appearance while the user keeps Theme on System — updates
	// resolvedTheme (and thus React consumers) without writing light/dark to storage.
	useEffect(() => {
		if (themePreference !== "system") return;

		const mediaQuery = window.matchMedia("(prefers-color-scheme: light)");
		const handleChange = () => syncSystemTheme();
		mediaQuery.addEventListener("change", handleChange);
		return () => mediaQuery.removeEventListener("change", handleChange);
	}, [themePreference, syncSystemTheme]);
}
