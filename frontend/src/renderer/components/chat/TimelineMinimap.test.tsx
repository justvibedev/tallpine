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

/** jsdom never matches :focus-visible; Chromium does for keyboard focus. */
function keyboardFocus(track: HTMLElement) {
	const matches = track.matches.bind(track);
	vi.spyOn(track, "matches").mockImplementation((selector) => selector === ":focus-visible" || matches(selector));
	fireEvent.focus(track);
}

describe("TimelineMinimap", () => {
	it("keyboard focus selects a marker while the inspector is closed", () => {
		const setHovered = vi.fn();
		render(<TimelineMinimap {...props({ current: false }, setHovered)} />);
		keyboardFocus(screen.getByTestId("chat-conversation-minimap"));
		expect(setHovered).toHaveBeenCalledWith(0);
	});

	// B812: a click left the track focused; when the window regained focus the
	// track got focus again and opened a card no pointer was there to close.
	it("does not open the card for focus that is not keyboard focus", () => {
		const setHovered = vi.fn();
		render(<TimelineMinimap {...props({ current: false }, setHovered)} />);
		fireEvent.focus(screen.getByTestId("chat-conversation-minimap"));
		expect(setHovered).not.toHaveBeenCalled();
	});

	it("closes the card when the window loses focus", () => {
		const setHovered = vi.fn();
		render(<TimelineMinimap {...{ ...props({ current: false }, setHovered), hoveredMarker: 0 }} />);
		expect(screen.getByRole("tooltip")).toBeInTheDocument();
		fireEvent.blur(window);
		expect(setHovered).toHaveBeenCalledWith(null);
	});

	it("reads the inspector ref at focus time, not the value from the last render", () => {
		const ref = { current: false };
		const setHovered = vi.fn();
		render(<TimelineMinimap {...props(ref, setHovered)} />);
		// The inspector opens without re-rendering the Timeline.
		ref.current = true;
		keyboardFocus(screen.getByTestId("chat-conversation-minimap"));
		expect(setHovered).not.toHaveBeenCalled();
	});
});
