
import { expect, it } from "vitest";
import { restrictBrowserTopTabDragToTabStrip } from "./browser/browser-constants";
import { reorderBrowserTabs } from "../lib/browser-tab-order";



it("reorders browser tabs around the drop target", () => {
	expect(reorderBrowserTabs(["t1", "t2", "t3"], "t1", "t3")).toEqual(["t2", "t3", "t1"]);
	expect(reorderBrowserTabs(["t1", "t2", "t3"], "t2", "missing")).toBeNull();
});

it("keeps a dragged browser tab inside the current tab strip bounds", () => {
	const activeNodeRect = { left: 650, right: 850 };
	const constrain = (left: number, right: number, x: number) =>
		restrictBrowserTopTabDragToTabStrip({
			activeNodeRect,
			containerNodeRect: { left, right },
			transform: { x, y: 24, scaleX: 1, scaleY: 1 },
		} as Parameters<typeof restrictBrowserTopTabDragToTabStrip>[0]);

	expect(constrain(600, 900, -300)).toMatchObject({ x: -50, y: 0 });
	expect(constrain(600, 900, 300)).toMatchObject({ x: 50, y: 0 });
	// A dock/resize transition can move the inspector while a drag is active.
	// Use the newly measured strip rather than the window, so the chip follows it.
	expect(constrain(760, 1060, -300)).toMatchObject({ x: 110, y: 0 });
});

