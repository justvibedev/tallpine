import { useEffect } from "react";
import { applyDocumentTheme, applyDocumentThemeStyle } from "../../lib/theme";
import { buildTerminalThemes } from "../../lib/terminal-themes";
import type { XtermTerminalProps } from "./xterm-types";
import type { useXtermState } from "./useXtermState";


export function useXtermThemeEffects(props: XtermTerminalProps, state: ReturnType<typeof useXtermState>) {
	const {
		announcedCursorSchemeRef,
		colorSchemeReporterRef,
		copiedToastTimerRef,
		fitRef,
		linkPreviewTimerRef,
		notifyCursorSchemeRef,
		termRef,
		themeStyle,
		translucent,
		webglLeaseRef,
	} = state;
	useEffect(
		() => () => {
			if (copiedToastTimerRef.current !== undefined) window.clearTimeout(copiedToastTimerRef.current);
			if (linkPreviewTimerRef.current !== undefined) window.clearTimeout(linkPreviewTimerRef.current);
		},
		[],
	);

	useEffect(() => {
		// buildTerminalThemes() reads live CSS vars from :root. Parent shell effects
		// run after child effects, so sync both independent theme axes here before
		// reading. Retained terminals subscribe to themeStyle directly and update
		// their live palette without being torn down or losing scrollback.
		applyDocumentTheme(props.theme);
		applyDocumentThemeStyle(themeStyle);
		const term = termRef.current;
		if (!term) return;
		const { dark, light } = buildTerminalThemes(translucent);
		term.options.theme = props.theme === "dark" ? dark : light;
		webglLeaseRef.current?.setEnabled(!translucent);
		colorSchemeReporterRef.current?.(props.theme, themeStyle);
	}, [props.theme, themeStyle, translucent]);

	useEffect(() => {
		if (!termRef.current || !props.supportsCursorColorScheme) return;
		announcedCursorSchemeRef.current = null;
		notifyCursorSchemeRef.current(props.theme, true, true);
	}, [props.theme, props.supportsCursorColorScheme]);

	useEffect(() => {
		const term = termRef.current;
		if (!term || !props.fontSize) return undefined;
		term.options.fontSize = props.fontSize;
		fitRef.current?.();
		const timer = window.setTimeout(() => fitRef.current?.(), 50);
		return () => window.clearTimeout(timer);
	}, [props.fontSize]);
}
