import { fireEvent, render, screen } from "@testing-library/react";
import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";
import { TimelineMinimap, type TimelineMinimapProps } from "./TimelineMinimap";

function props(inspectorOpenRef: { current: boolean }, setHoveredMarker = vi.fn()): TimelineMinimapProps {
	return {
		scrollTrack: createRef<HTMLDivElement>(),
		inspectorOpenRef,
		minimapEnabled: true,
		minimapRevealed: true,
		scrollbar: { visible: true, top: 0, height: 40, percent: 50, markers: [{ top: 10, scrollTop: 0, visible: true }] },
		previews: [{ title: "Prompt" }],
		hoveredMarker: null,
		setHoveredMarker,
		onPointerDown: vi.fn(),
		onPointerMove: vi.fn(),
		stopDrag: vi.fn(),
		onWheel: vi.fn(),
		onKeyDown: vi.fn(),
	};
}

describe("TimelineMinimap", () => {
	it("focus selects a marker while the inspector is closed", () => {
		const setHovered = vi.fn();
		render(<TimelineMinimap {...props({ current: false }, setHovered)} />);
		fireEvent.focus(screen.getByTestId("chat-conversation-minimap"));
		expect(setHovered).toHaveBeenCalledWith(0);
	});

	it("reads the inspector ref at focus time, not the value from the last render", () => {
		const ref = { current: false };
		const setHovered = vi.fn();
		render(<TimelineMinimap {...props(ref, setHovered)} />);
		// The inspector opens without re-rendering the Timeline.
		ref.current = true;
		fireEvent.focus(screen.getByTestId("chat-conversation-minimap"));
		expect(setHovered).not.toHaveBeenCalled();
	});
});
