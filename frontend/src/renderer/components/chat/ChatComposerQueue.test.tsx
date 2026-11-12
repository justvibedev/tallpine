import {
	render,
	SKILLS,
	renderComposer,
	deferred,
	typeInComposer,
	composerWireText,
	clipboardData,
	png,
} from "./ChatComposer.test.setup";

import { appI18n } from "../../i18n/instance";
import { purgeFileAttachmentsForSession } from "../../hooks/useFileAttachments";
import {
	act,
	fireEvent,
	screen,
	waitFor,
	within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { ChatComposer } from "./ChatComposer";
import { attachmentURL } from "@tallpine/shared/chat/attachments";
import { getApiBaseUrl } from "../../lib/api-client";
import {
	activateChatDraftScope,
	prepareChatComposerDelivery,
	readChatSessionDraft,
	writeChatComposerText,
	writeChatAttachments,
} from "../../lib/chat-drafts";
import { getChatDraftBoundaries } from "../../lib/chat-draft-boundary";
import { placeLexicalCaret, placeLexicalCaretAtEnd, placeLexicalCaretInLine } from "../../test/lexical";



describe("queued message edit", () => {
	it("saves a queued edit while the composer is busy", async () => {
		const onSend = vi.fn().mockResolvedValue(undefined);
		render(
			<ChatComposer
				onSend={onSend}
				busy
				willQueue
				editingQueuedTurnId="queued-1"
				draftSeed={{ id: "queued-1", text: "hi" }}
			/>,
		);
		await waitFor(() => expect(screen.getByLabelText("Message the agent")).toHaveTextContent("hi"));
		await userEvent.keyboard("{Enter}");
			await waitFor(() => expect(onSend).toHaveBeenCalledWith("hi", undefined, undefined, []));
	});
});

describe("sent message recall", () => {
	const SENT = ["first question", "second question", "latest question"];
	/** Fires one arrow key; true means the composer claimed it rather than moving the caret. */
	const press = (field: HTMLElement, key: "ArrowUp" | "ArrowDown", init: KeyboardEventInit = {}) =>
		!fireEvent.keyDown(field, { key, ...init });

	it("walks sent messages newest first from an empty composer and back to the empty draft", async () => {
		const { field, onSend } = renderComposer({ sentMessages: SENT });
		await typeInComposer(field, "");

		expect(press(field, "ArrowUp")).toBe(true);
		expect(composerWireText(field)).toBe("latest question");
		press(field, "ArrowUp");
		expect(composerWireText(field)).toBe("second question");
		press(field, "ArrowUp");
		expect(composerWireText(field)).toBe("first question");
		// The oldest message is the end of the walk; the key falls through untouched.
		expect(press(field, "ArrowUp")).toBe(false);
		expect(composerWireText(field)).toBe("first question");

		expect(press(field, "ArrowDown")).toBe(true);
		expect(composerWireText(field)).toBe("second question");
		press(field, "ArrowDown");
		expect(composerWireText(field)).toBe("latest question");
		press(field, "ArrowDown");
		expect(composerWireText(field)).toBe("");
		expect(press(field, "ArrowDown")).toBe(false);
		expect(onSend).not.toHaveBeenCalled();
	});

	it("starts from a typed draft only at its start and brings the draft back", async () => {
		const { field } = renderComposer({ sentMessages: SENT });
		await typeInComposer(field, "my draft");

		// Mid-text, Up is still caret movement.
		expect(press(field, "ArrowUp")).toBe(false);
		expect(composerWireText(field)).toBe("my draft");

		await placeLexicalCaret(field, 0);
		expect(press(field, "ArrowUp")).toBe(true);
		expect(composerWireText(field)).toBe("latest question");
		expect(press(field, "ArrowDown")).toBe(true);
		expect(composerWireText(field)).toBe("my draft");
		expect(screen.getByRole("button", { name: "Send message" })).toBeEnabled();
	});

	it("leaves the arrows to a text selection, even one starting at the very start", async () => {
		const { field } = renderComposer({ sentMessages: SENT });
		await typeInComposer(field, "my draft");
		const text = field.querySelector("[data-lexical-text]")?.firstChild;
		if (!text) throw new Error("Expected composer text");
		window.getSelection()?.setBaseAndExtent(text, 0, text, 2);

		expect(press(field, "ArrowUp")).toBe(false);
		expect(composerWireText(field)).toBe("my draft");
	});

	it("reads the caret the browser shows before Lexical has caught up with it", async () => {
		const { field } = renderComposer({ sentMessages: SENT });
		await typeInComposer(field, "my draft");
		// A native Up on the only line moves the caret to its start; Lexical adopts that
		// on a later selectionchange, which the next key can beat.
		const text = field.querySelector("[data-lexical-text]")?.firstChild;
		if (!text) throw new Error("Expected composer text");
		window.getSelection()?.collapse(text, 0);

		expect(press(field, "ArrowUp")).toBe(true);
		expect(composerWireText(field)).toBe("latest question");
	});

	it("keeps the arrows moving the caret inside multiline text", async () => {
		const sessionId = "composer-history-multiline";
		writeChatComposerText(sessionId, "line one\nline two");
		const { field } = renderComposer({
			draftSessionId: sessionId,
			sentMessages: ["older\nmultiline ask", "newest ask"],
		});

		await placeLexicalCaretInLine(field, 1, 3);
		expect(press(field, "ArrowUp")).toBe(false);
		expect(press(field, "ArrowDown")).toBe(false);
		await placeLexicalCaretAtEnd(field);
		expect(press(field, "ArrowUp")).toBe(false);
		expect(press(field, "ArrowDown")).toBe(false);
		expect(composerWireText(field)).toBe("line one\nline two");

		await placeLexicalCaret(field, 0);
		press(field, "ArrowUp");
		press(field, "ArrowUp");
		expect(composerWireText(field)).toBe("older\nmultiline ask");
		// Reading down a recalled message is caret movement; from its end, Down continues.
		await placeLexicalCaretInLine(field, 1, 2);
		expect(press(field, "ArrowDown")).toBe(false);
		expect(press(field, "ArrowUp")).toBe(false);
		await placeLexicalCaretAtEnd(field);
		expect(press(field, "ArrowDown")).toBe(true);
		expect(composerWireText(field)).toBe("newest ask");
	});

	it("leaves the arrows to an open completion menu", async () => {
		const { field } = renderComposer({ skills: SKILLS, sentMessages: ["older", "ask /rev"] });
		await typeInComposer(field, "");
		press(field, "ArrowUp");
		expect(composerWireText(field)).toBe("ask /rev");

		await placeLexicalCaretAtEnd(field);
		const listbox = await screen.findByRole("listbox");
		const before = within(listbox).getAllByRole("option").findIndex((option) => option.getAttribute("aria-selected") === "true");
		expect(press(field, "ArrowDown")).toBe(true);
		expect(composerWireText(field)).toBe("ask /rev");
		expect(within(listbox).getAllByRole("option").findIndex((option) => option.getAttribute("aria-selected") === "true")).toBe(before + 1);
	});

	it("ignores modified arrows and IME composition", async () => {
		const { field } = renderComposer({ sentMessages: SENT });
		await typeInComposer(field, "");

		for (const modifier of ["shiftKey", "altKey", "metaKey", "ctrlKey"] as const) {
			expect(press(field, "ArrowUp", { [modifier]: true })).toBe(false);
		}
		expect(press(field, "ArrowUp", { isComposing: true })).toBe(false);
		fireEvent.compositionStart(field);
		expect(press(field, "ArrowUp")).toBe(false);
		fireEvent.compositionEnd(field);
		expect(composerWireText(field)).toBe("");

		expect(press(field, "ArrowUp")).toBe(true);
		expect(composerWireText(field)).toBe("latest question");
	});

	it("keeps the saved draft while browsing and saves a recalled message once it is edited", async () => {
		const sessionId = "composer-history-durable";
		writeChatComposerText(sessionId, "saved draft");
		const { field } = renderComposer({ draftSessionId: sessionId, sentMessages: SENT });

		await placeLexicalCaret(field, 0);
		press(field, "ArrowUp");
		expect(composerWireText(field)).toBe("latest question");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("saved draft");

		await typeInComposer(field, " again");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("latest question again");
		// Edited, it is the draft now: the walk is over and Down at its end is a no-op.
		expect(press(field, "ArrowDown")).toBe(false);
		await placeLexicalCaret(field, 0);
		expect(press(field, "ArrowDown")).toBe(false);
		expect(composerWireText(field)).toBe("latest question again");
	});

	it("sends a recalled message only on Enter, then starts the next walk afresh", async () => {
		const { field, onSend } = renderComposer({ sentMessages: SENT });
		await typeInComposer(field, "");
		press(field, "ArrowUp");
		press(field, "ArrowUp");
		expect(onSend).not.toHaveBeenCalled();

		fireEvent.keyDown(field, { key: "Enter" });
		await waitFor(() => expect(onSend).toHaveBeenCalledWith("second question"));
		await waitFor(() => expect(composerWireText(field)).toBe(""));
		expect(press(field, "ArrowDown")).toBe(false);
		press(field, "ArrowUp");
		expect(composerWireText(field)).toBe("latest question");
	});

	it("stays out of a queued message edit", async () => {
		const { field } = renderComposer({
			sentMessages: SENT,
			editingQueuedTurnId: "turn-queued",
			draftSeed: { id: "turn-queued:owner", text: "" },
		});
		await typeInComposer(field, "");
		expect(press(field, "ArrowUp")).toBe(false);
		expect(composerWireText(field)).toBe("");
	});

	it("stays out of a disabled composer", async () => {
		const { field } = renderComposer({ sentMessages: SENT, disabled: true });
		await typeInComposer(field, "");
		expect(press(field, "ArrowUp")).toBe(false);
		expect(composerWireText(field)).toBe("");
	});

	it("stays out of a delivery still settling", async () => {
		const sessionId = "composer-history-delivery";
		prepareChatComposerDelivery(sessionId, {
			kind: "steer",
			composerText: "still settling",
			attachments: [],
			requestText: "still settling",
			clientMessageId: "composer-history-delivery-id",
		});
		const { field } = renderComposer({ draftSessionId: sessionId, sentMessages: SENT });
		await placeLexicalCaret(field, 0);
		expect(press(field, "ArrowUp")).toBe(false);
		expect(composerWireText(field)).toBe("still settling");
	});

	it("never completes a recalled skill as the caret moves through it, so the saved draft stays", async () => {
		const sessionId = "composer-history-skill";
		writeChatComposerText(sessionId, "my saved draft");
		const { field } = renderComposer({ draftSessionId: sessionId, skills: SKILLS, sentMessages: ["ask /review"] });
		await placeLexicalCaret(field, 0);
		press(field, "ArrowUp");
		expect(composerWireText(field)).toBe("ask /review");

		// The caret now sits after an exact `/review`, which typing would auto-complete.
		await placeLexicalCaretAtEnd(field);
		await screen.findByRole("listbox");
		expect(field.querySelector('[data-composer-token="skill"]')).toBeNull();
		expect(composerWireText(field)).toBe("ask /review");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("my saved draft");

		// The open menu keeps the arrows; once dismissed, Down walks back to the draft.
		fireEvent.keyDown(field, { key: "Escape" });
		expect(press(field, "ArrowDown")).toBe(true);
		expect(composerWireText(field)).toBe("my saved draft");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("my saved draft");
	});

	it("treats a completion the user accepts in a recalled message as an edit", async () => {
		const sessionId = "composer-history-skill-accepted";
		writeChatComposerText(sessionId, "my saved draft");
		const { field } = renderComposer({ draftSessionId: sessionId, skills: SKILLS, sentMessages: ["ask /rev"] });
		await placeLexicalCaret(field, 0);
		press(field, "ArrowUp");
		await placeLexicalCaretAtEnd(field);
		await screen.findByRole("listbox");

		fireEvent.keyDown(field, { key: "Enter" });
		await waitFor(() => expect(field.querySelector('[data-composer-token="skill"]')).not.toBeNull());
		expect(readChatSessionDraft(sessionId).composer.text).toBe(composerWireText(field));
		expect(composerWireText(field)).not.toBe("my saved draft");
	});

	it("takes an image removed while browsing out of the saved draft as well", async () => {
		const sessionId = "composer-history-removed-image";
		const path = ".tallpine/attachments/attachment-history-removed.png";
		const { field, onSend } = renderComposer({
			draftSessionId: sessionId,
			sentMessages: ["earlier ask"],
			onStageAttachments: vi.fn().mockResolvedValue([path]),
		});
		await typeInComposer(field, "compare");
		fireEvent.paste(field, { clipboardData: clipboardData([png("removed.png")]) });
		await waitFor(() => expect(field.querySelector('[data-composer-token="image"]')).not.toBeNull());
		expect(readChatSessionDraft(sessionId).composer.text).toContain(path);

		await placeLexicalCaret(field, 0);
		press(field, "ArrowUp");
		expect(composerWireText(field)).toBe("earlier ask");
		await userEvent.click(screen.getByLabelText("Remove removed.png"));
		await waitFor(() => expect(screen.queryByLabelText("Remove removed.png")).toBeNull());
		// Still browsing, and leaving now would bring back a draft without the image.
		expect(composerWireText(field)).toBe("earlier ask");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("compare ");

		expect(press(field, "ArrowDown")).toBe(true);
		expect(composerWireText(field)).toBe("compare ");
		fireEvent.keyDown(field, { key: "Enter" });
		await waitFor(() => expect(onSend).toHaveBeenCalledOnce());
		expect(onSend.mock.calls[0]?.[0]).toBe("compare");
		expect(onSend.mock.calls[0]?.[1]).toBeUndefined();
	});

	it("waits for a pasted image to finish staging before recalling, so its chip survives", async () => {
		const path = ".tallpine/attachments/attachment-history-staging.png";
		const staging = deferred<string[]>();
		const { field } = renderComposer({ sentMessages: ["earlier ask"], onStageAttachments: vi.fn(() => staging.promise) });
		await typeInComposer(field, "compare");
		fireEvent.paste(field, { clipboardData: clipboardData([png("staging.png")]) });
		expect(field.querySelector('[data-composer-token="image-pending"]')).not.toBeNull();

		await placeLexicalCaret(field, 0);
		expect(press(field, "ArrowUp")).toBe(false);
		expect(field.querySelector('[data-composer-token="image-pending"]')).not.toBeNull();

		await act(async () => staging.resolve([path]));
		await waitFor(() => expect(field.querySelector('[data-composer-token="image"]')).not.toBeNull());
		await placeLexicalCaret(field, 0);
		expect(press(field, "ArrowUp")).toBe(true);
		expect(composerWireText(field)).toBe("earlier ask");
		expect(press(field, "ArrowDown")).toBe(true);
		expect(field.querySelector('[data-composer-token="image"]')).not.toBeNull();
		expect(composerWireText(field)).toBe(`compare ${path} `);
	});

	it("recalls with Up while a Tab suggestion is offered, which dismisses it like any text", async () => {
		const onDismiss = vi.fn();
		const { field, onSend } = renderComposer({
			sentMessages: SENT,
			promptSuggestion: { id: "suggestion", text: "Run the tests", onDismiss },
		});
		await typeInComposer(field, "");
		expect(press(field, "ArrowUp")).toBe(true);
		expect(composerWireText(field)).toBe("latest question");
		expect(onDismiss).toHaveBeenCalled();
		expect(onSend).not.toHaveBeenCalled();
	});

	it("makes a recalled message the draft when an image is pasted into it", async () => {
		const sessionId = "composer-history-pasted-into-recall";
		const path = ".tallpine/attachments/attachment-history-pasted.png";
		writeChatComposerText(sessionId, "my draft");
		const { field } = renderComposer({
			draftSessionId: sessionId,
			sentMessages: ["earlier ask"],
			onStageAttachments: vi.fn().mockResolvedValue([path]),
		});
		await placeLexicalCaret(field, 0);
		press(field, "ArrowUp");
		await placeLexicalCaretAtEnd(field);
		fireEvent.paste(field, { clipboardData: clipboardData([png("pasted.png")]) });
		await waitFor(() => expect(field.querySelector('[data-composer-token="image"]')).not.toBeNull());

		expect(composerWireText(field)).toBe(`earlier ask ${path} `);
		expect(readChatSessionDraft(sessionId).composer.text).toBe(`earlier ask ${path} `);
		// The walk ended with the edit, so Down at the end no longer replaces the text.
		expect(press(field, "ArrowDown")).toBe(false);
	});
});

describe("steering", () => {
	function renderSteerable(props: Partial<Parameters<typeof ChatComposer>[0]> = {}) {
		const onSend = vi.fn();
		const onSteer = vi.fn().mockResolvedValue(undefined);
		render(<ChatComposer onSend={onSend} onSteer={onSteer} canSteer willQueue {...props} />);
		return {
			onSend,
			onSteer,
			field: screen.getByLabelText("Message the agent") as HTMLElement,
		};
	}

	it("steers on Cmd+Enter rather than queueing, and trims the body first", async () => {
		const { onSend, onSteer, field } = renderSteerable();

		await typeInComposer(field, "  change course  ");
		await userEvent.keyboard("{Meta>}{Enter}{/Meta}");

		await waitFor(() => expect(onSteer).toHaveBeenCalledWith("change course"));
		expect(onSend).not.toHaveBeenCalled();
	});

	it("offers an explicit steer action for a running Cloud turn", async () => {
		const { onSend, onSteer, field } = renderSteerable({ showSteerButton: true });
		await typeInComposer(field, "change course");
		await userEvent.click(screen.getByRole("button", { name: "Steer into running turn" }));
		await waitFor(() => expect(onSteer).toHaveBeenCalledWith("change course"));
		expect(onSend).not.toHaveBeenCalled();
	});

	it("steers on Ctrl+Enter, so the chord exists off macOS too", async () => {
		const { onSend, onSteer, field } = renderSteerable();

		await typeInComposer(field, "change course");
		await userEvent.keyboard("{Control>}{Enter}{/Control}");

		await waitFor(() => expect(onSteer).toHaveBeenCalledWith("change course"));
		expect(onSend).not.toHaveBeenCalled();
	});

	it("queues on a bare Enter even while a turn is running", async () => {
		const { onSend, onSteer, field } = renderSteerable();

		await typeInComposer(field, "wait your turn");
		await userEvent.keyboard("{Enter}");

		await waitFor(() => expect(onSend).toHaveBeenCalledWith("wait your turn"));
		expect(onSteer).not.toHaveBeenCalled();
	});

	// The modifier is a request, not a guarantee: a harness that cannot steer must
	// still deliver the message rather than swallow the keystroke.
	it("falls back to queueing on Cmd+Enter when the harness cannot steer", async () => {
		const onSend = vi.fn();
		render(<ChatComposer onSend={onSend} willQueue />);
		const field = screen.getByLabelText("Message the agent") as HTMLElement;

		await typeInComposer(field, "no steering here");
		await userEvent.keyboard("{Meta>}{Enter}{/Meta}");

		await waitFor(() => expect(onSend).toHaveBeenCalledWith("no steering here"));
	});

	// The indicator beside the send control is painted from a window-level view of
	// the modifier, so clicking has to read that same state or the button would
	// contradict the label sitting next to it.
	it("steers when the send control is clicked with a modifier held", async () => {
		const { onSend, onSteer, field } = renderSteerable();

		await typeInComposer(field, "pointer agrees with the chip");
		expect(screen.getByRole("button", { name: "Queue message" })).toBeEnabled();
		act(() => {
			window.dispatchEvent(new KeyboardEvent("keydown", { key: "Meta", metaKey: true }));
		});

		await userEvent.click(screen.getByRole("button", { name: "Steer message" }));

		await waitFor(() => expect(onSteer).toHaveBeenCalledWith("pointer agrees with the chip"));
		expect(onSend).not.toHaveBeenCalled();
	});

	it("releasing the modifier returns the send control to queueing", async () => {
		const { onSend, onSteer, field } = renderSteerable();

		await typeInComposer(field, "back to the queue");
		act(() => {
			window.dispatchEvent(new KeyboardEvent("keydown", { key: "Meta", metaKey: true }));
		});
		act(() => {
			window.dispatchEvent(new KeyboardEvent("keyup", { key: "Meta", metaKey: false }));
		});

		await userEvent.click(screen.getByRole("button", { name: "Queue message" }));

		await waitFor(() => expect(onSend).toHaveBeenCalledWith("back to the queue"));
		expect(onSteer).not.toHaveBeenCalled();
	});

	// A refused steer is an ordinary outcome — the turn may have ended mid-keystroke —
	// so the draft has to survive for the user to send it the other way.
	it("keeps the draft when the provider refuses the steer", async () => {
		const onSend = vi.fn();
		const onSteer = vi.fn().mockRejectedValue(new Error("the turn already finished"));
		render(<ChatComposer onSend={onSend} onSteer={onSteer} canSteer willQueue />);
		const field = screen.getByLabelText("Message the agent") as HTMLElement;

		await typeInComposer(field, "do not lose this");
		await userEvent.keyboard("{Meta>}{Enter}{/Meta}");

		await waitFor(() => expect(onSteer).toHaveBeenCalledWith("do not lose this"));
		expect(field.textContent).toBe("do not lose this");
		expect(onSend).not.toHaveBeenCalled();
	});

	it("durably unlocks a typed refused steer without offering delivery retry after remount", async () => {
		const sessionId = "steer-definitive-refusal";
		const onSteer = vi.fn().mockResolvedValue({
			status: "not-accepted",
			reason: "The turn finished before this landed. Send it as a message instead.",
		});
		const first = render(
			<ChatComposer
				onSend={vi.fn()}
				onSteer={onSteer}
				canSteer
				willQueue
				draftSessionId={sessionId}
			/>,
		);
		const field = screen.getByLabelText("Message the agent");
		await typeInComposer(field, "send this normally instead");
		fireEvent.keyDown(field, { key: "Enter", ctrlKey: true });

		await waitFor(() => expect(onSteer).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(field).toHaveAttribute("contenteditable", "true"));
		expect(field).toHaveTextContent("send this normally instead");
		expect(screen.queryByRole("button", { name: "Retry message safely" })).not.toBeInTheDocument();
		const restored = readChatSessionDraft(sessionId);
		expect(restored.composer.text).toBe("send this normally instead");
		expect(restored.composer.delivery).toBeUndefined();

		first.unmount();
		render(
			<ChatComposer
				onSend={vi.fn()}
				onSteer={onSteer}
				canSteer
				willQueue
				draftSessionId={sessionId}
			/>,
		);
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent(
			"send this normally instead",
		);
		expect(screen.getByLabelText("Message the agent")).toHaveAttribute("contenteditable", "true");
		expect(screen.queryByRole("button", { name: "Retry message safely" })).not.toBeInTheDocument();
		expect(onSteer).toHaveBeenCalledTimes(1);
	});

	it("restores an unresolved steer and retries with its original client id", async () => {
		const sessionId = "steer-stable-retry";
		const onSteer = vi
			.fn()
			.mockRejectedValueOnce(new Error("outcome unknown"))
			.mockResolvedValueOnce(undefined);
		const first = render(
			<ChatComposer
				onSend={vi.fn()}
				onSteer={onSteer}
				canSteer
				willQueue
				draftSessionId={sessionId}
			/>,
		);
		const field = screen.getByLabelText("Message the agent");
		await typeInComposer(field, "persist this steer");
		fireEvent.keyDown(field, { key: "Enter", ctrlKey: true });
		await waitFor(() => expect(onSteer).toHaveBeenCalledTimes(1));
		const clientMessageId = onSteer.mock.calls[0]?.[2];
		expect(clientMessageId).toEqual(expect.any(String));
		first.unmount();

		render(
			<ChatComposer
				onSend={vi.fn()}
				onSteer={onSteer}
				canSteer
				willQueue
				draftSessionId={sessionId}
			/>,
		);
		expect(screen.getByLabelText("Message the agent")).toHaveAttribute(
			"contenteditable",
			"false",
		);
		await userEvent.click(screen.getByRole("button", { name: "Retry message safely" }));
		await waitFor(() => expect(onSteer).toHaveBeenCalledTimes(2));
		expect(onSteer.mock.calls[1]?.[2]).toBe(clientMessageId);
		await waitFor(() => expect(screen.getByLabelText("Message the agent")).toHaveTextContent(""));
	});

	it("warns before explicitly abandoning uncertain steer recovery across a remount", async () => {
		const sessionId = "steer-explicit-abandon";
		const incarnation = "2026-08-26T10:00:00.000Z";
		const onSteer = vi.fn().mockRejectedValue(new Error("outcome unknown"));
		const props = {
			onSend: vi.fn(),
			onSteer,
			canSteer: true,
			willQueue: true,
			draftSessionId: sessionId,
			draftSessionIncarnation: incarnation,
		};
		const first = render(<ChatComposer {...props} />);
		const field = screen.getByLabelText("Message the agent");
		await typeInComposer(field, "possibly delivered guidance");
		fireEvent.keyDown(field, { key: "Enter", ctrlKey: true });

		await waitFor(() => expect(onSteer).toHaveBeenCalledTimes(1));
		await waitFor(() =>
			expect(screen.getByRole("alert")).toHaveTextContent(
				"may already have received this guidance",
			),
		);
		expect(screen.getByRole("alert")).toHaveTextContent("may duplicate it");
		expect(field).toHaveAttribute("contenteditable", "false");
		first.unmount();

		render(<ChatComposer {...props} />);
		const restored = screen.getByLabelText("Message the agent");
		expect(restored).toHaveTextContent("possibly delivered guidance");
		expect(restored).toHaveAttribute("contenteditable", "false");
		expect(onSteer).toHaveBeenCalledTimes(1);

		await userEvent.click(screen.getByRole("button", { name: "Abandon recovery" }));

		await waitFor(() => expect(restored).toHaveAttribute("contenteditable", "true"));
		expect(restored).toHaveTextContent("possibly delivered guidance");
		expect(onSteer).toHaveBeenCalledTimes(1);
		expect(screen.queryByRole("button", { name: "Retry message safely" })).not.toBeInTheDocument();
		expect(screen.getByRole("status")).toHaveTextContent("may already have been delivered");
		const draft = readChatSessionDraft({ sessionId, incarnation });
		expect(draft.composer.text).toBe("possibly delivered guidance");
		expect(draft.composer.delivery).toBeUndefined();
	});

	it.each([false, true])("keeps live steer completion owned until its response settles (response lost: %s)", async (responseLost) => {
		const sessionId = `steer-live-snapshot-${responseLost}`;
		const pending = deferred<void>();
		const onSteer = vi.fn(async () => {
			await pending.promise;
			if (responseLost) throw new Error("response lost");
		});
		const props = {
			onSend: vi.fn().mockResolvedValue(undefined),
			onSteer,
			canSteer: true,
			willQueue: true,
			draftSessionId: sessionId,
		};
		const view = render(<ChatComposer {...props} />);
		const field = screen.getByLabelText("Message the agent");
		await typeInComposer(field, "steer exactly once");
		fireEvent.keyDown(field, { key: "Enter", ctrlKey: true });
		await waitFor(() => expect(onSteer).toHaveBeenCalledOnce());
		const clientMessageId = readChatSessionDraft(sessionId).composer.delivery!.clientMessageId;

		view.rerender(<ChatComposer {...props} acceptedClientMessageIds={new Set([clientMessageId])} />);
		expect(field).toHaveAttribute("contenteditable", "false");
		expect(readChatSessionDraft(sessionId).composer.delivery?.state).toBe("dispatching");
		await act(async () => pending.resolve());

		await waitFor(() => expect(field).toHaveAttribute("contenteditable", "true"));
		expect(field.textContent).toBe("");
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined();
		expect(onSteer).toHaveBeenCalledOnce();
	});

	it("reconciles a restored steer from the daemon snapshot without redispatch", async () => {
		const sessionId = "steer-snapshot-reconciliation";
		const onSteer = vi.fn().mockRejectedValue(new Error("response lost"));
		const first = render(
			<ChatComposer
				onSend={vi.fn()}
				onSteer={onSteer}
				canSteer
				willQueue
				draftSessionId={sessionId}
			/>,
		);
		const field = screen.getByLabelText("Message the agent");
		await typeInComposer(field, "snapshot-confirmed steer");
		fireEvent.keyDown(field, { key: "Enter", ctrlKey: true });
		await waitFor(() => expect(onSteer).toHaveBeenCalledTimes(1));
		const clientMessageId = onSteer.mock.calls[0]?.[2] as string;
		first.unmount();

		render(
			<ChatComposer
				onSend={vi.fn()}
				onSteer={onSteer}
				canSteer
				willQueue
				draftSessionId={sessionId}
				acceptedClientMessageIds={new Set([clientMessageId])}
			/>,
		);
		await waitFor(() => expect(screen.getByLabelText("Message the agent")).toHaveTextContent(""));
		expect(onSteer).toHaveBeenCalledTimes(1);
		expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined();
	});

	it("clears a restored send the chat already holds, without the restart notice", async () => {
		const sessionId = "send-snapshot-reconciliation";
		// The answer is lost, as when the app restarts mid-send.
		const onSend = vi.fn().mockRejectedValue(new Error("response lost"));
		const first = render(<ChatComposer onSend={onSend} draftSessionId={sessionId} />);
		const field = screen.getByLabelText("Message the agent");
		await typeInComposer(field, "sent before the restart");
		fireEvent.keyDown(field, { key: "Enter" });
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery?.state).toBe("dispatching"));
		const clientMessageId = onSend.mock.calls[0]?.[2] as unknown as string;
		first.unmount();

		render(<ChatComposer onSend={onSend} draftSessionId={sessionId} acceptedClientMessageIds={new Set([clientMessageId])} />);
		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent("");
		expect(screen.queryByText(/wasn.t confirmed before Chat restarted/)).not.toBeInTheDocument();
		expect(onSend).toHaveBeenCalledTimes(1);
	});
});

