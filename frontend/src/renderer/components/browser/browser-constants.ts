/** Device presets and tab-drag modifiers for the browser panel. */
import { type Modifier } from "@dnd-kit/core";

// One-click viewport width presets for responsive testing — height is shown
// for reference but not enforced (only width drives CSS breakpoints, and
// this is a docked panel of limited, variable height, not a device
// emulator). No "Desktop" entry: the panel is already viewed on desktop, so
// that preset was always a no-op. "Custom" covers anything these named
// devices don't — you're never stuck with only this list.
//
// Matches Chrome DevTools' own "Standard" device list (front_end/models/
// emulation/EmulatedDevices.ts) so anyone already familiar with that list
// finds the same names here. Dimensions are each device's portrait/vertical
// mode from that source; Nest Hub/Max are fixed-landscape smart displays, so
// their one orientation is used directly. iPad Air and Nest Hub have since
// been dropped from Chrome's own current list but are kept here since
// they're still common, well-known breakpoints worth testing against.
export const DEVICE_PRESETS: { id: string; label: string; width: number; height: number; category: "phone" | "tablet" }[] = [
	{ id: "iphone-se", label: "iPhone SE", width: 375, height: 667, category: "phone" },
	{ id: "iphone-xr", label: "iPhone XR", width: 414, height: 896, category: "phone" },
	{ id: "iphone-12-pro", label: "iPhone 12 Pro", width: 390, height: 844, category: "phone" },
	{ id: "iphone-14-pro-max", label: "iPhone 14 Pro Max", width: 430, height: 932, category: "phone" },
	{ id: "iphone-15-pro-max", label: "iPhone 15 Pro Max", width: 430, height: 932, category: "phone" },
	{ id: "iphone-16-pro-max", label: "iPhone 16 Pro Max", width: 440, height: 956, category: "phone" },
	{ id: "pixel-7", label: "Pixel 7", width: 412, height: 915, category: "phone" },
	{ id: "pixel-8", label: "Pixel 8", width: 412, height: 915, category: "phone" },
	{ id: "pixel-9", label: "Pixel 9", width: 412, height: 924, category: "phone" },
	{ id: "pixel-10", label: "Pixel 10", width: 412, height: 924, category: "phone" },
	{ id: "galaxy-s8-plus", label: "Samsung Galaxy S8+", width: 360, height: 740, category: "phone" },
	{ id: "galaxy-s20-ultra", label: "Samsung Galaxy S20 Ultra", width: 412, height: 915, category: "phone" },
	{ id: "galaxy-a51-71", label: "Samsung Galaxy A51/71", width: 412, height: 914, category: "phone" },
	{ id: "ipad-mini", label: "iPad Mini", width: 768, height: 1024, category: "tablet" },
	{ id: "ipad-air", label: "iPad Air", width: 820, height: 1180, category: "tablet" },
	{ id: "ipad-pro", label: "iPad Pro", width: 1032, height: 1376, category: "tablet" },
	{ id: "surface-pro-7", label: "Surface Pro 7", width: 912, height: 1368, category: "tablet" },
	{ id: "surface-duo", label: "Surface Duo", width: 540, height: 720, category: "phone" },
	{ id: "galaxy-z-fold-5", label: "Galaxy Z Fold 5", width: 344, height: 882, category: "phone" },
	{ id: "zenbook-fold", label: "Asus Zenbook Fold", width: 853, height: 1280, category: "tablet" },
	{ id: "nest-hub", label: "Nest Hub", width: 1024, height: 600, category: "tablet" },
	{ id: "nest-hub-max", label: "Nest Hub Max", width: 1280, height: 800, category: "tablet" },
];
export const CUSTOM_DEVICE_PRESET_ID = "custom";
export const MAX_HISTORY_SUGGESTIONS = 4;
export const MIN_DEVICE_FRAME_WIDTH = 240;
export const MAX_DEVICE_FRAME_WIDTH = 2560;

export const restrictBrowserTopTabDragToTabStrip: Modifier = ({
	activeNodeRect,
	containerNodeRect,
	transform,
}) => {
	if (!activeNodeRect || !containerNodeRect) return { ...transform, y: 0 };
	const minX = containerNodeRect.left - activeNodeRect.left;
	const maxX = containerNodeRect.right - activeNodeRect.right;
	return {
		...transform,
		x: Math.min(maxX, Math.max(minX, transform.x)),
		y: 0,
	};
};
export const browserTopTabDragModifiers = [restrictBrowserTopTabDragToTabStrip];

export function clampDeviceFrameWidth(width: number): number | undefined {
	if (!Number.isFinite(width)) return undefined;
	return Math.min(MAX_DEVICE_FRAME_WIDTH, Math.max(MIN_DEVICE_FRAME_WIDTH, Math.round(width)));
}
