import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ConversationMessage } from "../../../types/conversation";
import { AssistantMessage } from "../ChatTimelineItems";
import { TooltipProvider } from "../../ui/tooltip";

let nextFrame = 1;
let frames = new Map<number, FrameRequestCallback>();

function message(overrides: Partial<ConversationMessage> = {}): ConversationMessage {
	return {
		kind: "message", id: "assistant-1", sequence: 1, revision: 1, role: "assistant",
		origin: "provider", text: "a", streaming: true, createdAt: "2026-08-24T00:00:00Z", ...overrides,
	};
}

const ui = (m: ConversationMessage) => <TooltipProvider><AssistantMessage message={m} /></TooltipProvider>;

beforeEach(() => {
	nextFrame = 1;
	frames = new Map();
	vi.spyOn(window, "requestAnimationFrame").mockImplementation((cb) => {
		const id = nextFrame++;
		frames.set(id, cb);
		return id;
	});
	vi.spyOn(window, "cancelAnimationFrame").mockImplementation((id) => void frames.delete(id));
});

afterEach(() => {
	vi.useRealTimers();
	vi.restoreAllMocks();
});

describe("AssistantMessage long streaming text", () => {
	it("coalesces long snapshots and cancels the queued update on unmount", () => {
		vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
		const text = "x".repeat(4001);
		const view = render(ui(message({ text })));
		view.rerender(ui(message({ text: text + " a" })));
		view.rerender(ui(message({ text: text + " ab" })));
		expect(document.querySelector("p")?.textContent).toBe(text);
		act(() => vi.advanceTimersByTime(100));
		expect(document.querySelector("p")?.textContent).toBe(text + " ab");
		expect(frames.size).toBe(0);
		view.rerender(ui(message({ text: text + " abc" })));
		expect(vi.getTimerCount()).toBeGreaterThan(0);
		view.unmount();
		expect(vi.getTimerCount()).toBe(0);
	});

	it("resumes smoothing after a long message is corrected to a short snapshot", () => {
		const view = render(ui(message({ text: "x".repeat(4001) })));
		view.rerender(ui(message({ text: "Corrected" })));
		view.rerender(ui(message({ text: "Corrected text" })));
		expect(frames.size).toBeGreaterThan(0);
		let now = 0;
		while (frames.size && now < 5000) {
			const [id, cb] = frames.entries().next().value!;
			frames.delete(id);
			act(() => cb((now += 16)));
		}
		expect(document.querySelector("p")?.textContent).toBe("Corrected text");
	});
});
