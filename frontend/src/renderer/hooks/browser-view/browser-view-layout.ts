/** Layout revision, hole publishing and visible slot geometry helpers. */
import type { BrowserRect } from "../../../main/browser-view-host";

export const HIDDEN_RECT: BrowserRect = { x: 0, y: 0, width: 0, height: 0 };

// Keep revisions monotonic across hook remounts. performance.timeOrigin gives a
// newly loaded shell a newer range while staying below Number.MAX_SAFE_INTEGER.
let nextLayoutRevision = Math.floor(globalThis.performance?.timeOrigin ?? Date.now()) * 1_000;

export function claimLayoutRevision(): number {
	nextLayoutRevision += 1;
	return nextLayoutRevision;
}

// The macOS translucent window tints the shell over the vibrancy blur. The live
// page sits below the shell, so styles.css cuts the tint out over this rect
// rather than tinting the page. Last visible view owns the hole.
let browserHoleOwner: string | null = null;

export function publishBrowserHole(viewId: string, rect: BrowserRect | null): void {
	const root = document.documentElement;
	// Custom properties on :root restyle the whole document, so only write them
	// while the translucent window actually needs the hole.
	if (rect && root.dataset.windowTranslucent !== "true") rect = null;
	if (!rect) {
		if (browserHoleOwner !== viewId) return;
		browserHoleOwner = null;
		delete root.dataset.browserHole;
		return;
	}
	browserHoleOwner = viewId;
	root.dataset.browserHole = "true";
	root.style.setProperty("--browser-hole-x", `${rect.x}px`);
	root.style.setProperty("--browser-hole-y", `${rect.y}px`);
	root.style.setProperty("--browser-hole-w", `${rect.width}px`);
	root.style.setProperty("--browser-hole-h", `${rect.height}px`);
}

// ResizeHandle.tsx sits at the inspector panel's left edge with a
// `--size-resize-handle-offset` (6px) negative inset, so only its right half
// (0 to 6px, inside the panel) survives the panel's `overflow-hidden` — the
// left half is clipped away. That surviving 6px is inside `[data-panel]`, the
// same territory the browser view fills, so without this reserve the native
// view covers the handle at rest and a drag can never start once a page is
// loaded (only continuing an already-started drag is handled elsewhere, via
// the `is-resizing-x` watcher below). Keep in sync with tokens.css.
export const RESIZE_HANDLE_RESERVE_PX = 6;

// The native WebContentsView is a window-level overlay, so DOM `overflow:
// hidden` never clips it — it paints wherever the slot's bounding box lands.
// During inspector open/close the column slides on a transform (the slot box
// moves without a ResizeObserver size change), so also clip to `.session-split`.
export function visibleSlotRect(node: HTMLElement): BrowserRect {
	const rect = node.getBoundingClientRect();
	let { left, top, right, bottom } = rect;
	const panel = node.closest<HTMLElement>("[data-panel]");
	if (panel) {
		const bounds = panel.getBoundingClientRect();
		left = Math.max(left, bounds.left + RESIZE_HANDLE_RESERVE_PX);
		top = Math.max(top, bounds.top);
		right = Math.min(right, bounds.right);
		bottom = Math.min(bottom, bounds.bottom);
	}
	const split = node.closest<HTMLElement>(".session-split");
	if (split) {
		const bounds = split.getBoundingClientRect();
		left = Math.max(left, bounds.left);
		top = Math.max(top, bounds.top);
		right = Math.min(right, bounds.right);
		bottom = Math.min(bottom, bounds.bottom);
	}
	return {
		x: left,
		y: top,
		width: Math.max(0, right - left),
		height: Math.max(0, bottom - top),
	};
}

// `requestFullscreen` (the terminal pane's fullscreen button) promotes an element
// into the DOM top layer, which covers every other DOM node — but not the native
// view, which Chromium composites above the page regardless. The transition also
// leaves the slot's own box untouched, since the top layer does not reflow
// normal-flow siblings, so neither the ResizeObserver nor `resize` fires and the
// view would keep painting at its pre-fullscreen bounds, over the fullscreen
// element and without its own (now hidden) toolbar. Nothing outside the
// fullscreen subtree is visible, so hide the view unless the slot is inside it.
export function hiddenByFullscreen(node: HTMLElement): boolean {
	// Truthy, not `!== null`: the spec says null, but jsdom (and older engines)
	// leave `fullscreenElement` undefined when nothing is fullscreen.
	const fullscreen = document.fullscreenElement;
	return Boolean(fullscreen) && !fullscreen!.contains(node);
}
