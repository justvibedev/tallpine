import { render } from "@testing-library/react";
import { useRef } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { chatScrollMemory, useChatScrollMemory } from "./useChatScrollMemory";

const setPinnedNow = vi.fn();
const markWritten = vi.fn();

function Harness({ sessionId, layoutKey, mounted = true }: { sessionId: string; layoutKey: number; mounted?: boolean }) {
	const scroller = useRef<HTMLDivElement>(null);
	useChatScrollMemory({ memoryKey: sessionId, scroller, setPinnedNow, markWritten, layoutKey });
	return mounted ? <div data-testid="scroller" ref={scroller} /> : null;
}

function scrollTo(node: HTMLElement, top: number, height = 2000) {
	Object.defineProperty(node, "scrollHeight", { configurable: true, value: height });
	Object.defineProperty(node, "clientHeight", { configurable: true, value: 500 });
	node.scrollTop = top;
	node.dispatchEvent(new Event("scroll"));
}

beforeEach(() => {
	chatScrollMemory.clear();
	setPinnedNow.mockReset();
});

describe("useChatScrollMemory", () => {
	it("remembers a scrolled-up position and restores it on remount", () => {
		const first = render(<Harness sessionId="s1" layoutKey={1} />);
		scrollTo(first.getByTestId("scroller"), 300);
		expect(chatScrollMemory.get("s1")).toBe(300);
		first.unmount();
		const second = render(<Harness sessionId="s1" layoutKey={1} />);
		expect(second.getByTestId("scroller").scrollTop).toBe(300);
		expect(setPinnedNow).toHaveBeenCalledWith(false);
	});

	it("does not remember a chat left at the end, so it keeps following", () => {
		const view = render(<Harness sessionId="s2" layoutKey={1} />);
		scrollTo(view.getByTestId("scroller"), 1500);
		expect(chatScrollMemory.get("s2")).toBeUndefined();
	});

	it("keeps positions per session when the same view switches sessions", () => {
		const view = render(<Harness sessionId="a" layoutKey={1} />);
		scrollTo(view.getByTestId("scroller"), 200);
		view.rerender(<Harness sessionId="b" layoutKey={1} />);
		scrollTo(view.getByTestId("scroller"), 700);
		view.rerender(<Harness sessionId="a" layoutKey={1} />);
		expect(view.getByTestId("scroller").scrollTop).toBe(200);
	});

	it("keeps a side chat and its parent main chat apart", () => {
		const view = render(<Harness sessionId="main:s1" layoutKey={1} />);
		scrollTo(view.getByTestId("scroller"), 400);
		view.rerender(<Harness sessionId="side:x" layoutKey={1} />);
		scrollTo(view.getByTestId("scroller"), 900);
		view.rerender(<Harness sessionId="main:s1" layoutKey={1} />);
		expect(view.getByTestId("scroller").scrollTop).toBe(400);
	});

	it("unpins and restores when the scroller only appears on a later layout", () => {
		chatScrollMemory.set("late", 350);
		const view = render(<Harness sessionId="late" layoutKey={1} mounted={false} />);
		expect(setPinnedNow).not.toHaveBeenCalled();
		view.rerender(<Harness sessionId="late" layoutKey={2} />);
		expect(setPinnedNow).toHaveBeenCalledWith(false);
		expect(view.getByTestId("scroller").scrollTop).toBe(350);
	});
});