/* ---- slash commands ------------------------------------------------------ */

describe("unavailable states", () => {
	it("explains a stopped controller and refuses to send", async () => {
		const { onSend, field } = renderComposer({ disabled: true, skills: SKILLS });
		expect(field).toHaveAttribute("aria-placeholder", expect.stringContaining("not connected"));
		expect(field).toHaveAttribute("contenteditable", "false");
		expect(field).toHaveAttribute("aria-readonly", "true");
		expect(field).toHaveAttribute("tabindex", "-1");
		await userEvent.keyboard("{Enter}");
		expect(onSend).not.toHaveBeenCalled();
	});

	it("does not show a loading spinner when no queued edit is saving", () => {
		renderComposer();
		expect(
			screen.getByRole("button", { name: "Send message" }).querySelector(".animate-spin"),
		).not.toBeInTheDocument();
	});

	it("shows a loading spinner only while the queued edit being edited is saving", () => {
		renderComposer({ editingQueuedTurnId: "turn-1", savingQueuedEditPending: true });
		expect(
			screen.getByRole("button", { name: "Send message" }).querySelector(".animate-spin"),
		).toBeInTheDocument();
	});

	it("says a mid-turn message will be held", () => {
		const { field } = renderComposer({ willQueue: true });
		expect(field).toHaveAttribute(
			"aria-placeholder",
			"Next message you send will be queued",
		);
	});

	it("turns the primary composer action into stop while the agent is working and the draft is empty", async () => {
		const onSend = vi.fn();
		const onInterrupt = vi.fn();
		render(<ChatComposer onSend={onSend} willQueue onInterrupt={onInterrupt} />);

		await userEvent.click(screen.getByRole("button", { name: "Stop turn" }));

		expect(onInterrupt).toHaveBeenCalledTimes(1);
		expect(onSend).not.toHaveBeenCalled();
	});

	it("keeps the primary action as queue while the agent is working and a draft exists", async () => {
		const onSend = vi.fn();
		const onInterrupt = vi.fn();
		render(<ChatComposer onSend={onSend} willQueue onInterrupt={onInterrupt} />);

		await typeInComposer(screen.getByLabelText("Message the agent"), "follow up");
		await userEvent.click(screen.getByRole("button", { name: "Queue message" }));

		expect(onSend).toHaveBeenCalledWith("follow up");
		expect(onInterrupt).not.toHaveBeenCalled();
	});

	it("explains when a send is blocked by the previous in-flight message", async () => {
		const onSend = vi.fn().mockImplementation(
			() => new Promise<void>(() => {
				/* keep pending */
			}),
		);
		render(<ChatComposer busy onSend={onSend} willQueue />);
		const field = screen.getByLabelText("Message the agent");

		await typeInComposer(field, "follow up");
		await userEvent.keyboard("{Enter}");

		expect(onSend).not.toHaveBeenCalled();
		expect(screen.getByRole("alert")).toHaveTextContent(/still sending the previous message/i);
	});
});

