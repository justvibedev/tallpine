import {
	render,
	EXCERPTS,
	renderComposer,
	deferred,
	typeInComposer,
	composerWireText,
	clipboardData,
	png,
} from "./ChatComposer.test.setup";

import {
	act,
	fireEvent,
	screen,
	waitFor,
	within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Activity, Profiler } from "react";
import {
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { ChatComposer } from "./ChatComposer";
import { sendReferenceToChat } from "../../lib/chat-context-bus";
import {
	markChatComposerDeliveryAccepted,
	prepareChatComposerDelivery,
	readChatSessionDraft,
	writeChatComposerText,
	writeChatExcerptReferences,
} from "../../lib/chat-drafts";
import { getChatDraftBoundaries, getChatDraftBoundary } from "../../lib/chat-draft-boundary";



describe("send keys", () => {
	it("lays attachments beside annotations and clears only annotation references", async () => {
		const sessionId = "composer-shared-attachment-row";
		writeChatExcerptReferences(sessionId, EXCERPTS);
		writeChatComposerText(sessionId, "keep this draft");
		const { field } = renderComposer({ draftSessionId: sessionId, onStageAttachments: vi.fn().mockResolvedValue([".tallpine/attachments/shot.png"]) });
		fireEvent.paste(field, { clipboardData: clipboardData([png()]) });
		const removeImage = await screen.findByRole("button", { name: "Remove shot.png" });
		const row = screen.getByTestId("composer-attachments-and-annotations");
		expect(row).toHaveClass("flex", "flex-wrap", "items-center");
		expect(row).toContainElement(removeImage);
		expect(row).toContainElement(screen.getByRole("button", { name: "1 annotation" }));
		await userEvent.click(screen.getByRole("button", { name: "Remove all annotations" }));
		expect(screen.queryByRole("button", { name: "1 annotation" })).not.toBeInTheDocument();
		expect(readChatSessionDraft(sessionId).composer.excerpts).toEqual([]);
		expect(removeImage).toBeInTheDocument();
		expect(field).toHaveTextContent("keep this draft");
	});
	it("navigates to the selected annotation from the composer popover", async () => {
		const excerpt = { id: "navigation", conversationId: "conversation-1", messageId: "source-1", revision: 2, text: "navigate here", role: "assistant" as const };
		writeChatExcerptReferences("composer-navigation", [excerpt]);
		const onSelectAnnotation = vi.fn();
		renderComposer({ draftSessionId: "composer-navigation", onSelectAnnotation });
		const user = userEvent.setup();
		await user.click(screen.getByRole("button", { name: "1 annotation" }));
		await user.click(screen.getByRole("button", { name: "navigate here" }));
		expect(onSelectAnnotation).toHaveBeenCalledWith(excerpt);
	});
	it("sends the exact durable transcript excerpts with the next message", async () => {
		const sessionId = "composer-excerpts";
		const excerpts = [{
			id: "excerpt-1",
			conversationId: "conversation-1",
			messageId: "message-1",
			revision: 4,
			text: "selected transcript text",
			role: "assistant" as const,
		}];
		writeChatExcerptReferences(sessionId, excerpts);
		const { onSend, field } = renderComposer({ draftSessionId: sessionId });
		await typeInComposer(field, "use this context");
		fireEvent.keyDown(field, { key: "Enter" });
		await waitFor(() => expect(onSend).toHaveBeenCalledWith(
			"use this context",
			undefined,
			expect.any(String),
			undefined,
			excerpts,
		));
	});

	it("focuses the message field when the chat composer opens", () => {
		const { field } = renderComposer({ autoFocusKey: "session-1" });
		expect(document.activeElement).toBe(field);
	});

	it("refocuses the message field when the active chat session changes", () => {
		const { rerender } = render(
			<>
				<button type="button">Outside</button>
				<ChatComposer onSend={vi.fn()} autoFocusKey="session-1" />
			</>,
		);
		const field = screen.getByLabelText("Message the agent");
		expect(document.activeElement).toBe(field);

		screen.getByRole("button", { name: "Outside" }).focus();
		expect(document.activeElement).not.toBe(field);

		rerender(
			<>
				<button type="button">Outside</button>
				<ChatComposer onSend={vi.fn()} autoFocusKey="session-2" />
			</>,
		);
		expect(document.activeElement).toBe(field);
	});

	it("refocuses the message field when returning to the chat window", () => {
		const { field } = renderComposer({ autoFocusKey: "session-1" });
		field.blur();
		expect(document.activeElement).not.toBe(field);

		act(() => {
			window.dispatchEvent(new Event("focus"));
		});

		expect(document.activeElement).toBe(field);
	});

	it("does not focus the hidden or inactive chat composer", () => {
		const { field } = renderComposer({ autoFocus: false, autoFocusKey: "session-1" });
		expect(document.activeElement).not.toBe(field);
	});

	it("applies the natural-growth and seven-line scroll-cap styles", () => {
		const { field } = renderComposer();
		expect(field).toHaveClass(
			"chat-composer-scrollbar",
			"min-h-[4.5rem]",
			"max-h-40",
			"overflow-y-auto",
		);
	});

	it("separates secondary message tools from the primary send action", async () => {
		const user = userEvent.setup();
		render(
			<ChatComposer
				onSend={vi.fn()}
				onStageAttachments={vi.fn().mockResolvedValue([])}
				settings={<button type="button">Model</button>}
			/>,
		);

		const tools = screen.getByRole("group", { name: "Message tools" });
		expect(within(tools).getByRole("button", { name: "Attach a file" })).toBeInTheDocument();
		expect(within(tools).getByRole("button", { name: "Model" })).toBeInTheDocument();

		const actions = screen.getByRole("group", { name: "Send message controls" });
		// The destination Enter is armed with rides on the send control's tooltip
		// rather than as a line of prose beside it.
		const send = within(actions).getByRole("button", { name: "Send message" });
		await user.hover(send);
		expect(await screen.findByRole("tooltip")).toHaveTextContent("Enter to send");
	});


	it("keeps a taller resting field for the redesigned composer", () => {
		const { field } = renderComposer();
		expect(field).toHaveAttribute("contenteditable", "true");
		expect(field).toHaveClass("min-h-[4.5rem]");
	});

	it("uses a muted circular send control that lights up when armed", async () => {
		const { field } = renderComposer();
		const send = screen.getByRole("button", { name: "Send message" });
		expect(send).toHaveClass("rounded-full", "bg-primary", "text-primary-foreground");
		expect(send).toBeDisabled();

		await typeInComposer(field, "hello");
		expect(send).toBeEnabled();
		expect(send).toHaveClass("bg-foreground", "text-on-foreground");
	});

	it("keeps ordinary typing local after the draft becomes nonempty", async () => {
		const onRender = vi.fn();
		render(
			<Profiler id="composer" onRender={onRender}>
				<ChatComposer onSend={vi.fn()} />
			</Profiler>,
		);
		const field = screen.getByLabelText("Message the agent");

		await typeInComposer(field, "a");
		const commitsAfterFirstCharacter = onRender.mock.calls.length;
		await typeInComposer(field, "b");
		await typeInComposer(field, "c");

		expect(field.textContent).toBe("abc");
		expect(onRender).toHaveBeenCalledTimes(commitsAfterFirstCharacter);
	});

	it("turns the empty send action into Stop while the agent is working", async () => {
		const onInterrupt = vi.fn();
		const { field } = renderComposer({ willQueue: true, onInterrupt });

		const stop = screen.getByRole("button", { name: "Stop turn" });
		expect(screen.queryByRole("button", { name: "Send message" })).not.toBeInTheDocument();
		await userEvent.click(stop);
		expect(onInterrupt).toHaveBeenCalledOnce();

		await typeInComposer(field, "queue this next");
		expect(screen.queryByRole("button", { name: "Stop turn" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Send message" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Queue message" })).toBeEnabled();
	});

	it("turns the empty send action into Continue after a stop, and back into Send once typed", async () => {
		const onContinue = vi.fn();
		const { onSend, field } = renderComposer({ onContinue });

		const resume = screen.getByRole("button", { name: "Continue turn" });
		expect(screen.queryByRole("button", { name: "Send message" })).not.toBeInTheDocument();
		await userEvent.click(resume);
		expect(onContinue).toHaveBeenCalledOnce();
		expect(onSend).not.toHaveBeenCalled();

		// Asking something else instead: a draft turns the same button into Send.
		await typeInComposer(field, "a different question");
		expect(screen.queryByRole("button", { name: "Continue turn" })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Send message" }));
		expect(onSend).toHaveBeenCalledWith("a different question");
	});

	it("keeps Stop while the agent is working, even when a stopped turn could be continued", () => {
		renderComposer({ willQueue: true, onInterrupt: vi.fn(), onContinue: vi.fn() });
		expect(screen.getByRole("button", { name: "Stop turn" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Continue turn" })).not.toBeInTheDocument();
	});

	it.each(["rejected promise", "synchronous throw"])("recovers from a Continue failure: %s", async (failure) => {
		const error = new Error("Cannot continue now");
		const onContinue = vi.fn(() => {
			if (failure === "synchronous throw") throw error;
			return Promise.reject(error);
		});
		renderComposer({ onContinue });
		await userEvent.click(screen.getByRole("button", { name: "Continue turn" }));
		expect(await screen.findByText(error.message)).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Continue turn" })).toBeEnabled();
	});

	it("disables Continue until the request settles and prevents duplicate clicks", async () => {
		const request = deferred<void>();
		const onContinue = vi.fn(() => request.promise);
		renderComposer({ onContinue });
		const button = screen.getByRole("button", { name: "Continue turn" });
		await userEvent.click(button);
		expect(button).toBeDisabled();
		await userEvent.click(button);
		expect(onContinue).toHaveBeenCalledOnce();
		await act(async () => request.resolve());
		expect(button).toBeEnabled();
	});

	it("sends on Enter", async () => {
		const { onSend, field } = renderComposer();
		await typeInComposer(field, "hello");
		expect(field).toHaveTextContent("hello");
		await userEvent.keyboard("{Enter}");
		expect(onSend).toHaveBeenCalledWith("hello");
	});

	it("joins rapid duplicate Enter submissions without showing a false retry error", async () => {
		const provider = deferred<void>();
		const onSend = vi
			.fn()
			.mockImplementationOnce(() => provider.promise)
			.mockRejectedValueOnce(new Error("A message is already being sent for this session."));
		render(<ChatComposer onSend={onSend} />);
		const field = screen.getByLabelText("Message the agent") as HTMLElement;
		await typeInComposer(field, "only send this once");

		fireEvent.keyDown(field, { key: "Enter" });
		fireEvent.keyDown(field, { key: "Enter" });

		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		provider.resolve();
		await act(async () => {
			await provider.promise;
		});
		await waitFor(() => expect(field).toHaveTextContent(""));
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(screen.queryByText(/draft.*kept|retry/i)).not.toBeInTheDocument();
	});

	it("makes a newline on Shift+Enter and does not send", async () => {
		const { onSend, field } = renderComposer();
		await typeInComposer(field, "one");
		await userEvent.keyboard("{Shift>}{Enter}{/Shift}");
		await typeInComposer(field, "two");
		expect(onSend).not.toHaveBeenCalled();
		expect(composerWireText(field)).toBe("one\ntwo");
	});

	it("refuses to send an empty message: there is no keystroke concept here", async () => {
		const { onSend, field } = renderComposer();
		await typeInComposer(field, "   ");
		await userEvent.keyboard("{Enter}");
		expect(onSend).not.toHaveBeenCalled();
	});

	it("clears the field after a send", async () => {
		const { field } = renderComposer();
		await typeInComposer(field, "hello");
		await userEvent.keyboard("{Enter}");
		expect(field.textContent).toBe("");
	});

	it("reports when the draft turns non-empty and empty again", async () => {
		const onDraftEmptyChange = vi.fn();
		const { field } = renderComposer({ onDraftEmptyChange });
		expect(onDraftEmptyChange).toHaveBeenLastCalledWith(true);
		await typeInComposer(field, "hello");
		expect(onDraftEmptyChange).toHaveBeenLastCalledWith(false);
		await userEvent.keyboard("{Enter}");
		expect(onDraftEmptyChange).toHaveBeenLastCalledWith(true);
	});

	it("does not restore a sent draft through undo", async () => {
		const { field } = renderComposer();
		await typeInComposer(field, "already sent");
		await userEvent.keyboard("{Enter}");
		await userEvent.keyboard("{Meta>}z{/Meta}");

		expect(field.textContent).toBe("");
	});

	it("does not undo an external draft seed into the previous draft", async () => {
		const onSend = vi.fn();
		const view = render(
			<ChatComposer onSend={onSend} draftSeed={{ id: "first", text: "first draft" }} />,
		);
		const field = screen.getByLabelText("Message the agent");
		await waitFor(() => expect(field).toHaveTextContent("first draft"));

		view.rerender(
			<ChatComposer onSend={onSend} draftSeed={{ id: "second", text: "replacement draft" }} />,
		);
		await waitFor(() => expect(field).toHaveTextContent("replacement draft"));
		field.focus();
		await userEvent.keyboard("{Meta>}z{/Meta}");

		expect(field).toHaveTextContent("replacement draft");
		expect(field).not.toHaveTextContent("first draft");
	});

	it("persists a trailing-whitespace edit made after restoring a session draft", async () => {
		const sessionId = "composer-trailing-whitespace";
		writeChatComposerText(sessionId, "restored draft");
		const firstView = render(
			<ChatComposer onSend={vi.fn()} draftSessionId={sessionId} />,
		);
		const field = screen.getByLabelText("Message the agent");
		await waitFor(() => expect(composerWireText(field)).toBe("restored draft"));

		await typeInComposer(field, " ");
		await waitFor(() =>
			expect(readChatSessionDraft(sessionId).composer.text).toBe("restored draft "),
		);
		firstView.unmount();

		render(<ChatComposer onSend={vi.fn()} draftSessionId={sessionId} />);
		await waitFor(() =>
			expect(composerWireText(screen.getByLabelText("Message the agent"))).toBe(
				"restored draft ",
			),
		);
	});

	it("keeps the draft and reports the error when sending fails", async () => {
		const onSend = vi.fn().mockRejectedValue(new Error("daemon unavailable"));
		render(<ChatComposer onSend={onSend} />);
		const field = screen.getByLabelText("Message the agent") as HTMLElement;

		await typeInComposer(field, "do not lose this task");
		await userEvent.keyboard("{Enter}");

		expect(await screen.findByRole("alert")).toHaveTextContent("Your draft was kept");
		expect(field.textContent).toBe("do not lose this task");
	});

	it("clears and locks a memory-only draft during delivery and restores it after rejection", async () => {
		let rejectSend!: (error: unknown) => void;
		const pending = new Promise<void>((_resolve, reject) => { rejectSend = reject; });
		const onSend = vi.fn().mockReturnValue(pending);
		render(<ChatComposer onSend={onSend} />);
		const field = screen.getByLabelText("Message the agent") as HTMLElement;

		await typeInComposer(field, "show this immediately");
		await userEvent.keyboard("{Enter}");

		expect(onSend).toHaveBeenCalledWith("show this immediately");
		expect(field).toHaveTextContent("");
		expect(field).toHaveAttribute("contenteditable", "false");
		await act(async () => rejectSend({ code: "CHAT_RESUME_FAILED", message: "Could not resume" }));
		expect(field).toHaveTextContent("show this immediately");
		expect(field).toHaveAttribute("contenteditable", "true");
		expect(screen.getByRole("alert")).toHaveTextContent("Your draft was kept");
	});

	it("hides an excerpt draft while its send is in flight and shows it again if the send fails", async () => {
		const sessionId = "composer-conceal-in-flight";
		writeChatExcerptReferences(sessionId, EXCERPTS);
		let fail!: (error: Error) => void;
		const pending = new Promise<void>((_resolve, reject) => { fail = reject; });
		const onSend = vi.fn().mockReturnValue(pending);
		render(<ChatComposer draftSessionId={sessionId} onSend={onSend} />);
		const field = screen.getByLabelText("Message the agent") as HTMLElement;

		await typeInComposer(field, "sent but not yet accepted");
		await userEvent.keyboard("{Enter}");

		await waitFor(() => expect(onSend).toHaveBeenCalledOnce());
		expect(field).toHaveClass("invisible");
		expect(field).toHaveAttribute("contenteditable", "false");
		expect(readChatSessionDraft(sessionId).composer.delivery?.draft).toBeUndefined();

		await act(async () => {
			fail(new Error("daemon unreachable"));
			await pending.catch(() => undefined);
		});
		await waitFor(() => expect(field).not.toHaveClass("invisible"));
		expect(field.textContent).toBe("sent but not yet accepted");
	});

	it("brings a hidden excerpt draft back if the send never answers", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			const sessionId = "composer-conceal-hung";
			writeChatExcerptReferences(sessionId, EXCERPTS);
			const onSend = vi.fn().mockReturnValue(new Promise<void>(() => {}));
			render(<ChatComposer draftSessionId={sessionId} onSend={onSend} />);
			const field = screen.getByLabelText("Message the agent") as HTMLElement;

			await typeInComposer(field, "never answered");
			await userEvent.keyboard("{Enter}");
			await waitFor(() => expect(field).toHaveClass("invisible"));
			expect(field).toHaveAttribute("contenteditable", "false");

			await act(async () => {
				await vi.advanceTimersByTimeAsync(3100);
			});

			expect(field).not.toHaveClass("invisible");
			expect(field.textContent).toBe("never answered");
		} finally {
			vi.useRealTimers();
		}
	});

	it("takes an Ask-in-chat code reference as a chip and sends its code with the question", async () => {
		const sessionId = "composer-ask-in-chat-reference";
		const onSend = vi.fn().mockResolvedValue(undefined);
		render(<ChatComposer draftSessionId={sessionId} onSend={onSend} />);
		const field = screen.getByLabelText("Message the agent");

		act(() => {
			expect(sendReferenceToChat(sessionId, {
				path: "src/retry.ts",
				display: "retry.ts#L3-L4",
				wire: "\n[src/retry.ts#L3-L4]\n```diff\n+await retry();\n```\n",
			})).toBe(true);
		});
		await waitFor(() => expect(within(field).getByText("retry.ts#L3-L4")).toBeInTheDocument());
		await typeInComposer(field, "why twice?");
		fireEvent.keyDown(field, { key: "Enter" });

		await waitFor(() => expect(onSend).toHaveBeenCalledOnce());
		const sent = onSend.mock.calls[0][0] as string;
		expect(sent).toContain("[src/retry.ts#L3-L4]");
		expect(sent).toContain("+await retry();");
		expect(sent).toContain("why twice?");
		// Another session's composer never receives it.
		expect(sendReferenceToChat("some-other-session", { path: "x", display: "x", wire: "x" })).toBe(false);
	});

	it.each([false, true])("clears the composer during delivery and preserves a repeated next draft (queued: %s)", async (willQueue) => {
		const sessionId = `composer-live-send-acceptance-${willQueue}`;
		const pending = deferred<void>();
		const onSend = vi.fn().mockReturnValueOnce(pending.promise).mockResolvedValue(undefined);
		render(<ChatComposer draftSessionId={sessionId} onSend={onSend} willQueue={willQueue} />);
		const field = screen.getByLabelText("Message the agent");
		await typeInComposer(field, "send this once");
		fireEvent.keyDown(field, { key: "Enter" });

		await waitFor(() => expect(onSend).toHaveBeenCalledOnce());
		expect(field.textContent).toBe("");
		expect(field).not.toHaveClass("invisible");
		await waitFor(() => expect(field).toHaveAttribute("contenteditable", "true"));
		await typeInComposer(field, "send this once");
		await act(async () => pending.resolve());
		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		expect(field).toHaveTextContent("send this once");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("send this once");
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(field).toHaveAttribute("contenteditable", "true");
		expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined();

		fireEvent.keyDown(field, { key: "Enter" });
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(2));
		expect(onSend.mock.calls[1][0]).toBe("send this once");
		expect(onSend.mock.calls[1][2]).not.toBe(onSend.mock.calls[0][2]);
		await waitFor(() => expect(field).toHaveAttribute("contenteditable", "true"));
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("keeps the next draft when its storage write fails before acceptance", async () => {
		const sessionId = "composer-optimistic-next-draft-storage-failure";
		const storage = window.localStorage;
		const pending = deferred<void>();
		const onSend = vi.fn(() => pending.promise);
		render(<ChatComposer draftSessionId={sessionId} onSend={onSend} />);
		const field = screen.getByLabelText("Message the agent");
		await typeInComposer(field, "original request");
		fireEvent.keyDown(field, { key: "Enter" });
		await waitFor(() => expect(onSend).toHaveBeenCalledOnce());
		await waitFor(() => expect(field).toHaveAttribute("contenteditable", "true"));
		let failWrite = true;
		const localStorage = vi.spyOn(window, "localStorage", "get").mockReturnValue({
			getItem: storage.getItem.bind(storage),
			removeItem: storage.removeItem.bind(storage),
			setItem: (key: string, value: string) => {
				if (failWrite && JSON.parse(value).composer?.text === "next draft") {
					throw new DOMException("full", "QuotaExceededError");
				}
				storage.setItem(key, value);
			},
		} as Storage);
		await typeInComposer(field, "next draft");
		await act(async () => pending.resolve());
		expect(field).toHaveTextContent("next draft");
		expect(screen.getByRole("alert")).toHaveTextContent("couldn’t be cleared");
		expect(onSend).toHaveBeenCalledOnce();
		failWrite = false;
		await userEvent.click(screen.getByRole("button", { name: "Finish clearing accepted message" }));
		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		expect(readChatSessionDraft(sessionId).composer.text).toBe("next draft");
		expect(onSend).toHaveBeenCalledOnce();
		localStorage.mockRestore();
	});

	it.each(["CHAT_EXCERPT_STALE", "CHAT_EXCERPT_INVALID"])("returns a rejected excerpt send (%s) to the draft instead of an uncertain retry", async (code) => {
		const sessionId = `composer-excerpt-refused-${code}`;
		const excerpts = [{ id: "stale", conversationId: "conversation-1", messageId: "source-1", revision: 1, text: "old text", role: "assistant" as const }];
		writeChatExcerptReferences(sessionId, excerpts);
		const onSend = vi.fn().mockRejectedValueOnce({ code, message: "source message changed; reselect it" });
		render(<ChatComposer draftSessionId={sessionId} onSend={onSend} />);
		const field = screen.getByLabelText("Message the agent");
		await typeInComposer(field, "explain this");
		fireEvent.keyDown(field, { key: "Enter" });
		await waitFor(() => expect(onSend).toHaveBeenCalledOnce());
		await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("reselect it"));
		const draft = readChatSessionDraft(sessionId).composer;
		expect(draft.delivery).toBeUndefined();
		expect(draft.excerpts).toEqual(excerpts);
		expect(screen.queryByRole("button", { name: "Retry message safely" })).not.toBeInTheDocument();
	});

	it("queues instead of steering when excerpts are attached", async () => {
		const sessionId = "composer-excerpt-no-steer";
		const excerpts = [{ id: "keep", conversationId: "conversation-1", messageId: "source-1", revision: 1, text: "keep me", role: "assistant" as const }];
		writeChatExcerptReferences(sessionId, excerpts);
		const onSend = vi.fn().mockResolvedValue(undefined);
		const onSteer = vi.fn();
		render(<ChatComposer onSend={onSend} onSteer={onSteer} canSteer willQueue draftSessionId={sessionId} />);
		const field = screen.getByLabelText("Message the agent");
		await typeInComposer(field, "about this");
		fireEvent.keyDown(field, { key: "Enter", ctrlKey: true });
		await waitFor(() => expect(onSend).toHaveBeenCalledOnce());
		expect(onSteer).not.toHaveBeenCalled();
		expect(onSend.mock.calls[0]?.[4]).toEqual(excerpts);
	});

	it.each([
		["CHAT_CONTROLLER_NOT_READY", "Request was rejected"],
		["validation_error", "Request was rejected"],
		["CHAT_RESUME_FAILED", "The provider could not reopen the saved conversation"],
		// A refused /btw or /side launch must not lock the main draft.
		["CHAT_SIDE_UNSUPPORTED", "side chats support Codex, Claude Code, and OpenCode"],
		["CHAT_SIDE_UNAVAILABLE", "side chats are unavailable"],
	])("unlocks a definitively rejected first send (%s) and gives the edited send a new identity", async (code, message) => {
		const sessionId = `composer-first-send-refused-${code}`;
		const onSend = vi.fn()
			.mockRejectedValueOnce({ code, message })
			.mockResolvedValue(undefined);
		const first = render(<ChatComposer draftSessionId={sessionId} onSend={onSend} />);
		const field = screen.getByLabelText("Message the agent");
		await typeInComposer(field, "original request");
		fireEvent.keyDown(field, { key: "Enter" });
		await waitFor(() => expect(onSend).toHaveBeenCalledOnce());
		await waitFor(() => expect(field).toHaveAttribute("contenteditable", "true"));
		expect(screen.getByRole("alert")).toHaveTextContent(message);
		expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined();
		first.unmount();
		render(<ChatComposer draftSessionId={sessionId} onSend={onSend} />);
		const restored = screen.getByLabelText("Message the agent");
		await waitFor(() => expect(restored).toHaveTextContent("original request"));
		expect(restored).toHaveAttribute("contenteditable", "true");
		await typeInComposer(restored, "edited request");
		fireEvent.keyDown(restored, { key: "Enter" });
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(2));
		expect(onSend.mock.calls[1]?.[2]).not.toBe(onSend.mock.calls[0]?.[2]);
		await waitFor(() => expect(restored).toHaveTextContent(""));
	});

	it.each(["CHAT_CONTROLLER_NOT_READY", "validation_error"])("keeps an uncertain earlier send locked when its retry is definitively refused (%s)", async (code) => {
		const sessionId = `composer-recovery-send-refused-${code}`;
		const onSend = vi.fn()
			.mockRejectedValueOnce(new Error("response lost"))
			.mockRejectedValue({ code, message: "Request was rejected" });
		const first = render(<ChatComposer draftSessionId={sessionId} onSend={onSend} />);
		await typeInComposer(screen.getByLabelText("Message the agent"), "possibly accepted request");
		fireEvent.keyDown(screen.getByLabelText("Message the agent"), { key: "Enter" });
		await waitFor(() => expect(screen.getByRole("button", { name: "Retry message safely" })).toBeEnabled());
		first.unmount();
		render(<ChatComposer draftSessionId={sessionId} onSend={onSend} />);
		await userEvent.click(screen.getByRole("button", { name: "Retry message safely" }));
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(2));
		await waitFor(() => expect(screen.getByRole("button", { name: "Retry message safely" })).toBeEnabled());
		expect(onSend.mock.calls[1]).toEqual(onSend.mock.calls[0]);
		expect(screen.getByLabelText("Message the agent")).toHaveAttribute("contenteditable", "false");
		expect(readChatSessionDraft(sessionId).composer.delivery?.state).toBe("dispatching");
	});

	it("locks an accepted draft whose durable clear failed and clears without redispatch", async () => {
		const sessionId = "composer-accepted-clear-failure";
		const durableStorage = window.localStorage;
		let failRemoval = true;
		const storage = {
			getItem: durableStorage.getItem.bind(durableStorage),
			setItem: durableStorage.setItem.bind(durableStorage),
			removeItem: (key: string) => {
				if (failRemoval) throw new DOMException("blocked", "SecurityError");
				durableStorage.removeItem(key);
			},
		} as unknown as Storage;
		const localStorage = vi.spyOn(window, "localStorage", "get").mockReturnValue(storage);
		const onSend = vi.fn(async () => undefined);
		const view = render(<ChatComposer onSend={onSend} draftSessionId={sessionId} />);
		try {
			const field = screen.getByLabelText("Message the agent");
			await typeInComposer(field, "accepted but not cleared");
			fireEvent.keyDown(field, { key: "Enter" });
			await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
			expect(await screen.findByRole("alert")).toHaveTextContent("couldn’t be cleared");
			expect(field).toHaveAttribute("contenteditable", "false");
			expect(getChatDraftBoundaries(sessionId)).toEqual([
				"persistence-failed",
			]);

			fireEvent.keyDown(field, { key: "Enter" });
			expect(onSend).toHaveBeenCalledTimes(1);

			view.rerender(
				<ChatComposer busy onSend={onSend} draftSessionId={sessionId} />,
			);
			expect(
				screen.getByRole("button", { name: "Finish clearing accepted message" }),
			).toBeEnabled();

			failRemoval = false;
			await userEvent.click(
				screen.getByRole("button", { name: "Finish clearing accepted message" }),
			);
			await waitFor(() => expect(field).toHaveTextContent(""));
			expect(onSend).toHaveBeenCalledTimes(1);
			view.rerender(<ChatComposer onSend={onSend} draftSessionId={sessionId} />);

			await typeInComposer(field, "a new message after recovery");
			fireEvent.keyDown(field, { key: "Enter" });
			await waitFor(() => expect(onSend).toHaveBeenCalledTimes(2));
		} finally {
			view.unmount();
			localStorage.mockRestore();
		}
	});

	it.each([false, true])("finishes a restored accepted delivery without sending again (delayed mount: %s)", async (delayedMount) => {
		const sessionId = `composer-restarted-accepted-delivery-${delayedMount}`;
		const prepared = prepareChatComposerDelivery(sessionId, {
			kind: "send",
			composerText: "already sent before restart",
			attachments: [],
			requestText: "already sent before restart",
			clientMessageId: "restarted-accepted-delivery",
		});
		if (!prepared.ok) throw new Error("Failed to prepare test delivery");
		markChatComposerDeliveryAccepted(sessionId, prepared.mutation.clientMessageId, prepared.mutation.revision);
		const onSend = vi.fn();
		const surface = (mode: "hidden" | "visible") => (
			<Activity mode={mode}>
				<ChatComposer draftSessionId={sessionId} onSend={onSend} />
			</Activity>
		);
		const view = render(surface(delayedMount ? "hidden" : "visible"));
		if (delayedMount) {
			await screen.findByLabelText("Message the agent");
			const active = render(<ChatComposer draftSessionId={sessionId} onSend={onSend} />);
			await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
			active.unmount();
			view.rerender(surface("visible"));
		}

		const field = screen.getByLabelText("Message the agent");
		await waitFor(() => expect(field).toHaveAttribute("contenteditable", "true"));
		expect(field.textContent).toBe("");
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined();
		expect(onSend).not.toHaveBeenCalled();
	});

	it.each(["read", "write"])("preserves safe retry when recording acceptance fails to %s storage", async (failure) => {
		const sessionId = `composer-live-acceptance-storage-failure-${failure}`;
		const durableStorage = window.localStorage;
		let failAcceptance = false;
		const storage = {
			getItem: (key: string) => {
				if (failAcceptance && failure === "read") throw new DOMException("blocked", "SecurityError");
				return durableStorage.getItem(key);
			},
			removeItem: durableStorage.removeItem.bind(durableStorage),
			setItem: (key: string, value: string) => {
				if (failAcceptance && failure === "write" && key.includes(sessionId) && JSON.parse(value).composer?.delivery?.state === "accepted") {
					throw new DOMException("full", "QuotaExceededError");
				}
				durableStorage.setItem(key, value);
			},
		} as Storage;
		const localStorage = vi.spyOn(window, "localStorage", "get").mockReturnValue(storage);
		const onSend = vi.fn().mockImplementationOnce(async () => { failAcceptance = true; }).mockResolvedValue(undefined);
		const view = render(<ChatComposer draftSessionId={sessionId} onSend={onSend} />);
		try {
			const field = screen.getByLabelText("Message the agent");
			await typeInComposer(field, "accepted but not recorded");
			fireEvent.keyDown(field, { key: "Enter" });
			expect(await screen.findByRole("alert")).toHaveTextContent("acceptance couldn’t be recorded");
			expect(field).toHaveAttribute("contenteditable", "false");

			failAcceptance = false;
			expect(readChatSessionDraft(sessionId).composer.delivery?.state).toBe("dispatching");
			await userEvent.click(screen.getByRole("button", { name: "Retry message safely" }));
			await waitFor(() => expect(field).toHaveAttribute("contenteditable", "true"));
			expect(screen.queryByRole("alert")).not.toBeInTheDocument();
			expect(onSend).toHaveBeenCalledTimes(2);
			expect(onSend.mock.calls[1]).toEqual(onSend.mock.calls[0]);
		} finally {
			view.unmount();
			localStorage.mockRestore();
		}
	});

	it("keeps a restored delivery recoverable without blocking navigation", async () => {
		const sessionId = "composer-restored-delivery-boundary";
		prepareChatComposerDelivery(sessionId, {
			kind: "send",
			composerText: "recover this message",
			attachments: [],
			requestText: "recover this message",
			clientMessageId: "composer-restored-delivery-id",
		});

		render(<ChatComposer onSend={vi.fn()} draftSessionId={sessionId} />);

		expect(await screen.findByRole("alert")).toHaveTextContent(
			"Message delivery wasn’t confirmed before Chat restarted",
		);
		await waitFor(() => expect(getChatDraftBoundaries(sessionId)).toEqual([]));
	});

	it.each([false, true])("preserves a newer draft when an old surface accepts before replacement mount: %s", async (acceptBeforeMount) => {
		const sessionId = `composer-later-revision-${acceptBeforeMount}`;
		let acceptSend!: () => void;
		const onSend = vi.fn(
			() =>
				new Promise<void>((resolve) => {
					acceptSend = resolve;
				}),
		);
		const first = render(<ChatComposer onSend={onSend} draftSessionId={sessionId} />);
		const firstField = screen.getByLabelText("Message the agent");
		await typeInComposer(firstField, "submitted revision");
		fireEvent.keyDown(firstField, { key: "Enter" });
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		await typeInComposer(firstField, "draft before leaving");
		first.unmount();
		writeChatComposerText(sessionId, "later revision");
		if (acceptBeforeMount) await act(async () => acceptSend());

		render(<ChatComposer onSend={onSend} draftSessionId={sessionId} />);
		const restored = screen.getByLabelText("Message the agent");
		expect(restored).toHaveTextContent("later revision");
		expect(restored).toHaveAttribute("contenteditable", "true");

		if (!acceptBeforeMount) await act(async () => acceptSend());
		await waitFor(() => expect(restored).toHaveAttribute("contenteditable", "true"));
		expect(restored).toHaveTextContent("later revision");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("later revision");
		expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		await waitFor(() => expect(getChatDraftBoundary(sessionId)).toBeUndefined());
	});

	it("does not resurrect an accepted draft when a same-session replacement rendered before settlement", async () => {
		const sessionId = "composer-rendered-replacement-before-settlement";
		let acceptSend!: () => void;
		const onSend = vi.fn(
			() =>
				new Promise<void>((resolve) => {
					acceptSend = resolve;
				}),
		);
		const surfaces = (
			showOriginal: boolean,
			replacementMode?: "hidden" | "visible",
		) => (
			<>
				{showOriginal ? (
					<div data-testid="original-composer-surface">
						<ChatComposer onSend={onSend} draftSessionId={sessionId} />
					</div>
				) : null}
				{replacementMode ? (
					<Activity key="replacement" mode={replacementMode}>
						<div data-testid="replacement-composer-surface">
							<ChatComposer onSend={onSend} draftSessionId={sessionId} />
						</div>
					</Activity>
				) : null}
			</>
		);

		const view = render(surfaces(true));
		const original = within(screen.getByTestId("original-composer-surface")).getByLabelText(
			"Message the agent",
		);
		await typeInComposer(original, "send exactly once across the render gap");
		fireEvent.keyDown(original, { key: "Enter" });
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));

		// Activity renders the replacement state but keeps its effects disconnected.
		// This is the render-to-commit window: the original surface remains the sole
		// subscriber that can observe and acknowledge settlement.
		view.rerender(surfaces(true, "hidden"));
		const renderedReplacement = within(
			await screen.findByTestId("replacement-composer-surface"),
		).getByLabelText("Message the agent");
		expect(renderedReplacement).toBeInTheDocument();
		expect(readChatSessionDraft(sessionId).composer.text).toBe("");
		expect(readChatSessionDraft(sessionId).composer.delivery?.draft?.text).toBe(
			"send exactly once across the render gap",
		);

		await act(async () => acceptSend());
		await waitFor(() => expect(composerWireText(original)).toBe(""));
		expect(readChatSessionDraft(sessionId).composer.text).toBe("");

		// Commit the already-rendered replacement only after the original subscriber
		// consumed the receipt. Accepted text must not come back unlocked and resendable.
		view.rerender(surfaces(false, "visible"));
		const committedReplacement = within(
			screen.getByTestId("replacement-composer-surface"),
		).getByLabelText("Message the agent");
		expect(composerWireText(committedReplacement)).toBe("");
		fireEvent.keyDown(committedReplacement, { key: "Enter" });
		expect(onSend).toHaveBeenCalledTimes(1);
	});

	it("does not resurrect accepted attachments when a same-session replacement rendered before settlement", async () => {
		const sessionId = "composer-attachment-replacement-before-settlement";
		let acceptSend!: () => void;
		const onSend = vi
			.fn()
			.mockImplementationOnce(
				() =>
					new Promise<void>((resolve) => {
						acceptSend = resolve;
					}),
			)
			.mockResolvedValue(undefined);
		const common = {
			onSend,
			draftSessionId: sessionId,
			nativeImages: true,
			onStageAttachments: vi
				.fn()
				.mockResolvedValue([".tallpine/attachments/accepted-once.png"]),
		};
		const surfaces = (
			showOriginal: boolean,
			replacementMode?: "hidden" | "visible",
		) => (
			<>
				{showOriginal ? (
					<div data-testid="original-attachment-composer-surface">
						<ChatComposer {...common} />
					</div>
				) : null}
				{replacementMode ? (
					<Activity key="attachment-replacement" mode={replacementMode}>
						<div data-testid="replacement-attachment-composer-surface">
							<ChatComposer {...common} />
						</div>
					</Activity>
				) : null}
			</>
		);

		const view = render(surfaces(true));
		const originalSurface = screen.getByTestId("original-attachment-composer-surface");
		const original = within(originalSurface).getByLabelText("Message the agent");
		fireEvent.paste(original, {
			clipboardData: clipboardData([png("accepted-once.png")]),
		});
		await within(originalSurface).findByLabelText("Remove accepted-once.png");
		await typeInComposer(original, "inspect this once");
		fireEvent.keyDown(original, { key: "Enter" });
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		expect(onSend.mock.calls[0]?.[0]).toContain(".tallpine/attachments/accepted-once.png");
		expect(onSend.mock.calls[0]?.[1]).toEqual([
			{ mimeType: "image/png", data: expect.any(String) },
		]);

		view.rerender(surfaces(true, "hidden"));
		const renderedReplacementSurface = await screen.findByTestId(
			"replacement-attachment-composer-surface",
		);
		expect(
			within(renderedReplacementSurface).queryByLabelText("Remove accepted-once.png"),
		).not.toBeInTheDocument();
		expect(readChatSessionDraft(sessionId).composer.delivery?.draft?.attachments).toMatchObject([
			{ path: ".tallpine/attachments/accepted-once.png" },
		]);

		await act(async () => acceptSend());
		await waitFor(() =>
			expect(readChatSessionDraft(sessionId).composer).toMatchObject({
				text: "",
				attachments: [],
			}),
		);
		await waitFor(() =>
			expect(
				within(originalSurface).queryByLabelText("Remove accepted-once.png"),
			).not.toBeInTheDocument(),
		);

		view.rerender(surfaces(false, "visible"));
		const committedReplacementSurface = screen.getByTestId(
			"replacement-attachment-composer-surface",
		);
		const committedReplacement = within(committedReplacementSurface).getByLabelText(
			"Message the agent",
		);
		expect(
			within(committedReplacementSurface).queryByLabelText("Remove accepted-once.png"),
		).not.toBeInTheDocument();
		fireEvent.keyDown(committedReplacement, { key: "Enter" });
		expect(onSend).toHaveBeenCalledTimes(1);

		await typeInComposer(committedReplacement, "fresh follow-up");
		fireEvent.keyDown(committedReplacement, { key: "Enter" });
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(2));
		expect(onSend.mock.calls[1]?.[0]).toBe("fresh follow-up");
		expect(onSend.mock.calls[1]?.[0]).not.toContain(".tallpine/attachments/accepted-once.png");
		expect(onSend.mock.calls[1]?.[1]).toBeUndefined();
	});

	it("renders command failures from the live surface", () => {
		render(<ChatComposer onSend={vi.fn()} commandError="The approval could not be submitted" />);
		expect(screen.getByRole("alert")).toHaveTextContent("The approval could not be submitted");
	});
});

/* ---- steering -------------------------------------------------------------
   Queueing and steering are different promises to the user: a queued message
   waits for a cold start, a steer lands in the turn already running. The chord
   is the only way to pick the second one now, so each path that can reach it —
   Cmd, Ctrl, and the send control while a modifier is held — is pinned here.
--------------------------------------------------------------------------- */

