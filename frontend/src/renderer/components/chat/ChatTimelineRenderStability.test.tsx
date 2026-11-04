import { createElement, memo, useContext, type ComponentType } from "react";
import { describe, expect, it, vi } from "vitest";
import { chatFixtureSettled } from "../../lib/chat-fixture";
import { renderChat as render } from "../../test/render-chat";
import { ChatPromptSender } from "./ChatTimelineItems";
import { ChatWorkspace } from "./ChatWorkspace";

const seen: unknown[] = [];
let timelineRenders = 0;

vi.mock("./Timeline", async (importOriginal) => {
	const mod = await importOriginal<typeof import("./Timeline")>();
	const Impl = (mod.Timeline as unknown as { type: ComponentType<Record<string, unknown>> }).type;
	return {
		...mod,
		// Same shallow-compare memo as the real export, wrapped to count renders and observe the context.
		Timeline: memo(function TimelineCounted(props: Record<string, unknown>) {
			timelineRenders++;
			seen.push(useContext(ChatPromptSender));
			return createElement(Impl, props);
		}),
	};
});

describe("chat render stability", () => {
	it("keeps ChatPromptSender identical and Timeline un-rerendered across parent renders", () => {
		seen.length = 0;
		timelineRenders = 0;
		const snapshot = chatFixtureSettled;
		const el = () => <ChatWorkspace snapshot={snapshot} onSend={vi.fn()} onRollback={vi.fn()} />;
		const view = render(el());
		const initial = timelineRenders;
		expect(initial).toBeGreaterThan(0);
		view.rerender(el());
		view.rerender(el());
		expect(timelineRenders).toBe(initial);
		expect(typeof seen[0]).toBe("function");
		expect(new Set(seen).size).toBe(1);
	});
});