it("shows restored composer recovery notices and actions in the selected language", async () => {
	const sessionId = "composer-localized-recovery";
	prepareChatComposerDelivery(sessionId, { kind: "send", composerText: "bonjour", attachments: [], requestText: "bonjour", clientMessageId: "fr-recovery" });
	const view = render(<ChatComposer draftSessionId={sessionId} onSend={vi.fn()} />);
	try {
		expect(await screen.findByRole("alert")).toHaveTextContent("Message delivery wasn’t confirmed");
		await act(async () => { await appI18n.changeLanguage("fr"); });
		expect(await screen.findByRole("alert")).toHaveTextContent("La livraison du message n’a pas été confirmée");
		expect(screen.getByRole("button", { name: "Réessayer sans risque de doublon" })).toBeEnabled();
		expect(getChatDraftBoundaries(sessionId)).toEqual([]);
	} finally {
		view.unmount();
		await appI18n.changeLanguage("en");
	}
});

it("reserves a restored image send across remount and preserves typing during native-byte reads", async () => {
	const sessionId = "composer-reserve-before-native-read";
	writeChatComposerText(sessionId, "inspect restored image");
	writeChatAttachments(sessionId, [{ id: "restored-image", name: "restored.png", mimeType: "image/png", bytes: 4, path: ".tallpine/attachments/restored.png" }]);
	const pending = deferred<Response>();
	const fetch = vi.spyOn(globalThis, "fetch").mockReturnValue(pending.promise);
	const onSend = vi.fn().mockResolvedValue(undefined);
	let view = render(<ChatComposer onSend={onSend} draftSessionId={sessionId} nativeImages />);
	try {
		await userEvent.click(screen.getByRole("button", { name: "Send message" }));
		await waitFor(() => expect(fetch).toHaveBeenCalledOnce());
		expect(onSend).not.toHaveBeenCalled();
		const delivery = readChatSessionDraft(sessionId).composer.delivery;
		expect(delivery).toMatchObject({ state: "dispatching", nativeImages: true, clientMessageId: expect.any(String) });
		view.unmount();
		view = render(<ChatComposer onSend={onSend} draftSessionId={sessionId} nativeImages />);
		const field = screen.getByLabelText("Message the agent");
		expect(field.textContent).toBe("");
		expect(field).toHaveAttribute("contenteditable", "true");
		expect(getChatDraftBoundaries(sessionId)).toEqual([]);
		await typeInComposer(field, "next draft during image read");
		const response = new Response();
		vi.spyOn(response, "blob").mockResolvedValue(new Blob([new Uint8Array([137, 80, 78, 71])], { type: "image/png" }));
		await act(async () => { pending.resolve(response); });
		await waitFor(() => expect(onSend).toHaveBeenCalledOnce());
		expect(onSend).toHaveBeenCalledWith(
			expect.stringMatching(/^inspect restored image\n\n---\n.+\n- \[IMG1\] \.tallpine\/attachments\/restored\.png$/),
			[{ mimeType: "image/png", data: "iVBORw==" }],
			delivery?.clientMessageId,
		);
		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		expect(field).toHaveTextContent("next draft during image read");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("next draft during image read");
	} finally {
		view.unmount();
		fetch.mockRestore();
	}
});

