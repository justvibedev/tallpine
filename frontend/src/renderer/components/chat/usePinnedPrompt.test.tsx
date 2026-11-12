import { act, fireEvent, render, renderHook, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PinnedPrompt } from "./PinnedPrompt";
import { latestPromptInfo, usePinnedPrompt } from "./usePinnedPrompt";
import type { TimelineGroup } from "./timeline-groups";

function group(key: string, text?: string, ...more: string[]): TimelineGroup {
	const items = text === undefined ? [] : [text, ...more].map((t, i) => ({ id: i === 0 ? key : `${key}${i}`, kind: "message", role: "user", origin: "human", text: t }));
	return { key, items } as unknown as TimelineGroup;
}

function rect(top: number, bottom: number) {
	return { top, bottom, left: 0, right: 0, width: 0, height: bottom - top, x: 0, y: top, toJSON: () => ({}) } as DOMRect;
}

function setup(promptBottom: number) {
	const scroller = document.createElement("div");
	const row = document.createElement("div");
	row.setAttribute("data-index", "1");
	const prompt = document.createElement("div");
	prompt.setAttribute("data-chat-message-id", "b");
	// An earlier human message in the same row (e.g. before a steer) must not be measured.
	const earlier = document.createElement("div");
	earlier.setAttribute("data-chat-message-id", "other");
	earlier.getBoundingClientRect = () => rect(-500, -480);
	row.append(earlier, prompt);
	scroller.append(row);
	scroller.getBoundingClientRect = () => rect(100, 600);
	let bottom = promptBottom;
	prompt.getBoundingClientRect = () => rect(bottom - 20, bottom);
	row.getBoundingClientRect = () => rect(bottom - 20, bottom + 300);
	const scrollTo = vi.fn();
	scroller.scrollTo = scrollTo as never;
	const virtualizer = { measurementsCache: [], scrollToIndex: vi.fn() } as never;
	const groups = [group("a", "first"), group("b", "  second\n prompt "), group("c")];
	const hook = renderHook(() => usePinnedPrompt({ groups, scroller: { current: scroller }, virtualizer, virtualScrollMargin: 0, layoutKey: "k" }));
	return { scroller, scrollTo, hook, move: (b: number) => { bottom = b; act(() => { fireEvent.scroll(scroller); }); } };
}

describe("latestPromptInfo", () => {
	it("returns the last human prompt as one line, or nothing", () => {
		expect(latestPromptInfo([group("a", "first"), group("b", "  second\n prompt "), group("c")])).toEqual({ index: 1, id: "b", text: "second prompt" });
		expect(latestPromptInfo([group("a", "orig", "steered")])).toEqual({ index: 0, id: "a1", text: "steered" });
		expect(latestPromptInfo([group("c")])).toBeUndefined();
	});
});

describe("usePinnedPrompt", () => {
	it("appears once the prompt scrolls above the viewport and disappears when it returns", () => {
		const t = setup(300);
		expect(t.hook.result.current.visible).toBe(false);
		t.move(90);
		expect(t.hook.result.current.visible).toBe(true);
		t.move(300);
		expect(t.hook.result.current.visible).toBe(false);
	});

	it("jump scrolls the prompt row to the top of the viewport", () => {
		const t = setup(90);
		act(() => t.hook.result.current.jump());
		// row top (70) is 30px above the scroller top (100); minus 8px breathing room.
		expect(t.scrollTo).toHaveBeenCalledWith(expect.objectContaining({ top: -38 }));
	});
});

describe("usePinnedPrompt with an unmounted (virtualized) prompt row", () => {
	function virtualSetup(start: number, scrollTop: number) {
		const scroller = document.createElement("div");
		scroller.scrollTop = scrollTop;
		const scrollTo = vi.fn();
		scroller.scrollTo = scrollTo as never;
		const scrollToIndex = vi.fn();
		const virtualizer = { measurementsCache: [{ start: 0 }, { start }], scrollToIndex } as never;
		const groups = [group("a", "first"), group("b", "second")];
		const hook = renderHook(() => usePinnedPrompt({ groups, scroller: { current: scroller }, virtualizer, virtualScrollMargin: 20, layoutKey: "k" }));
		return { scroller, scrollTo, scrollToIndex, hook };
	}

	it("counts a row unmounted above the window as passed, and one below as not", () => {
		expect(virtualSetup(1000, 5000).hook.result.current.visible).toBe(true);
		expect(virtualSetup(9000, 5000).hook.result.current.visible).toBe(false);
	});

	it("jump asks the virtualizer to bring the prompt row to the top", () => {
		const t = virtualSetup(1000, 5000);
		act(() => t.hook.result.current.jump());
		expect(t.scrollToIndex).toHaveBeenCalledWith(1, { align: "start" });
		expect(t.scrollTo).not.toHaveBeenCalled();
	});
});

describe("PinnedPrompt", () => {
	it("shows the prompt text and jumps on click", () => {
		const onJump = vi.fn();
		render(<PinnedPrompt text="second prompt" onJump={onJump} />);
		fireEvent.click(screen.getByTestId("pinned-prompt"));
		expect(screen.getByTestId("pinned-prompt")).toHaveTextContent("second prompt");
		expect(onJump).toHaveBeenCalledOnce();
	});
});
