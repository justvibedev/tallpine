import { render, renderComposer, composerWireText } from "./ChatComposer.test.setup";
import { ChatComposer } from "./ChatComposer";
import { readChatSessionDraft } from "../../lib/chat-drafts";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";

const BIG = "line one\nline two\nline three\nline four";

function paste(field: HTMLElement, text: string) {
	fireEvent.paste(field, { clipboardData: { files: [], items: [], getData: () => text, setData: () => undefined } });
}

describe("large paste chips", () => {
	it("collapses a 3+ line paste into a numbered chip and sends the full text", async () => {
		const { field, onSend } = renderComposer();
		paste(field, BIG);
		const chip = await waitFor(() => {
			const node = field.querySelector('[data-composer-token="paste"]');
			expect(node).not.toBeNull();
			return node as HTMLElement;
		});
		expect(chip).toHaveTextContent("Paste #1 · 4 lines");
		expect(field.textContent).not.toContain("line two");
		expect(composerWireText(field).trim()).toBe(BIG);
		fireEvent.click(screen.getByRole("button", { name: /send/i }));
		await waitFor(() => expect(onSend).toHaveBeenCalled());
		expect(onSend.mock.calls[0]![0]).toBe(BIG);
	});

	it("collapses a long single line and numbers a second paste #2", async () => {
		const { field } = renderComposer();
		paste(field, "x".repeat(200));
		await waitFor(() => expect(field.querySelectorAll('[data-composer-token="paste"]')).toHaveLength(1));
		paste(field, BIG);
		await waitFor(() => expect(field.querySelectorAll('[data-composer-token="paste"]')).toHaveLength(2));
		expect(field.textContent).toContain("Paste #2 · 4 lines");
	});

	it("labels a single long line in the singular", async () => {
		const { field } = renderComposer();
		paste(field, "x".repeat(200));
		await waitFor(() => expect(field.textContent).toContain("Paste #1 · 1 line"));
		expect(field.textContent).not.toContain("1 lines");
	});

	it("leaves small pastes inline", async () => {
		const { field } = renderComposer();
		paste(field, "a\nb");
		expect(field.querySelector('[data-composer-token="paste"]')).toBeNull();
	});

	it("expands the chip back to text on double-click", async () => {
		const { field } = renderComposer();
		paste(field, BIG);
		const chip = await waitFor(() => {
			const node = field.querySelector('[data-composer-token="paste"]');
			expect(node).not.toBeNull();
			return node as HTMLElement;
		});
		fireEvent.doubleClick(chip);
		await waitFor(() => expect(field.querySelector('[data-composer-token="paste"]')).toBeNull());
		expect(composerWireText(field).trim()).toBe(BIG);
	});

	it("expands a focused chip on Enter without sending", async () => {
		const { field, onSend } = renderComposer();
		paste(field, BIG);
		const chip = await waitFor(() => {
			const node = field.querySelector('[data-composer-token="paste"]');
			expect(node).not.toBeNull();
			return node as HTMLElement;
		});
		chip.focus();
		fireEvent.keyDown(chip, { key: "Enter" });
		await waitFor(() => expect(field.querySelector('[data-composer-token="paste"]')).toBeNull());
		expect(composerWireText(field).trim()).toBe(BIG);
		await new Promise((resolve) => setTimeout(resolve, 100));
		expect(onSend).not.toHaveBeenCalled();
	});

	it("pastes raw after Cmd+Shift+V", async () => {
		const { field } = renderComposer();
		fireEvent.keyDown(field, { key: "v", metaKey: true, shiftKey: true });
		paste(field, BIG);
		expect(field.querySelector('[data-composer-token="paste"]')).toBeNull();
	});

	it("keeps the full pasted text in the saved draft and restores it as plain text", async () => {
		const sessionId = "paste-draft-session";
		const first = render(<ChatComposer onSend={() => {}} draftSessionId={sessionId} />);
		paste(screen.getByLabelText("Message the agent"), BIG);
		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.text.trim()).toBe(BIG));
		first.unmount();
		render(<ChatComposer onSend={() => {}} draftSessionId={sessionId} />);
		expect(composerWireText(screen.getByLabelText("Message the agent")).trim()).toBe(BIG);
	});
});