it("reserves the composer before pending staging yields to a replacement surface", async () => {
	const sessionId = "composer-stage-reservation";
	const pending = deferred<string[]>();
	const stage = vi.fn(() => pending.promise);
	const send = vi.fn().mockResolvedValue(undefined);
	let view = render(<ChatComposer onSend={send} onStageAttachments={stage} draftSessionId={sessionId} />);
	let field = screen.getByLabelText("Message the agent");
	await typeInComposer(field, "original request");
	fireEvent.paste(field, { clipboardData: clipboardData([png("pending.png")]) });
	await waitFor(() => expect(stage).toHaveBeenCalledOnce());
	fireEvent.keyDown(field, { key: "Enter" });
	view.unmount();
	view = render(<ChatComposer onSend={send} onStageAttachments={stage} draftSessionId={sessionId} />);
	field = screen.getByLabelText("Message the agent");
	expect(field).toHaveAttribute("contenteditable", "false");
	expect(send).not.toHaveBeenCalled();
	await act(async () => { pending.resolve([".tallpine/attachments/pending.png"]); });
	await waitFor(() => expect(send).toHaveBeenCalledOnce());
	await waitFor(() => expect(field).toHaveTextContent(/^$/));
	expect(readChatSessionDraft(sessionId).composer.text).toBe("");
	view.unmount();
});

