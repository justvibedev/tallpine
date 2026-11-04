import type { BrowserRect } from "./types";

export function clampBoundsToWindow(
  rect: BrowserRect,
  windowBounds: Pick<BrowserRect, "width" | "height">,
): BrowserRect {
  const rounded = {
    x: Math.round(rect.x),
    y: Math.round(rect.y),
    width: Math.max(0, Math.round(rect.width)),
    height: Math.max(0, Math.round(rect.height)),
  };
  const maxX = Math.max(0, Math.round(windowBounds.width));
  const maxY = Math.max(0, Math.round(windowBounds.height));
  const x = Math.min(Math.max(rounded.x, 0), maxX);
  const y = Math.min(Math.max(rounded.y, 0), maxY);
  return {
    x,
    y,
    width: Math.min(rounded.width, Math.max(0, maxX - x)),
    height: Math.min(rounded.height, Math.max(0, maxY - y)),
  };
}

export function scaleBoundsForZoom(
  rect: BrowserRect,
  zoomFactor: number,
): BrowserRect {
  if (!Number.isFinite(zoomFactor) || zoomFactor <= 0 || zoomFactor === 1)
    return rect;
  return {
    x: rect.x * zoomFactor,
    y: rect.y * zoomFactor,
    width: rect.width * zoomFactor,
    height: rect.height * zoomFactor,
  };
}

// Web pages may declare `app-region: drag` for their own Electron desktop
// builds (Linear does). Chrome ignores it, but Electron turns those areas of
// the embedded view into Tallpine window-drag regions that never receive wheel,
// click, or scrollbar input. A user-origin !important rule outranks the page's
// author rules, inline styles included.
export const BROWSER_NO_DRAG_CSS = `
	*,
	*::before,
	*::after {
		app-region: no-drag !important;
	}
`;

export const browserScrollbarCSS = (zoomFactor: number): string => {
  const effectiveZoom =
    Number.isFinite(zoomFactor) && zoomFactor > 0 ? zoomFactor : 1;
  const thickness = Number((8 / effectiveZoom).toFixed(3));
  return `
	::-webkit-scrollbar {
		width: ${thickness}px;
		height: ${thickness}px;
	}

	::-webkit-scrollbar-button {
		display: none;
	}

	::-webkit-scrollbar-track,
	::-webkit-scrollbar-corner {
		background: transparent;
	}

	::-webkit-scrollbar-thumb {
		border-radius: 999px;
		background: rgba(232, 232, 232, 0.72);
	}

	::-webkit-scrollbar-thumb:hover {
		background: rgba(232, 232, 232, 0.86);
	}
`;
};
