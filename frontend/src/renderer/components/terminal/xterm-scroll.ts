/** SGR wheel/page-key reports and drag-selection helpers for the xterm terminal. */
import { Terminal } from "@xterm/xterm";
import { XtermInternal } from "./xterm-types";

export const SGR_WHEEL_UP = 64;
export const SGR_WHEEL_DOWN = 65;

export function sgrWheelReport(button: number, count: number, column: number, row: number): string {
	return `\x1b[<${button};${column};${row}M`.repeat(count);
}

// PageUp (CSI 5~) / PageDown (CSI 6~) for pane apps that scroll their transcript
// by keyboard rather than mouse reports. One page key per wheel notch: a page
// already scrolls a full screen, so scaling by line count would over-scroll.
export const PAGE_UP = "\x1b[5~";
export const PAGE_DOWN = "\x1b[6~";
export const MAC_TERMINAL_SCROLLBAR_WIDTH = 7;
export const MAC_TERMINAL_SCROLLBAR_IDLE_MS = 700;

export function pageKeyReport(lines: number): string {
	return lines < 0 ? PAGE_UP : PAGE_DOWN;
}

export function forceSelectionMode(term: Terminal): void {
	const internal = term as XtermInternal;
	const selectionService = internal._core?._selectionService;
	const element = internal._core?.element;
	if (!selectionService || !element) return;
	selectionService.shouldForceSelection = () => true;
	selectionService.enable();
	element.classList.remove("enable-mouse-events");
}

// xterm deliberately keeps drag selection listening on document. When a drag
// leaves the terminal horizontally, its coordinate conversion clamps the pointer
// to the last terminal column. In split layouts that turns a drag into the
// neighboring inspector into a selection of a full-width TUI sidebar (OpenCode
// is the visible example). Keep vertical overflow intact for xterm's standard
// drag-to-scroll behavior, but do not extend a selection into a sibling pane.
export function confineDragSelectionToTerminalWidth(term: Terminal): void {
	const internal = term as XtermInternal;
	const selectionService = internal._core?._selectionService;
	const element = internal._core?.element;
	const originalMouseMoveListener = selectionService?._mouseMoveListener;
	if (!selectionService || !element || !originalMouseMoveListener) return;

	selectionService._mouseMoveListener = (event: Event) => {
		if (!(event instanceof MouseEvent)) return;
		const { left, right } = element.getBoundingClientRect();
		if (event.clientX < left || event.clientX > right) {
			// xterm's document-level drag timer continues using its last vertical
			// overflow value. Clear that value when the pointer enters a sibling
			// pane, otherwise a previous below-the-terminal drag keeps scrolling and
			// extends the frozen selection.
			selectionService._dragScrollAmount = 0;
			return;
		}
		originalMouseMoveListener(event);
	};
}