it("does not let Enter omit an image still staging on the previous surface", async () => {
	const sessionId = "composer-shared-pending-image";
	const pending = deferred<string[]>();
	const stage = vi.fn(() => pending.promise);
	const send = vi.fn().mockResolvedValue(undefined);
	let view = render(<ChatComposer onSend={send} onStageAttachments={stage} draftSessionId={sessionId} />);
	await typeInComposer(screen.getByLabelText("Message the agent"), "inspect the image");
	fireEvent.paste(screen.getByLabelText("Message the agent"), { clipboardData: clipboardData([png("pending.png")]) });
	await waitFor(() => expect(stage).toHaveBeenCalledOnce());
	view.unmount();
	view = render(<ChatComposer onSend={send} onStageAttachments={stage} draftSessionId={sessionId} />);
	fireEvent.keyDown(screen.getByLabelText("Message the agent"), { key: "Enter" });
	await act(async () => { await Promise.resolve(); });
	expect(send).not.toHaveBeenCalled();
	await act(async () => { pending.resolve([".tallpine/attachments/pending.png"]); });
	await screen.findByLabelText("Remove pending.png");
	await userEvent.click(screen.getByRole("button", { name: "Send message" }));
	await waitFor(() => expect(send).toHaveBeenCalledWith(expect.stringContaining(".tallpine/attachments/pending.png"), undefined, expect.any(String)));
	view.unmount();
});

