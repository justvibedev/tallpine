// The setup module registers vi.mock calls, so it must be imported before ChatWorkspace.
import {
	writeText,
	cleanupChatWorkspaceTest,
	resetChatWorkspaceMocks,
} from "./ChatWorkspace.test.setup";
import {
	act,
	fireEvent,
	screen,
	waitFor,
	within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Activity } from "react";
import { typeInLexicalEditor } from "../../test/lexical";
import {
	afterEach,
	beforeEach,
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { chatFixture } from "../../lib/chat-fixture";
import type { ConversationMessage, ConversationSnapshot } from "../../types/conversation";
import {
	getChatComposerMutation,
	getChatInlineEditMutation,
	prepareChatInlineEditDelivery,
	readChatSessionDraft,
	writeChatInlineEdit,
} from "../../lib/chat-drafts";
import { getChatDraftBoundaries, getChatDraftBoundary } from "../../lib/chat-draft-boundary";
import { renderChat as render } from "../../test/render-chat";
import {
	idleSnapshot,
	chatSession,
} from "./ChatWorkspaceTestHelpers";


beforeEach(resetChatWorkspaceMocks);

afterEach(async () => {
	await cleanupChatWorkspaceTest();
});

describe("ChatWorkspace message actions", () => {
	it("hides first-message editing until the conversation controller is ready", () => {
		render(
			<ChatWorkspace
				snapshot={{
					...idleSnapshot(),
					controller: { state: "stopped" },
					capabilities: ["fork", "prompt_replay", "embedded_context"],
					hasMoreBefore: false,
				}}
				onEditMessage={vi.fn(async () => undefined)}
			/>,
		);

		expect(screen.queryByRole("button", { name: "Edit user message" })).not.toBeInTheDocument();
	});

	it("offers exact first-message edits even when the provider cannot branch or replay history", async () => {
		const user = userEvent.setup();
		const onEditMessage = vi.fn(async () => undefined);
		const view = render(
			<ChatWorkspace
				snapshot={{
					...idleSnapshot(),
					capabilities: [],
					hasMoreBefore: true,
				}}
				onEditMessage={onEditMessage}
			/>,
		);

		// The earliest loaded prompt is not necessarily the first provider prompt
		// until every older page has been loaded.
		expect(screen.queryByRole("button", { name: "Edit user message" })).not.toBeInTheDocument();
		view.rerender(
			<ChatWorkspace
				snapshot={{
					...idleSnapshot(),
					capabilities: [],
					hasMoreBefore: false,
				}}
				onEditMessage={onEditMessage}
			/>,
		);

		const editButtons = screen.getAllByRole("button", {
			name: "Edit user message",
		});
		expect(editButtons).toHaveLength(1);
		await user.click(editButtons[0]!);
		expect(screen.queryByText(/Reconstructed context:/)).not.toBeInTheDocument();

		const editor = screen.getByRole("textbox", { name: "Edit message" });
		await user.clear(editor);
		await user.type(editor, "Replace the first prompt exactly.");
		await user.click(screen.getByRole("button", { name: "Send edited message" }));

		expect(onEditMessage).toHaveBeenCalledWith(
			"turn-1",
			"Replace the first prompt exactly.",
			expect.any(String),
		);
	});

	it("uses the server's first eligible prompt after a provider boundary", () => {
		const snapshot = idleSnapshot();
		render(
			<ChatWorkspace
				snapshot={{
					...snapshot,
					capabilities: [],
					hasMoreBefore: false,
					items: snapshot.items.map((item) =>
						item.kind === "message" && item.turnId === "turn-1"
							? { ...item, editAvailable: false }
							: item,
					),
				}}
				onEditMessage={vi.fn(async () => undefined)}
			/>,
		);

		const edits = screen.getAllByRole("button", {
			name: "Edit user message",
		});
		expect(edits).toHaveLength(1);
	});

	it("offers reconstructed historical edits only when text replay and embedded context are both negotiated", async () => {
		const user = userEvent.setup();
		const onEditMessage = vi.fn(async () => undefined);
		const promptReplayOnly = {
			...idleSnapshot(),
			capabilities: ["prompt_replay"],
		};
		const view = render(
			<ChatWorkspace snapshot={promptReplayOnly} onEditMessage={onEditMessage} />,
		);

		// The first prompt is still exactly reconstructable in a fresh provider session.
		expect(screen.getAllByRole("button", { name: "Edit user message" })).toHaveLength(1);

		view.rerender(
			<ChatWorkspace
				snapshot={{
					...promptReplayOnly,
					capabilities: ["prompt_replay", "embedded_context"],
				}}
				onEditMessage={onEditMessage}
			/>,
		);

		expect(screen.getAllByRole("button", { name: "Edit user message" })).toHaveLength(2);
		await user.click(screen.getAllByRole("button", { name: "Edit user message" })[1]!);
		const disclosure = screen.getByText(/Reconstructed context:/);
		expect(disclosure).toHaveTextContent(
			"Reconstructed context: text messages will be replayed into a new agent session. Tool calls, approvals, and workspace history will not be replayed; current worktree files stay as they are.",
		);
		expect(screen.getByRole("textbox", { name: "Edit message" })).toHaveAttribute(
			"aria-describedby",
			disclosure.id,
		);
	});

	it("keeps replay available when the provider also supports native forks", async () => {
		const user = userEvent.setup();
		const snapshot = idleSnapshot();
		render(
			<ChatWorkspace
				snapshot={{
					...snapshot,
					capabilities: ["fork", "prompt_replay", "embedded_context"],
					branchedFromEarlierMessage: true,
					branchMaterialization: {
						strategy: "approximate_context",
						replayTruncated: false,
					},
					turns: snapshot.turns.map((turn) => ({
						...turn,
						providerTurnId: undefined,
					})),
				}}
				onEditMessage={vi.fn(async () => undefined)}
			/>,
		);

		expect(screen.getAllByRole("button", { name: "Edit user message" })).toHaveLength(2);
		await user.click(screen.getAllByRole("button", { name: "Edit user message" })[1]!);
		expect(screen.getByText(/Reconstructed context:/)).toBeVisible();
	});

	it("requires a prior visible provider turn before offering a native historical fork", () => {
		const snapshot = idleSnapshot();
		render(
			<ChatWorkspace
				snapshot={{
					...snapshot,
					capabilities: ["fork"],
					turns: snapshot.turns.map((turn) =>
						turn.id === "turn-1" ? { ...turn, providerTurnId: undefined } : turn,
					),
				}}
				onEditMessage={vi.fn(async () => undefined)}
			/>,
		);

		// The first provider prompt can always be restarted fresh. The second needs
		// either a prior provider turn to fork or negotiated reconstructed replay.
		expect(screen.getAllByRole("button", { name: "Edit user message" })).toHaveLength(1);
	});

	it("does not warn about reconstruction when a native fork anchor is visible", async () => {
		const user = userEvent.setup();
		render(
			<ChatWorkspace
				snapshot={{
					...idleSnapshot(),
					capabilities: ["fork", "prompt_replay", "embedded_context"],
					nativeForkAvailableAfterSequence: 1,
				}}
				onEditMessage={vi.fn(async () => undefined)}
			/>,
		);

		await user.click(screen.getAllByRole("button", { name: "Edit user message" })[1]!);
		expect(screen.queryByText(/Reconstructed context:/)).not.toBeInTheDocument();
	});

	it("keeps reconstructed ancestor prompts editable when their provider ids belong to the source scope", () => {
		const snapshot = idleSnapshot();
		render(
			<ChatWorkspace
				snapshot={{
					...snapshot,
					capabilities: ["prompt_replay", "embedded_context"],
					branchedFromEarlierMessage: true,
					branchMaterialization: {
						strategy: "approximate_context",
						replayTruncated: false,
					},
					turns: snapshot.turns.map((turn) =>
						turn.id === "turn-1" ? { ...turn, providerTurnId: undefined } : turn,
					),
				}}
				onEditMessage={vi.fn(async () => undefined)}
			/>,
		);

		expect(screen.getAllByRole("button", { name: "Edit user message" })).toHaveLength(2);
	});

	it("treats the earliest loaded prompt as historical while older pages remain", async () => {
		const user = userEvent.setup();
		render(
			<ChatWorkspace
				snapshot={{
					...idleSnapshot(),
					hasMoreBefore: true,
					capabilities: ["prompt_replay", "embedded_context"],
				}}
				onEditMessage={vi.fn(async () => undefined)}
			/>,
		);

		await user.click(screen.getAllByRole("button", { name: "Edit user message" })[0]!);
		expect(screen.getByText(/Reconstructed context:/)).toBeVisible();
	});

	it("uses the durable native anchor when its prompt is on an older page", async () => {
		const user = userEvent.setup();
		render(
			<ChatWorkspace
				snapshot={{
					...idleSnapshot(),
					hasMoreBefore: true,
					capabilities: ["fork"],
					nativeForkAvailableAfterSequence: 1,
				}}
				onEditMessage={vi.fn(async () => undefined)}
			/>,
		);

		const editButtons = screen.getAllByRole("button", {
			name: "Edit user message",
		});
		expect(editButtons).toHaveLength(1);
		await user.click(editButtons[0]!);
		expect(screen.queryByText(/Reconstructed context:/)).not.toBeInTheDocument();
	});

	it("restores the latest composer text after the same session fully remounts", async () => {
		const first = idleSnapshot();
		const draft = "persist every character across a full surface remount";
		const firstView = render(<ChatWorkspace snapshot={first} onSend={vi.fn()} />);
		await typeInLexicalEditor(screen.getByLabelText("Message the agent"), draft);

		firstView.unmount();
		render(
			<ChatWorkspace
				snapshot={{ ...first, conversationId: "replacement-controller-conversation" }}
				onSend={vi.fn()}
			/>,
		);

		await waitFor(() =>
			expect(screen.getByLabelText("Message the agent")).toHaveTextContent(draft),
		);
	});

	it("keeps independent composer drafts for remounted sessions", async () => {
		const sessionA = idleSnapshot();
		const sessionB = {
			...sessionA,
			sessionId: "independent-draft-session",
			conversationId: "independent-draft-conversation",
		};

		const firstView = render(<ChatWorkspace snapshot={sessionA} onSend={vi.fn()} />);
		await typeInLexicalEditor(screen.getByLabelText("Message the agent"), "session A draft");
		firstView.unmount();

		const secondView = render(<ChatWorkspace snapshot={sessionB} onSend={vi.fn()} />);
		const secondComposer = screen.getByLabelText("Message the agent");
		expect(secondComposer).toHaveTextContent("");
		await typeInLexicalEditor(secondComposer, "session B draft");
		secondView.unmount();

		const restoredA = render(<ChatWorkspace snapshot={sessionA} onSend={vi.fn()} />);
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent("session A draft");
		restoredA.unmount();

		render(<ChatWorkspace snapshot={sessionB} onSend={vi.fn()} />);
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent("session B draft");
	});

	it("keeps renderer drafts separate for two hosts with the same daemon session ID", async () => {
		const snapshot = idleSnapshot();
		const session = { ...chatSession, createdAt: "2026-08-25T09:00:00.000Z" };
		const a = `host-A:${snapshot.sessionId}`;
		const b = `host-B:${snapshot.sessionId}`;
		const first = render(<ChatWorkspace snapshot={snapshot} session={session} uiSessionId={a} onSend={vi.fn()} />);
		await typeInLexicalEditor(screen.getByLabelText("Message the agent"), "draft on A");
		first.unmount();

		const second = render(<ChatWorkspace snapshot={snapshot} session={session} uiSessionId={b} onSend={vi.fn()} />);
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent("");
		await typeInLexicalEditor(screen.getByLabelText("Message the agent"), "draft on B");
		second.unmount();

		const restored = render(<ChatWorkspace snapshot={snapshot} session={session} uiSessionId={a} onSend={vi.fn()} />);
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent("draft on A");
		restored.unmount();

		render(<ChatWorkspace snapshot={snapshot} session={session} uiSessionId={b} onSend={vi.fn()} />);
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent("draft on B");
	});

	it("saves a reviewer draft separately from its worker conversation", async () => {
		const snapshot = idleSnapshot();
		const draftOwner = { sessionId: "review:review-1", incarnation: "review-1" };
		const view = render(<ChatWorkspace snapshot={snapshot} draftOwner={draftOwner} onSend={vi.fn()} />);
		await typeInLexicalEditor(screen.getByLabelText("Message the agent"), "reviewer reply");
		await waitFor(() => expect(readChatSessionDraft(draftOwner).composer.text).toBe("reviewer reply"));
		expect(readChatSessionDraft({ sessionId: snapshot.sessionId, incarnation: snapshot.sessionId }).composer.text).toBe("");
		view.unmount();
		render(<ChatWorkspace snapshot={snapshot} draftOwner={draftOwner} onSend={vi.fn()} />);
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent("reviewer reply");
		expect(screen.queryByText("Draft couldn’t be saved.")).not.toBeInTheDocument();
	});

	it("lets only the newest daemon session incarnation own restored drafts", async () => {
		const snapshot = idleSnapshot();
		const firstIncarnation = {
			...chatSession,
			createdAt: "2026-08-25T09:00:00.000Z",
		};
		const replacementIncarnation = {
			...chatSession,
			createdAt: "2026-08-26T09:00:00.000Z",
		};
		const view = render(
			<ChatWorkspace
				snapshot={snapshot}
				session={firstIncarnation}
				onSend={vi.fn()}
			/>,
		);
		await typeInLexicalEditor(
			screen.getByLabelText("Message the agent"),
			"draft from deleted incarnation",
		);

		view.rerender(
			<ChatWorkspace
				snapshot={snapshot}
				session={replacementIncarnation}
				onSend={vi.fn()}
			/>,
		);
		const replacementComposer = await screen.findByLabelText("Message the agent");
		expect(replacementComposer).toHaveTextContent("");
		await typeInLexicalEditor(replacementComposer, "replacement draft");
		await waitFor(() =>
			expect(
				readChatSessionDraft({
					sessionId: snapshot.sessionId,
					incarnation: replacementIncarnation.createdAt,
				}).composer.text,
			).toBe("replacement draft"),
		);

		view.rerender(
			<ChatWorkspace
				snapshot={snapshot}
				session={firstIncarnation}
				onSend={vi.fn()}
			/>,
		);
		expect(await screen.findByRole("alert")).toHaveTextContent(
			"This Chat view belongs to an older session incarnation. Reopen the current session to continue.",
		);
		expect(screen.queryByLabelText("Message the agent")).not.toBeInTheDocument();
		expect(screen.queryByText(/Drafts can’t be saved right now/)).not.toBeInTheDocument();
		expect(
			readChatSessionDraft({
				sessionId: snapshot.sessionId,
				incarnation: replacementIncarnation.createdAt,
			}).composer.text,
		).toBe("replacement draft");
	});

	it("keeps Chat usable in memory when draft storage cannot activate", async () => {
		// Same shape as the inline-edit test above, where "Edit user message" is offered.
		const snapshot: ConversationSnapshot = { ...idleSnapshot(), capabilities: [], hasMoreBefore: false };
		const onSend = vi.fn();
		const session = {
			...chatSession,
			createdAt: "2026-08-26T09:30:00.000Z",
		};
		const backing = window.localStorage;
		const storage = {
			getItem: backing.getItem.bind(backing),
			removeItem: backing.removeItem.bind(backing),
			setItem: (_key: string, _value: string): void => {
				throw new DOMException("blocked", "SecurityError");
			},
		} as Storage;
		const localStorage = vi.spyOn(window, "localStorage", "get").mockReturnValue(storage);

		try {
			render(<ChatWorkspace snapshot={snapshot} session={session} onSend={onSend} onEditMessage={vi.fn()} />);
			expect(
				await screen.findByText(
					"Drafts can’t be saved right now. You can keep chatting, but unsent text won’t be kept if you leave this chat.",
				),
			).toBeInTheDocument();
			expect(screen.queryByText(/older session incarnation/)).not.toBeInTheDocument();
			expect(screen.getByRole("log")).toBeInTheDocument();
			// Editing a sent message depends on a saved draft, so it is not offered.
			expect(screen.queryByRole("button", { name: "Edit user message" })).not.toBeInTheDocument();

			const composer = await screen.findByLabelText("Message the agent");
			await typeInLexicalEditor(composer, "send without draft storage");
			fireEvent.keyDown(composer, { key: "Enter" });
			await waitFor(() => expect(onSend.mock.calls[0]?.[0]).toBe("send without draft storage"));
			expect(
				readChatSessionDraft({ sessionId: snapshot.sessionId, incarnation: session.createdAt }, backing).composer.text,
			).toBe("");
		} finally {
			localStorage.mockRestore();
		}
	});

	it("saves drafts again once storage recovers and the window regains focus", async () => {
		const snapshot = idleSnapshot();
		const session = {
			...chatSession,
			createdAt: "2026-08-26T09:45:00.000Z",
		};
		const backing = window.localStorage;
		let failWrites = true;
		const storage = {
			getItem: backing.getItem.bind(backing),
			removeItem: backing.removeItem.bind(backing),
			setItem: (key: string, value: string) => {
				if (failWrites) throw new DOMException("blocked", "SecurityError");
				backing.setItem(key, value);
			},
		} as Storage;
		const localStorage = vi.spyOn(window, "localStorage", "get").mockReturnValue(storage);

		try {
			render(<ChatWorkspace snapshot={snapshot} session={session} onSend={vi.fn()} />);
			expect(await screen.findByText(/Drafts can’t be saved right now/)).toBeInTheDocument();

			failWrites = false;
			fireEvent.focus(window);
			await waitFor(() => expect(screen.queryByText(/Drafts can’t be saved right now/)).not.toBeInTheDocument());
			await typeInLexicalEditor(await screen.findByLabelText("Message the agent"), "saved after recovery");
			await waitFor(() =>
				expect(
					readChatSessionDraft({ sessionId: snapshot.sessionId, incarnation: session.createdAt }, backing).composer.text,
				).toBe("saved after recovery"),
			);
		} finally {
			localStorage.mockRestore();
		}
	});

	it("retains a rejected send and removes only the accepted session draft", async () => {
		const snapshot = idleSnapshot();
		const onSend = vi
			.fn()
			.mockRejectedValueOnce(new Error("offline"))
			.mockResolvedValueOnce(undefined);
		const firstView = render(<ChatWorkspace snapshot={snapshot} onSend={onSend} />);
		const composer = screen.getByLabelText("Message the agent");
		await typeInLexicalEditor(composer, "retry this exact draft");
		fireEvent.keyDown(composer, { key: "Enter" });
		await screen.findByText(/delivery wasn’t confirmed/);
		const firstClientMessageId = onSend.mock.calls[0]?.[2];
		expect(firstClientMessageId).toEqual(expect.any(String));
		firstView.unmount();

		const retryView = render(<ChatWorkspace snapshot={snapshot} onSend={onSend} />);
		const restored = screen.getByLabelText("Message the agent");
		expect(restored).toHaveTextContent("retry this exact draft");
		await userEvent.click(screen.getByRole("button", { name: "Retry message safely" }));
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(2));
		expect(onSend.mock.calls[1]?.[2]).toBe(firstClientMessageId);
		await waitFor(() =>
			expect(readChatSessionDraft(snapshot.sessionId).composer.text).toBe(""),
		);
		await waitFor(() => expect(restored).toHaveTextContent(""));
		retryView.unmount();

		render(<ChatWorkspace snapshot={snapshot} onSend={onSend} />);
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent("");
	});

	it("keeps the composer clear and editable across a same-session remount during delivery", async () => {
		const snapshot = idleSnapshot();
		let acceptSend!: () => void;
		const onSend = vi.fn(
			() =>
				new Promise<void>((resolve) => {
					acceptSend = resolve;
				}),
		);
		const firstView = render(<ChatWorkspace snapshot={snapshot} onSend={onSend} />);
		const firstComposer = screen.getByLabelText("Message the agent");
		await typeInLexicalEditor(firstComposer, "send exactly once");
		fireEvent.keyDown(firstComposer, { key: "Enter" });
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		firstView.unmount();

		render(<ChatWorkspace snapshot={snapshot} onSend={onSend} />);
		const replacement = screen.getByLabelText("Message the agent");
		expect(replacement.textContent).toBe("");
		expect(replacement).toHaveAttribute("contenteditable", "true");
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(readChatSessionDraft(snapshot.sessionId).composer.delivery?.draft?.text).toBe("send exactly once");
		await typeInLexicalEditor(replacement, "next draft after returning");
		fireEvent.keyDown(replacement, { key: "Enter" });
		expect(onSend).toHaveBeenCalledTimes(1);

		await act(async () => acceptSend());
		await waitFor(() => expect(readChatSessionDraft(snapshot.sessionId).composer.delivery).toBeUndefined());
		expect(replacement).toHaveTextContent("next draft after returning");
		expect(readChatSessionDraft(snapshot.sessionId).composer.text).toBe("next draft after returning");
		expect(getChatComposerMutation(snapshot.sessionId)).toEqual({ pending: false });
	});

	it("restores an inline edit independently and cancelling it preserves the composer draft", async () => {
		const user = userEvent.setup();
		const snapshot = idleSnapshot();
		const common = {
			snapshot,
			onEditMessage: vi.fn(async () => undefined),
		};
		const firstView = render(<ChatWorkspace {...common} />);
		const composer = screen.getByLabelText("Message the agent");
		await typeInLexicalEditor(composer, "independent composer draft");
		await user.click(screen.getAllByRole("button", { name: "Edit user message" })[0]!);
		const edit = screen.getByRole("textbox", { name: "Edit message" });
		await user.clear(edit);
		await user.type(edit, "persist this inline edit");
		firstView.unmount();

		const restoredView = render(<ChatWorkspace {...common} />);
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent(
			"independent composer draft",
		);
		expect(await screen.findByRole("textbox", { name: "Edit message" })).toHaveValue(
			"persist this inline edit",
		);
		await user.click(screen.getByRole("button", { name: "Cancel edit" }));
		expect(screen.queryByRole("textbox", { name: "Edit message" })).not.toBeInTheDocument();
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent(
			"independent composer draft",
		);
		restoredView.unmount();

		render(<ChatWorkspace {...common} />);
		expect(screen.queryByRole("textbox", { name: "Edit message" })).not.toBeInTheDocument();
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent(
			"independent composer draft",
		);
	});

	it("locks and clears an accepted inline edit across a same-session remount", async () => {
		const user = userEvent.setup();
		const snapshot = idleSnapshot();
		let acceptEdit!: () => void;
		const editPending = new Promise<void>((resolve) => {
			acceptEdit = resolve;
		});
		const onEditMessage = vi.fn(() => editPending);
		const common = { snapshot, onEditMessage };

		const firstView = render(<ChatWorkspace {...common} />);
		await user.click(screen.getAllByRole("button", { name: "Edit user message" })[0]!);
		const firstEditor = screen.getByRole("textbox", { name: "Edit message" });
		await user.clear(firstEditor);
		await user.type(firstEditor, "accepted edit");
		fireEvent.keyDown(firstEditor, { key: "Enter", ctrlKey: true });
		await waitFor(() =>
			expect(onEditMessage).toHaveBeenCalledWith(
				"turn-1",
				"accepted edit",
				expect.any(String),
			),
		);
		firstView.unmount();

		render(<ChatWorkspace {...common} />);
		const remountedEditor = await screen.findByRole("textbox", { name: "Edit message" });
		expect(remountedEditor).toBeDisabled();
		expect(remountedEditor).toHaveValue("accepted edit");

		await act(async () => acceptEdit());
		await waitFor(() =>
			expect(screen.queryByRole("textbox", { name: "Edit message" })).not.toBeInTheDocument(),
		);
		expect(readChatSessionDraft(snapshot.sessionId).inlineEdit).toBeUndefined();
		expect(getChatInlineEditMutation(snapshot.sessionId)).toEqual({ pending: false });
	});

	it("durably unlocks a definitively rejected inline edit without consuming its text", async () => {
		const user = userEvent.setup();
		const snapshot = idleSnapshot();
		const onEditMessage = vi.fn(async () => ({
			status: "not-accepted" as const,
			reason: "The provider rejected this edit before accepting it.",
		}));
		const first = render(<ChatWorkspace snapshot={snapshot} onEditMessage={onEditMessage} />);
		await user.click(screen.getAllByRole("button", { name: "Edit user message" })[0]!);
		const editor = screen.getByRole("textbox", { name: "Edit message" });
		await user.clear(editor);
		await user.type(editor, "preserve rejected edit");
		await user.click(screen.getByRole("button", { name: "Send edited message" }));

		await waitFor(() => expect(onEditMessage).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(editor).not.toBeDisabled());
		expect(editor).toHaveValue("preserve rejected edit");
		expect(screen.getByRole("alert")).toHaveTextContent("provider rejected this edit");
		expect(screen.queryByRole("button", { name: "Retry edit safely" })).not.toBeInTheDocument();
		expect(readChatSessionDraft(snapshot.sessionId).inlineEditDelivery).toBeUndefined();
		first.unmount();

		render(<ChatWorkspace snapshot={snapshot} onEditMessage={onEditMessage} />);
		expect(await screen.findByRole("textbox", { name: "Edit message" })).toHaveValue(
			"preserve rejected edit",
		);
		expect(screen.queryByRole("button", { name: "Retry edit safely" })).not.toBeInTheDocument();
		expect(onEditMessage).toHaveBeenCalledTimes(1);
	});

	it("warns before explicitly abandoning uncertain inline-edit recovery after remount", async () => {
		const user = userEvent.setup();
		const snapshot = idleSnapshot();
		const onEditMessage = vi.fn().mockRejectedValue(new Error("outcome unknown"));
		const common = { snapshot, onEditMessage };
		const first = render(<ChatWorkspace {...common} />);
		await user.click(screen.getAllByRole("button", { name: "Edit user message" })[0]!);
		const editor = screen.getByRole("textbox", { name: "Edit message" });
		await user.clear(editor);
		await user.type(editor, "possibly delivered edit");
		await user.click(screen.getByRole("button", { name: "Send edited message" }));

		await waitFor(() => expect(onEditMessage).toHaveBeenCalledTimes(1));
		await waitFor(() =>
			expect(screen.getByRole("alert")).toHaveTextContent("may already have been delivered"),
		);
		expect(screen.getByRole("alert")).toHaveTextContent("may duplicate it");
		first.unmount();

		render(<ChatWorkspace {...common} />);
		const restored = await screen.findByRole("textbox", { name: "Edit message" });
		expect(restored).toBeDisabled();
		expect(restored).toHaveValue("possibly delivered edit");
		expect(onEditMessage).toHaveBeenCalledTimes(1);
		await user.click(screen.getByRole("button", { name: "Abandon edit recovery" }));

		await waitFor(() => expect(restored).not.toBeDisabled());
		expect(restored).toHaveValue("possibly delivered edit");
		expect(onEditMessage).toHaveBeenCalledTimes(1);
		expect(screen.queryByRole("button", { name: "Retry edit safely" })).not.toBeInTheDocument();
		expect(screen.getByRole("alert")).toHaveTextContent("may already have been delivered");
		expect(readChatSessionDraft(snapshot.sessionId).inlineEditDelivery).toBeUndefined();
	});

	it("reports a restored inline delivery as pending recovery without claiming persistence failed", async () => {
		const snapshot = {
			...idleSnapshot(),
			sessionId: "inline-edit-restored-delivery-boundary",
		};
		prepareChatInlineEditDelivery(snapshot.sessionId, {
			turnId: "turn-1",
			text: "recover this edit",
			content: [],
			clientMessageId: "inline-edit-restored-delivery-id",
		});

		render(<ChatWorkspace snapshot={snapshot} onEditMessage={vi.fn()} />);

		expect(await screen.findByRole("alert")).toHaveTextContent(
			"earlier edit may already have been delivered before Chat restarted",
		);
		await waitFor(() => expect(getChatDraftBoundary(snapshot.sessionId)).toBeUndefined());
	});

	it("reconciles an accepted inline delivery without clearing or blocking a later edit", async () => {
		const user = userEvent.setup();
		const snapshot = idleSnapshot();
		let acceptEdit!: () => void;
		const onEditMessage = vi.fn(
			() =>
				new Promise<void>((resolve) => {
					acceptEdit = resolve;
				}),
		);
		const first = render(<ChatWorkspace snapshot={snapshot} onEditMessage={onEditMessage} />);
		await user.click(screen.getAllByRole("button", { name: "Edit user message" })[0]!);
		const editor = screen.getByRole("textbox", { name: "Edit message" });
		await user.clear(editor);
		await user.type(editor, "submitted edit revision");
		fireEvent.keyDown(editor, { key: "Enter", ctrlKey: true });
		await waitFor(() => expect(onEditMessage).toHaveBeenCalledTimes(1));
		first.unmount();
		writeChatInlineEdit(snapshot.sessionId, {
			turnId: "turn-1",
			text: "later edit revision",
			content: [],
		});

		render(<ChatWorkspace snapshot={snapshot} onEditMessage={onEditMessage} />);
		const restored = await screen.findByRole("textbox", { name: "Edit message" });
		expect(restored).toHaveValue("later edit revision");
		expect(restored).toBeDisabled();

		await act(async () => acceptEdit());
		await waitFor(() => expect(restored).not.toBeDisabled());
		expect(restored).toHaveValue("later edit revision");
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		await waitFor(() => expect(getChatDraftBoundary(snapshot.sessionId)).toBeUndefined());
	});

	it("does not expose an accepted inline edit when a hidden replacement committed a later revision", async () => {
		const snapshot = idleSnapshot();
		let acceptFirstEdit!: () => void;
		const firstEditPending = new Promise<void>((resolve) => {
			acceptFirstEdit = resolve;
		});
		const onEditMessage = vi
			.fn()
			.mockImplementationOnce(() => firstEditPending)
			.mockResolvedValue(undefined);
		const common = { snapshot, onEditMessage };
		const surfaces = (
			showOriginal: boolean,
			replacementMode?: "hidden" | "visible",
		) => (
			<>
				{showOriginal ? (
					<div data-testid="original-inline-edit-surface">
						<ChatWorkspace {...common} />
					</div>
				) : null}
				{replacementMode ? (
					<Activity key="replacement" mode={replacementMode}>
						<div data-testid="replacement-inline-edit-surface">
							<ChatWorkspace {...common} />
						</div>
					</Activity>
				) : null}
			</>
		);

		const view = render(surfaces(true));
		const originalSurface = within(screen.getByTestId("original-inline-edit-surface"));
		fireEvent.click(originalSurface.getAllByRole("button", { name: "Edit user message" })[0]!);
		const originalEditor = originalSurface.getByRole("textbox", { name: "Edit message" });
		fireEvent.change(originalEditor, { target: { value: "accepted inline edit" } });

		// Mount once before hiding so React preserves a replacement editor whose local
		// state was seeded from the exact revision that is about to be accepted.
		await act(async () => {
			view.rerender(surfaces(true, "visible"));
		});
		const replacementSurface = within(
			await screen.findByTestId("replacement-inline-edit-surface"),
		);
		expect(replacementSurface.getByRole("textbox", { name: "Edit message" })).toHaveValue(
			"accepted inline edit",
		);
		await act(async () => {
			view.rerender(surfaces(true, "hidden"));
		});

		// The edit event was already queued when submission locked the surface. React
		// processes both in one batch: A is delivered, while B becomes the later durable
		// revision that accepted-clear must preserve.
		await act(async () => {
			fireEvent.keyDown(originalEditor, { key: "Enter", ctrlKey: true });
			fireEvent.change(originalEditor, { target: { value: "later inline edit" } });
		});
		await waitFor(() =>
			expect(onEditMessage).toHaveBeenCalledWith(
				"turn-1",
				"accepted inline edit",
				expect.any(String),
			),
		);
		expect(readChatSessionDraft(snapshot.sessionId).inlineEdit?.text).toBe(
			"later inline edit",
		);

		await act(async () => acceptFirstEdit());
		await waitFor(() => expect(originalEditor).not.toBeDisabled());
		expect(originalEditor).toHaveValue("later inline edit");
		expect(readChatSessionDraft(snapshot.sessionId).inlineEdit?.text).toBe(
			"later inline edit",
		);
		expect(getChatInlineEditMutation(snapshot.sessionId)).toEqual({ pending: false });

		await act(async () => {
			view.rerender(surfaces(false, "visible"));
		});
		const committedReplacement = within(
			screen.getByTestId("replacement-inline-edit-surface"),
		).getByRole("textbox", { name: "Edit message" });
		await waitFor(() => expect(committedReplacement).toHaveValue("later inline edit"));
		expect(onEditMessage).toHaveBeenCalledTimes(1);

		fireEvent.keyDown(committedReplacement, { key: "Enter", ctrlKey: true });
		await waitFor(() => expect(onEditMessage).toHaveBeenCalledTimes(2));
		expect(onEditMessage.mock.calls[1]).toEqual([
			"turn-1",
			"later inline edit",
			expect.any(String),
		]);
		expect(onEditMessage.mock.calls[1]?.[2]).not.toBe(onEditMessage.mock.calls[0]?.[2]);
		await waitFor(() => expect(committedReplacement).not.toBeInTheDocument());
	});

	it("locks an accepted inline edit whose durable clear failed and clears without redispatch", async () => {
		const user = userEvent.setup();
		const snapshot = idleSnapshot();
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
		const onEditMessage = vi.fn(async () => undefined);
		const view = render(<ChatWorkspace snapshot={snapshot} onEditMessage={onEditMessage} />);
		try {
			await user.click(screen.getAllByRole("button", { name: "Edit user message" })[0]!);
			const editor = screen.getByRole("textbox", { name: "Edit message" });
			await user.clear(editor);
			await user.type(editor, "accepted but not cleared");
			fireEvent.keyDown(editor, { key: "Enter", ctrlKey: true });
			await waitFor(() => expect(onEditMessage).toHaveBeenCalledTimes(1));
			expect(await screen.findByRole("alert")).toHaveTextContent("couldn’t be cleared");
			expect(getChatDraftBoundaries(snapshot.sessionId)).toEqual([
				"persistence-failed",
			]);

			fireEvent.keyDown(editor, { key: "Enter", ctrlKey: true });
			expect(onEditMessage).toHaveBeenCalledTimes(1);

			expect(editor).toBeDisabled();
			failRemoval = false;
			await user.click(
				screen.getByRole("button", { name: "Finish clearing accepted edit" }),
			);
			await waitFor(() => expect(editor).not.toBeInTheDocument());
			expect(onEditMessage).toHaveBeenCalledTimes(1);

			await user.click(screen.getAllByRole("button", { name: "Edit user message" })[0]!);
			const nextEditor = screen.getByRole("textbox", { name: "Edit message" });
			await user.clear(nextEditor);
			await user.type(nextEditor, "new edit after recovery");
			fireEvent.keyDown(nextEditor, { key: "Enter", ctrlKey: true });
			await waitFor(() => expect(onEditMessage).toHaveBeenCalledTimes(2));
		} finally {
			view.unmount();
			localStorage.mockRestore();
		}
	});

	it.each(["read", "write"] as const)(
		"does not dispatch an inline edit until its exact draft survives a storage %s failure",
		async (failure) => {
			const user = userEvent.setup();
			const snapshot = idleSnapshot();
			const durableStorage = window.localStorage;
			let storageAvailable = true;
			const storage = {
				getItem: (key: string) => {
					if (!storageAvailable && failure === "read") {
						throw new DOMException("blocked", "SecurityError");
					}
					return durableStorage.getItem(key);
				},
				setItem: (key: string, value: string) => {
					if (!storageAvailable && failure === "write") {
						throw new DOMException("full", "QuotaExceededError");
					}
					durableStorage.setItem(key, value);
				},
				removeItem: durableStorage.removeItem.bind(durableStorage),
			} as Storage;
			const localStorage = vi.spyOn(window, "localStorage", "get").mockReturnValue(storage);
			const onEditMessage = vi.fn(async () => undefined);
			const view = render(<ChatWorkspace snapshot={snapshot} onEditMessage={onEditMessage} />);
			try {
				await user.click(screen.getAllByRole("button", { name: "Edit user message" })[0]!);
				const editor = screen.getByRole("textbox", { name: "Edit message" });
				await user.clear(editor);
				await user.type(editor, "prove exact inline edit");
				storageAvailable = false;
				fireEvent.keyDown(editor, { key: "Enter", ctrlKey: true });

				await waitFor(() =>
					expect(screen.getByRole("alert")).toHaveTextContent("Nothing was sent"),
				);
				expect(onEditMessage).not.toHaveBeenCalled();

				storageAvailable = true;
				fireEvent.keyDown(editor, { key: "Enter", ctrlKey: true });
				await waitFor(() => expect(onEditMessage).toHaveBeenCalledTimes(1));
				expect(onEditMessage).toHaveBeenCalledWith(
					"turn-1",
					"prove exact inline edit",
					expect.any(String),
				);
			} finally {
				view.unmount();
				localStorage.mockRestore();
			}
		},
	);

	it("blocks destructive boundaries when an inline edit cannot be persisted", async () => {
		const user = userEvent.setup();
		const snapshot = idleSnapshot();
		const durableStorage = window.localStorage;
		const storage = {
			getItem: durableStorage.getItem.bind(durableStorage),
			removeItem: durableStorage.removeItem.bind(durableStorage),
			setItem: (key: string, value: string) => {
				if (key.includes(encodeURIComponent(snapshot.sessionId))) {
					throw new DOMException("full", "QuotaExceededError");
				}
				durableStorage.setItem(key, value);
			},
		} as Storage;
		const localStorage = vi.spyOn(window, "localStorage", "get").mockReturnValue(storage);
		const view = render(
			<ChatWorkspace snapshot={snapshot} onEditMessage={vi.fn(async () => undefined)} />,
		);
		try {
			await user.click(screen.getAllByRole("button", { name: "Edit user message" })[0]!);
			await waitFor(() =>
				expect(getChatDraftBoundary(snapshot.sessionId)).toBe("persistence-failed"),
			);
			expect(screen.getByRole("alert")).toHaveTextContent("Inline edit couldn’t be saved");
		} finally {
			view.unmount();
			localStorage.mockRestore();
		}
		expect(getChatDraftBoundary(snapshot.sessionId)).toBeUndefined();
	});

	it("carries pending attachment staging through a same-session remount", async () => {
		const snapshot = idleSnapshot();
		let finishStaging!: (paths: string[]) => void;
		const onStageAttachments = vi.fn(
			() =>
				new Promise<string[]>((resolve) => {
					finishStaging = resolve;
				}),
		);
		const common = { snapshot, onSend: vi.fn(), onStageAttachments };
		const firstView = render(<ChatWorkspace {...common} />);
		fireEvent.paste(screen.getByLabelText("Message the agent"), {
			clipboardData: {
				files: [new File([new Uint8Array([1, 2, 3])], "pending.txt", { type: "text/plain" })],
				items: [],
			},
		});
		await waitFor(() => expect(onStageAttachments).toHaveBeenCalledTimes(1));
		expect(screen.getByRole("status")).toHaveTextContent("Saving attachments");
		firstView.unmount();

		render(<ChatWorkspace {...common} />);
		expect(screen.getByRole("status")).toHaveTextContent("Saving attachments");
		await act(async () => finishStaging([".tallpine/attachments/attachment-pending.txt"]));

		expect(await screen.findByLabelText("Remove pending.txt")).toBeInTheDocument();
		await waitFor(() => expect(screen.queryByText("Saving attachments… Wait before leaving this chat.")).toBeNull());
		expect(readChatSessionDraft(snapshot.sessionId).composer.attachments).toEqual([
			expect.objectContaining({
				name: "pending.txt",
				path: ".tallpine/attachments/attachment-pending.txt",
			}),
		]);
	});

	it("keeps the replacement surface unsafe when remounted attachment persistence fails", async () => {
		const snapshot = idleSnapshot();
		const durableStorage = window.localStorage;
		const storage = {
			getItem: durableStorage.getItem.bind(durableStorage),
			removeItem: durableStorage.removeItem.bind(durableStorage),
			setItem: (key: string, value: string) => {
				if (key.includes(encodeURIComponent(snapshot.sessionId))) {
					throw new DOMException("full", "QuotaExceededError");
				}
				durableStorage.setItem(key, value);
			},
		} as Storage;
		const localStorage = vi.spyOn(window, "localStorage", "get").mockReturnValue(storage);
		let finishStaging!: (paths: string[]) => void;
		const onStageAttachments = vi.fn(
			() =>
				new Promise<string[]>((resolve) => {
					finishStaging = resolve;
				}),
		);
		const common = { snapshot, onSend: vi.fn(), onStageAttachments };
		const firstView = render(<ChatWorkspace {...common} />);
		fireEvent.paste(screen.getByLabelText("Message the agent"), {
			clipboardData: {
				files: [new File([new Uint8Array([1])], "unsafe.txt", { type: "text/plain" })],
				items: [],
			},
		});
		await waitFor(() => expect(onStageAttachments).toHaveBeenCalledTimes(1));
		firstView.unmount();
		await act(async () => {
			finishStaging([".tallpine/attachments/attachment-unsafe.txt"]);
			await Promise.resolve();
			await Promise.resolve();
		});

		const replacement = render(<ChatWorkspace {...common} />);
		try {
			expect(await screen.findByLabelText("Remove unsafe.txt")).toBeInTheDocument();
			expect(await screen.findByRole("alert")).toHaveTextContent("Draft couldn’t be saved");
			await waitFor(() =>
				expect(getChatDraftBoundary(snapshot.sessionId)).toBe("persistence-failed"),
			);
		} finally {
			replacement.unmount();
			localStorage.mockRestore();
		}
	});

	it("restores only durably staged attachments and does not resurrect them after an accepted send", async () => {
		const snapshot = idleSnapshot();
		const onStageAttachments = vi.fn(async () => [
			".tallpine/attachments/attachment-durable.png",
		]);
		const onSend = vi.fn(async (_text: string) => undefined);
		const common = { snapshot, onSend, onStageAttachments };
		const firstView = render(<ChatWorkspace {...common} />);
		const composer = screen.getByLabelText("Message the agent");
		fireEvent.paste(composer, {
			clipboardData: {
				files: [
					new File([new Uint8Array([137, 80, 78, 71])], "durable.png", {
						type: "image/png",
					}),
				],
				items: [],
			},
		});
		await waitFor(() => expect(onStageAttachments).toHaveBeenCalledTimes(1));
		await screen.findByLabelText("Remove durable.png");
		firstView.unmount();

		const restoredView = render(<ChatWorkspace {...common} />);
		expect(await screen.findByLabelText("Remove durable.png")).toBeInTheDocument();
		const restoredComposer = screen.getByLabelText("Message the agent");
		await typeInLexicalEditor(restoredComposer, "send the restored attachment");
		await userEvent.click(screen.getByRole("button", { name: "Send message" }));
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		expect(onSend.mock.calls[0]?.[0]).toContain(
			".tallpine/attachments/attachment-durable.png",
		);
		expect(onStageAttachments).toHaveBeenCalledTimes(1);
		restoredView.unmount();

		render(<ChatWorkspace {...common} />);
		expect(screen.queryByLabelText("Remove durable.png")).not.toBeInTheDocument();
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent("");
	});

	it("copies a human message as the exact text the user sent", async () => {
		const user = userEvent.setup();
		render(<ChatWorkspace snapshot={chatFixture} />);

		await user.click(screen.getAllByRole("button", { name: "Copy user message" })[0]!);

		expect(writeText).toHaveBeenCalledTimes(1);
		expect(writeText).toHaveBeenCalledWith(
			"Check the worktree state and tell me what changed since the base commit.",
		);
	});

	it("edits a human message through the branch endpoint without touching the composer", async () => {
		const user = userEvent.setup();
		const onRollback = vi.fn();
		const onEditMessage = vi.fn(async () => undefined);
		render(
			<ChatWorkspace
				snapshot={idleSnapshot()}
				onRollback={onRollback}
				onEditMessage={onEditMessage}
			/>,
		);
		const composer = screen.getByLabelText("Message the agent");
		await typeInLexicalEditor(composer, "unsent composer draft");

		await user.click(screen.getAllByRole("button", { name: "Edit user message" })[0]!);
		expect(composer).toHaveTextContent("unsent composer draft");

		const editor = screen.getByRole("textbox", { name: "Edit message" });
		expect(editor).toHaveFocus();
		expect(editor).toHaveValue(
			"Check the worktree state and tell me what changed since the base commit.",
		);

		await user.clear(editor);
		await user.type(editor, "Check worktree state, including staged files.");
		fireEvent.keyDown(editor, { key: "Enter", metaKey: true });

		await waitFor(() =>
			expect(onEditMessage).toHaveBeenCalledWith(
				"turn-1",
				"Check worktree state, including staged files.",
				expect.any(String),
			),
		);
		expect(onRollback).not.toHaveBeenCalled();
		expect(composer).toHaveTextContent("unsent composer draft");
	});

	it("hides editing while another turn is active", () => {
		render(<ChatWorkspace snapshot={chatFixture} onEditMessage={vi.fn(async () => undefined)} />);

		expect(screen.queryByRole("button", { name: "Edit user message" })).not.toBeInTheDocument();
	});

	it("retains the inline draft when branch creation fails", async () => {
		const user = userEvent.setup();
		const onEditMessage = vi.fn(async () => {
			throw new Error("branch failed");
		});
		const approximateSnapshot = {
			...idleSnapshot(),
			capabilities: ["prompt_replay", "embedded_context"],
		};
		const view = render(
			<ChatWorkspace
				snapshot={approximateSnapshot}
				onEditMessage={onEditMessage}
				editMessageError="branch failed"
			/>,
		);

		await user.click(screen.getAllByRole("button", { name: "Edit user message" })[1]!);
		const editor = screen.getByRole("textbox", { name: "Edit message" });
		await user.clear(editor);
		await user.type(editor, "keep this draft");
		fireEvent.keyDown(editor, { key: "Enter", ctrlKey: true });

		await waitFor(() =>
			expect(onEditMessage).toHaveBeenCalledWith(
				"turn-2",
				"keep this draft",
				expect.any(String),
			),
		);
		expect(screen.getByRole("textbox", { name: "Edit message" })).toHaveValue("keep this draft");
		expect(screen.getByRole("alert")).toHaveTextContent("branch failed");
		expect(screen.getByText(/Reconstructed context:/)).toBeVisible();

		view.rerender(
			<ChatWorkspace
				snapshot={{
					...approximateSnapshot,
					items: [],
					turns: [],
				}}
				onEditMessage={onEditMessage}
				editMessageError="branch failed"
			/>,
		);
		expect(screen.getByRole("textbox", { name: "Edit message" })).toHaveValue("keep this draft");
		expect(screen.getByRole("alert")).toHaveTextContent("branch failed");
		expect(screen.getByText(/Reconstructed context:/)).toBeVisible();

		// An ambiguous provider failure can durably activate the replacement branch.
		// Keep its journal actionable when that branch refetch arrives: the user still
		// needs the exact editor to retry with the same ID or abandon recovery.
		view.rerender(
			<ChatWorkspace
				snapshot={{
					...approximateSnapshot,
					activeBranchId: "branch-failed",
					branchedFromEarlierMessage: true,
					items: [],
					turns: [],
				}}
				onEditMessage={onEditMessage}
				editMessageError="branch failed"
			/>,
		);
		const recoveringEditor = await screen.findByRole("textbox", { name: "Edit message" });
		expect(recoveringEditor).toBeDisabled();
		expect(recoveringEditor).toHaveValue("keep this draft");
		expect(screen.getByRole("button", { name: "Retry edit safely" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Abandon edit recovery" })).toBeInTheDocument();
		await waitFor(() =>
			expect(getChatDraftBoundary(approximateSnapshot.sessionId)).toBeUndefined(),
		);
	});

	it("lets the user retry a failed replacement on the active approximate branch", async () => {
		const user = userEvent.setup();
		const snapshot = idleSnapshot();
		render(
			<ChatWorkspace
				snapshot={{
					...snapshot,
					activeBranchId: "branch-failed",
					branchedFromEarlierMessage: true,
					branchMaterialization: {
						strategy: "approximate_context",
						replayTruncated: false,
					},
					capabilities: ["prompt_replay", "embedded_context"],
					turns: snapshot.turns.map((turn) =>
						turn.id === "turn-2"
							? {
									...turn,
									state: "failed" as const,
									providerTurnId: undefined,
									errorMessage: "provider unavailable",
								}
							: turn,
					),
				}}
				onEditMessage={vi.fn(async () => undefined)}
			/>,
		);

		const edits = screen.getAllByRole("button", {
			name: "Edit user message",
		});
		expect(edits).toHaveLength(2);
		await user.click(edits[1]!);
		expect(screen.getByRole("textbox", { name: "Edit message" })).toBeVisible();
	});

	it("navigates prompt branches without a persistent context notice", async () => {
		const user = userEvent.setup();
		const onActivateBranch = vi.fn(async () => undefined);
		const snapshot = {
			...idleSnapshot(),
			activeBranchId: "branch-current",
			branchedFromEarlierMessage: true,
			branchMaterialization: {
				strategy: "approximate_context" as const,
				replayTruncated: true,
			},
			branchPoints: [
				{
					turnId: "turn-1",
					position: 2,
					total: 3,
					previousBranchId: "branch-previous",
					nextBranchId: "branch-next",
				},
			],
		};
		render(<ChatWorkspace snapshot={snapshot} onActivateBranch={onActivateBranch} />);

		expect(screen.getByText("2 / 3")).toBeVisible();
		expect(screen.queryByText(/Reconstructed context/)).not.toBeInTheDocument();
		await user.click(
			screen.getByRole("button", {
				name: "Previous conversation branch",
			}),
		);
		expect(onActivateBranch).toHaveBeenCalledWith("branch-previous");
	});

	it("does not label a provider-native branch as exact context", () => {
		render(
			<ChatWorkspace
				snapshot={{
					...idleSnapshot(),
					branchedFromEarlierMessage: true,
					branchMaterialization: {
						strategy: "native",
						replayTruncated: false,
					},
				}}
			/>,
		);

		expect(screen.queryByText(/Exact context/)).not.toBeInTheDocument();
	});

	it("copies an assistant message as the markdown the agent wrote", async () => {
		const user = userEvent.setup();
		const snapshot = structuredClone(chatFixture);
		const finalIndex = snapshot.items.findIndex((item) => item.id === "m-2");
		const finalMessage = snapshot.items[finalIndex] as ConversationMessage;
		snapshot.items.splice(finalIndex, 0, {
			...finalMessage,
			id: "m-intermediate",
			sequence: finalMessage.sequence - 0.5,
			text: "Intermediate progress while the turn is still working.",
		});
		render(<ChatWorkspace snapshot={snapshot} />);

		const copies = screen.getAllByRole("button", {
			name: /copy message as markdown/i,
		});
		expect(copies).toHaveLength(1);
		const copy = copies[0]!;
		await user.click(copy);

		expect(writeText).toHaveBeenCalledTimes(1);
		const copied = writeText.mock.calls[0]![0];
		// The stored text, fences and all — not a re-serialization of the rendered DOM.
		expect(copied).toContain("```go");
		expect(copied).toContain("func (m *Manager) Spawn");
		expect(copied).not.toContain("Intermediate progress");
	});

	it("offers no copy on a message that is still arriving", () => {
		const snapshot = structuredClone(chatFixture);
		snapshot.items = snapshot.items.filter((item) => item.sequence <= 12);
		render(<ChatWorkspace snapshot={snapshot} />);
		// The latest assistant message is mid-stream; its copy action remains mounted
		// in the bottom row while the response continues.
		expect(screen.queryByLabelText("still writing")).not.toBeInTheDocument();
		expect(screen.queryByText("Writing…")).not.toBeInTheDocument();
	});

	it("does not show a writing caret while prose is streaming", () => {
		render(<ChatWorkspace snapshot={chatFixture} />);
		expect(screen.queryByLabelText("still writing")).not.toBeInTheDocument();
	});
});

