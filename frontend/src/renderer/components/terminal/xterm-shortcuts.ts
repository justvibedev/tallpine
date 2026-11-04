/** Keyboard shortcut classification and focus helpers for the xterm terminal. */
import { terminalFontSizeDelta as shortcutFontSizeDelta } from "../../../shared/shortcuts";
import { isDialogOrMenuOpenOutside } from "../../lib/dom-selectors";
import { isMacPlatform } from "../../lib/platform";

export function isTerminalCopyShortcut(event: KeyboardEvent): boolean {
	if (event.key === "Insert") return event.ctrlKey && !event.altKey && !event.metaKey;
	if (event.key.toLowerCase() !== "c") return false;
	if (event.metaKey) return true;
	if (event.ctrlKey && event.shiftKey && !event.altKey) return true;
	return isWindowsPlatform() && event.ctrlKey && !event.shiftKey && !event.altKey && !event.metaKey;
}

export function isWindowsPlatform(): boolean {
	const platform =
		(navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform ?? navigator.platform;
	return platform.toLowerCase().startsWith("win");
}

export function isTerminalPasteShortcut(event: KeyboardEvent): boolean {
	if (event.key === "Insert") return event.shiftKey && !event.ctrlKey && !event.altKey && !event.metaKey;
	if (event.key.toLowerCase() !== "v") return false;
	if (event.metaKey) return true;
	if (event.ctrlKey && event.shiftKey && !event.altKey) return true;
	return isWindowsPlatform() && event.ctrlKey && !event.shiftKey && !event.altKey && !event.metaKey;
}

export function isTerminalSearchShortcut(event: KeyboardEvent): boolean {
	if (event.altKey || event.shiftKey || event.key.toLowerCase() !== "f") return false;
	return isMacPlatform()
		? event.metaKey && !event.ctrlKey
		: event.ctrlKey && !event.metaKey;
}

export function consumeTerminalShortcut(event: KeyboardEvent): void {
	event.preventDefault();
	event.stopPropagation();
}

export function terminalFontSizeDelta(event: KeyboardEvent): -1 | 0 | 1 {
	return shortcutFontSizeDelta(
		{
			key: event.key,
			code: event.code,
			ctrl: event.ctrlKey,
			meta: event.metaKey,
			shift: event.shiftKey,
			alt: event.altKey,
		},
		isMacPlatform(),
	);
}

export function normalizedTerminalShortcut(event: KeyboardEvent): string | null {
	if (event.shiftKey) return null;

	// macOS Command+Left/Right → readline beginning/end of line. Do not treat
	// the Windows key (metaKey on Win/Linux) as Command, and do not rewrite
	// Windows Home/End: those must fall through to xterm's native sequences
	// because Ctrl+A is SelectAll in default PSReadLine (#3093).
	if (event.metaKey && !event.ctrlKey && !event.altKey && isMacPlatform()) {
		switch (event.key) {
			case "ArrowLeft":
				return "\x01";
			case "ArrowRight":
				return "\x05";
			default:
				return null;
		}
	}

	if (event.metaKey) return null;

	if (event.altKey && !event.ctrlKey) {
		switch (event.key) {
			case "ArrowLeft":
				return "\x1bb";
			case "ArrowRight":
				return "\x1bf";
			case "Backspace":
				return "\x1b\x7f";
			case "Delete":
				return "\x1bd";
			default:
				return null;
		}
	}

	if (event.ctrlKey && !event.altKey) {
		switch (event.key) {
			case "ArrowLeft":
				return "\x1b[1;5D";
			case "ArrowRight":
				return "\x1b[1;5C";
			case "Backspace":
				return "\x1b\x7f";
			case "Delete":
				return "\x1bd";
			default:
				return null;
		}
	}

	return null;
}

export function terminalHasFocus(host: HTMLElement): boolean {
	const activeElement = document.activeElement;
	return !!activeElement && host.contains(activeElement);
}

export function canAutoFocusTerminal(host: HTMLElement): boolean {
	if (isDialogOrMenuOpenOutside(host)) return false;
	const activeElement = document.activeElement;
	if (!(activeElement instanceof HTMLElement) || activeElement === document.body || !activeElement.isConnected) return true;
	if (host.contains(activeElement)) return true;
	// Selecting a session in the sidebar deliberately leaves its navigation
	// button focused. Terminal tabs and controls that launch an inline PTY are
	// the same intentional handoff. Every other focused control remains
	// authoritative.
	return (
		activeElement.matches("button[aria-current='page']") ||
		activeElement.matches("button[data-terminal-focus-handoff='true']") ||
		(activeElement.matches("button[role='tab']") &&
			activeElement.closest('[data-testid="session-workspace-topbar"]') !== null) ||
		// Shell-tab close/rename affordances carry the marker on the button
		// itself (ShellTerminalTab). Match the button directly — not via
		// closest() — because the session tab's ⋮ trigger sits inside a
		// wrapper div[data-terminal-tab-action] that must stay excluded.
		activeElement.matches("button[data-terminal-tab-action='true']")
	);
}