it("keeps staged paths authoritative across a remount until acceptance clears the exact draft", async () => {
	const sessionId = "composer-staged-path-remount";
	const response = deferred<void>();
	const send = vi.fn(() => response.promise);
	const stage = vi.fn().mockResolvedValue([".tallpine/attachments/once.png"]);
	let view = render(<ChatComposer onSend={send} onStageAttachments={stage} draftSessionId={sessionId} />);
	await typeInComposer(screen.getByLabelText("Message the agent"), "inspect once");
	fireEvent.paste(screen.getByLabelText("Message the agent"), { clipboardData: clipboardData([png("once.png")]) });
	await screen.findByLabelText("Remove once.png");
	fireEvent.keyDown(screen.getByLabelText("Message the agent"), { key: "Enter" });
	await waitFor(() => expect(send).toHaveBeenCalledOnce());
	const revision = readChatSessionDraft(sessionId).composer.revision;
	view.unmount();
	view = render(<ChatComposer onSend={send} onStageAttachments={stage} draftSessionId={sessionId} />);
	expect(readChatSessionDraft(sessionId).composer).toMatchObject({
		revision,
		attachments: [],
		delivery: { draft: { attachments: [{ path: ".tallpine/attachments/once.png" }] } },
	});
	await act(async () => { response.resolve(); });
	await waitFor(() => expect(screen.getByLabelText("Message the agent")).toHaveTextContent(/^$/));
	expect(readChatSessionDraft(sessionId).composer.text).toBe("");
	expect(send).toHaveBeenCalledOnce();
	view.unmount();
});

