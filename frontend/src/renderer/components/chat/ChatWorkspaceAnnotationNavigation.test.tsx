/**
 * Navigating to a quoted annotation: the transcript finds the source message,
 * highlights the quoted text, and says so when the source cannot be found.
 */

import { screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { chatFixtureEmpty } from "../../lib/chat-fixture";
import type { ConversationMessage, ConversationSnapshot } from "../../types/conversation";
import { renderChat as render } from "../../test/render-chat";

const message = (id: string, role: "user" | "assistant", sequence: number, text: string): ConversationMessage => ({
	kind: "message", id, turnId: "t1", sequence, revision: 0, role, origin: role === "user" ? "human" : "provider",
	streaming: false, createdAt: "2026-10-22T03:00:00Z", text,
});

const snapshot: ConversationSnapshot = {
	...chatFixtureEmpty,
	latestSequence: 2,
	turns: [{ id: "t1", state: "completed", providerTurnId: "p1", requestedAt: "2026-10-22T03:00:00Z", completedAt: "2026-10-22T03:00:01Z" }],
	items: [
		message("m1", "user", 1, "Why is the build slow?"),
		message("m2", "assistant", 2, "The linker dominates the build time."),
	],
};

const withRequest = (request: { id: string; text: string; messageId?: string }) => (
	<ChatWorkspace snapshot={snapshot} annotationNavigationRequest={request} />
);

/** jsdom has no element scrolling; record the centering the jump asks for. */
function stubScrolling() {
	const intoView = vi.spyOn(Element.prototype, "scrollIntoView").mockImplementation(() => {});
	const scrollTo = vi.fn();
	Element.prototype.scrollTo = scrollTo as unknown as typeof Element.prototype.scrollTo;
	return { intoView, scrollTo };
}

describe("annotation navigation", () => {
	afterEach(() => {
		vi.restoreAllMocks();
		delete (Element.prototype as { scrollTo?: unknown }).scrollTo;
	});

	it("highlights the quoted text in the referenced message", async () => {
		const { intoView, scrollTo } = stubScrolling();
		render(withRequest({ id: "r1", text: "linker dominates", messageId: "m2" }));

		await waitFor(() => expect(document.querySelector(".chat-annotation-target")).not.toBeNull());
		expect(document.querySelector(".chat-annotation-target")).toHaveTextContent("The linker dominates the build time.");
		await waitFor(() => expect(scrollTo).toHaveBeenCalled());
		expect(intoView).toHaveBeenCalled();
		expect(screen.queryByRole("status")).not.toBeInTheDocument();
	});

	it("finds the source by its text when the reference carries no message id", async () => {
		stubScrolling();
		render(withRequest({ id: "r2", text: "Why is the build slow?" }));

		await waitFor(() => expect(document.querySelector(".chat-annotation-target")).not.toBeNull());
		expect(document.querySelector(".chat-annotation-target")).toHaveTextContent("Why is the build slow?");
	});

	it("says so when the referenced message is not in this chat", async () => {
		render(withRequest({ id: "r3", text: "gone", messageId: "missing" }));

		expect(await screen.findByText("The referenced message is no longer available in this chat.")).toBeInTheDocument();
		expect(document.querySelector(".chat-annotation-target")).toBeNull();
	});

	it("says so when a reference without a message id matches nothing", async () => {
		render(withRequest({ id: "r4", text: "text that appears nowhere" }));

		expect(await screen.findByText(/This annotation's source is unavailable/)).toBeInTheDocument();
	});
});
