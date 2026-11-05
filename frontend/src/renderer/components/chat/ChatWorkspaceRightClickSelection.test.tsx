// The setup module registers vi.mock calls, so it must be imported before ChatWorkspace.
import { cleanupChatWorkspaceTest, resetChatWorkspaceMocks } from "./ChatWorkspace.test.setup";
import { fireEvent, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { chatFixtureSettled } from "../../lib/chat-fixture";
import { renderChat as render } from "../../test/render-chat";

beforeEach(resetChatWorkspaceMocks);

afterEach(async () => {
	vi.restoreAllMocks();
	await cleanupChatWorkspaceTest();
});

describe("ChatWorkspace right-click selection", () => {
	it("does not show the floating Add to chat action for a right-click, so link and path menus stay usable", () => {
		render(<ChatWorkspace snapshot={chatFixtureSettled} excerptsEnabled />);
		const log = screen.getByRole("log", { name: "Conversation" });
		const range = document.createRange();
		range.selectNodeContents(log.querySelector("[data-chat-message-id] [data-chat-message-body]")!);
		const selection = window.getSelection()!;
		selection.removeAllRanges();
		selection.addRange(range);
		vi.spyOn(Range.prototype, "getClientRects").mockReturnValue([new DOMRect(40, 150, 100, 20)] as unknown as DOMRectList);
		vi.spyOn(log, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 0, 600, 500));
		fireEvent.mouseUp(log, { button: 2 });
		expect(screen.queryByRole("button", { name: "Add to chat" })).not.toBeInTheDocument();
		fireEvent.mouseUp(log, { button: 0 });
		expect(screen.getByRole("button", { name: "Add to chat" })).toBeInTheDocument();
		selection.removeAllRanges();
	});
});
