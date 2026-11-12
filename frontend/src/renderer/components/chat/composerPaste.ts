import { COMMAND_PRIORITY_HIGH, PASTE_COMMAND, type LexicalEditor } from "lexical";

/** Policy for collapsing a large paste in the chat composer into an inline chip. */

export const PASTE_COLLAPSE_MIN_LINES = 3;
export const PASTE_COLLAPSE_MIN_CHARS = 200;
export const PASTE_PREVIEW_LINES = 12;

function splitLines(text: string): string[] {
	return text.replace(/\r\n?/g, "\n").replace(/\n$/, "").split("\n");
}

/** Number of lines a paste spans; one trailing newline does not add a line. */
export function pasteLineCount(text: string): number {
	return text === "" ? 0 : splitLines(text).length;
}

export function shouldCollapsePaste(text: string): boolean {
	return pasteLineCount(text) >= PASTE_COLLAPSE_MIN_LINES || text.length >= PASTE_COLLAPSE_MIN_CHARS;
}

/** The first lines shown on hover, and how many lines were left out. */
export function pastePreview(text: string): { text: string; hiddenLines: number } {
	const lines = splitLines(text);
	return {
		text: lines.slice(0, PASTE_PREVIEW_LINES).join("\n"),
		hiddenLines: Math.max(0, lines.length - PASTE_PREVIEW_LINES),
	};
}

/** Cmd/Ctrl+Shift+V pastes the text as-is instead of collapsing it. */
export function isRawPasteShortcut(event: { key: string; metaKey: boolean; ctrlKey: boolean; shiftKey: boolean }): boolean {
	return event.shiftKey && (event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "v";
}

/**
 * Collapses large text pastes through `insert` (called inside the editor update),
 * unless Cmd/Ctrl+Shift+V was just pressed. Returns the unregister function.
 */
export function registerPasteCollapse(editor: LexicalEditor, insert: (text: string) => boolean): () => void {
	let raw = false;
	const onKeyDown = (event: KeyboardEvent) => {
		raw = isRawPasteShortcut(event);
	};
	const removeRoot = editor.registerRootListener((root, previous) => {
		previous?.removeEventListener("keydown", onKeyDown, true);
		root?.addEventListener("keydown", onKeyDown, true);
	});
	const removeCommand = editor.registerCommand(
		PASTE_COMMAND,
		(event) => {
			const wasRaw = raw;
			raw = false;
			const data = "clipboardData" in event ? event.clipboardData : null;
			if (wasRaw || !data || data.files.length > 0) return false;
			const text = data.getData("text/plain");
			if (!shouldCollapsePaste(text)) return false;
			event.preventDefault();
			return insert(text);
		},
		COMMAND_PRIORITY_HIGH,
	);
	return () => {
		removeCommand();
		removeRoot();
	};
}
