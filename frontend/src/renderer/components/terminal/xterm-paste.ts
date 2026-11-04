/** Paste, bracketed-paste and copy text helpers for the xterm terminal. */
import { Terminal } from "@xterm/xterm";

// xterm palette tracks the app theme (see lib/terminal-themes.ts + tokens.css).
export const SUPPRESS_NATIVE_PASTE_MS = 100;
/** Long enough to notice, short enough that a second copy reads as a second copy. */
export const COPY_TOAST_MS = 1400;
/** Hover dwell before the link preview card opens, matching ui/hover-card. */
export const LINK_PREVIEW_OPEN_MS = 300;
/** Grace period to move the pointer from the link into the preview card. */
export const LINK_PREVIEW_CLOSE_MS = 300;
export const AUTOFOCUS_RETRY_FRAMES = 6;
export const COLOR_SCHEME_UPDATE_MODE = 2031;
export const COLOR_SCHEME_QUERY = 996;

export function preparePastedText(text: string): string {
	return text.replace(/\r?\n/g, "\r");
}

export function bracketPastedText(text: string, bracketedPasteMode: boolean): string {
	return bracketedPasteMode ? `\x1b[200~${text}\x1b[201~` : text;
}

// xterm marks physically wrapped rows (`isWrapped`) and getSelection() joins
// them itself, but agent TUIs repaint full-screen with absolute cursor moves,
// so their visually continuous rows never get that mark and are copied with a
// hard newline exactly at the window edge (issue #5785). Walk the selected
// buffer range and drop the newline between adjacent selected rows when the
// upper row fills every column of the grid — a full-width TUI row has no line
// end of its own. Rows xterm already merged are skipped, and a row with a
// visible end keeps its newline.
// ponytail: "fills the grid" is a heuristic — a genuine paragraph line that
// happens to end at the last column joins too; buffer-level precision (what
// getSelectionPosition gives us here) is already applied, the rest has no
// on-screen marker to read.
export function joinVisuallyContinuousLines(term: Terminal, selection: string, columnSelection: boolean): string {
	// xterm's column mode intentionally keeps one text line per selected buffer
	// row, even when the upper row fills the grid. Joining those rows would
	// change the rectangular selection into a single line.
	if (columnSelection) return selection;
	const range = term.getSelectionPosition();
	if (!range) return selection;
	const lineBreak = selection.includes("\r\n") ? "\r\n" : "\n";
	const lines = selection.split(lineBreak);
	const buffer = term.buffer.active;
	let joined = lines[0] ?? "";
	let index = 0;
	for (let row = range.start.y + 1; row <= range.end.y; row++) {
		// xterm already merged this row into the previous one; no text boundary here.
		if (buffer.getLine(row)?.isWrapped) continue;
		const text = lines[++index];
		if (text === undefined) break;
		const previous = buffer.getLine(row - 1);
		// Full grid = content in the last visible cell. Read only the current grid
		// width because xterm can retain backing cells after a resize. Null cells
		// compose to spaces and a wide char skips its continuation cell, so this
		// stays cell-accurate without comparing UTF-16 length to cols.
		const continuous = !!previous && !/\s$/.test(previous.translateToString(false, 0, term.cols));
		joined += `${continuous ? "" : lineBreak}${text}`;
	}
	return joined;
}