it("saves /compact as literal queued text without running the command", async () => {
	const onSend = vi.fn().mockResolvedValue(undefined);
	const onCompact = vi.fn();
	render(<ChatComposer onSend={onSend} onCompact={onCompact} editingQueuedTurnId="queued-command" draftSeed={{ id: "queued-command", text: "/compact" }} />);
	await userEvent.click(screen.getByRole("button", { name: "Send message" }));
	await waitFor(() => expect(onSend).toHaveBeenCalledWith("/compact", undefined, undefined, []));
	expect(onCompact).not.toHaveBeenCalled();
});

it("does not dispatch a restored image after its session incarnation was replaced", async () => {
	const scope = { sessionId: "composer-obsolete-native-read", incarnation: "2026-09-07T10:00:00Z" };
	const replacement = { ...scope, incarnation: "2026-09-08T10:00:00Z" };
	expect(activateChatDraftScope(scope).ok).toBe(true);
	writeChatComposerText(scope, "old session prompt");
	writeChatAttachments(scope, [{ id: "old-image", name: "old.png", mimeType: "image/png", bytes: 4, path: ".tallpine/attachments/old.png" }]);
	const pending = deferred<Response>();
	const fetch = vi.spyOn(globalThis, "fetch").mockReturnValue(pending.promise);
	const readComplete = deferred<void>();
	const readAsDataURL = FileReader.prototype.readAsDataURL;
	const reader = vi.spyOn(FileReader.prototype, "readAsDataURL").mockImplementation(function (this: FileReader, blob) {
		this.addEventListener("loadend", () => readComplete.resolve(), { once: true });
		readAsDataURL.call(this, blob);
	});
	const onSend = vi.fn();
	const view = render(<ChatComposer draftSessionId={scope.sessionId} draftSessionIncarnation={scope.incarnation} onSend={onSend} nativeImages />);
	try {
		await userEvent.click(screen.getByRole("button", { name: "Send message" }));
		await waitFor(() => expect(fetch).toHaveBeenCalledOnce());
		view.unmount();
		expect(activateChatDraftScope(replacement)).toMatchObject({ ok: true, replaced: true });
		writeChatComposerText(replacement, "replacement draft");
		const response = new Response();
		vi.spyOn(response, "blob").mockResolvedValue(new Blob([new Uint8Array([137, 80, 78, 71])], { type: "image/png" }));
		await act(async () => {
			pending.resolve(response);
			await readComplete.promise;
		});
		expect(onSend).not.toHaveBeenCalled();
		expect(readChatSessionDraft(replacement).composer.text).toBe("replacement draft");
	} finally {
		view.unmount();
		fetch.mockRestore();
		reader.mockRestore();
	}
});


it.each([
	{ name: "local", draftSessionId: "composer-restored-thumbnail" },
	{ name: "remote", draftSessionId: "host-a:composer-restored-thumbnail", assetSessionId: "composer-restored-thumbnail", assetBaseUrl: "http://127.0.0.1:4000/token-a" },
])("restores a $name image thumbnail from its durable path after the composer remounts", async ({ draftSessionId, assetSessionId, assetBaseUrl }) => {
	const path = ".tallpine/attachments/restored-thumbnail.png";
	const props = { onSend: vi.fn(), draftSessionId, assetSessionId, assetBaseUrl,
		onStageAttachments: vi.fn().mockResolvedValue([path]) };
	const view = render(<ChatComposer {...props} />);
	fireEvent.paste(screen.getByLabelText("Message the agent"), { clipboardData: clipboardData([png()]) });
	await screen.findByLabelText("Remove shot.png");
	expect(screen.getByRole("list", { name: "Attached files" }).querySelector("img"))
		.toHaveAttribute("src", expect.stringContaining("data:image/png;base64,"));
	view.unmount();
	// A new renderer only has persisted descriptors, never cached image bytes.
	purgeFileAttachmentsForSession(draftSessionId);
	render(<ChatComposer {...props} />);
	expect(screen.getByRole("list", { name: "Attached files" }).querySelector("img"))
		.toHaveAttribute("src", attachmentURL(assetBaseUrl ?? getApiBaseUrl(), assetSessionId ?? draftSessionId, path));
	expect(props.onStageAttachments).toHaveBeenCalledOnce();
});

