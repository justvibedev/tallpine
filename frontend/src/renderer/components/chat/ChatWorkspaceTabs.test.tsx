// The setup module registers vi.mock calls, so it must be imported before ChatWorkspace.
import {
	previousTabListeners,
	nextTabListeners,
	closeShellTerminalListeners,
	closeShellTerminalShortcutStates,
	terminalPaneState,
	cleanupChatWorkspaceTest,
	resetChatWorkspaceMocks,
} from "./ChatWorkspace.test.setup";
import {
	act,
	fireEvent,
	screen,
	waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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
import { renderChat as render } from "../../test/render-chat";
import {
	idleSnapshot,
	stubGeometry,
	chatSession,
} from "./ChatWorkspaceTestHelpers";


beforeEach(resetChatWorkspaceMocks);

afterEach(async () => {
	await cleanupChatWorkspaceTest();
});

describe("ChatWorkspace reviewer tabs", () => {
	const reviewerTerminal = { handleId: "review-1", harness: "codex" };
	const reviewerTarget = {
		kind: "reviewer" as const,
		...reviewerTerminal,
		sessionId: chatSession.id,
	};

	it("makes each full-height tile its semantic click target", () => {
		const onOpenReviewerTerminal = vi.fn();
		render(
			<ChatWorkspace
				snapshot={idleSnapshot()}
				session={chatSession}
				reviewerTerminal={reviewerTerminal}
				onOpenReviewerTerminal={onOpenReviewerTerminal}
			/>,
		);

		const chatTab = screen.getByRole("tab", { name: /^Reviewer chat/ });
		const reviewerTab = screen.getByRole("tab", { name: "Reviewer" });
		expect(chatTab).toHaveClass("px-2", "cursor-pointer");
		expect(chatTab.closest("[data-terminal-tab-frame]")).toHaveClass("self-stretch");
		expect(reviewerTab).toHaveClass(
			"self-stretch",
			"px-3",
			"cursor-pointer",
			"focus-visible:outline-2",
		);
		expect(reviewerTab.querySelector('img, [style*="mask"]')).toBeInTheDocument();

		fireEvent.click(reviewerTab);
		expect(onOpenReviewerTerminal).toHaveBeenCalledWith(reviewerTerminal);
	});

	it("removes the active highlight from the reviewer while a workspace file is selected", () => {
		render(
			<ChatWorkspace
				reviewerTarget={reviewerTarget}
				reviewerTerminal={reviewerTerminal}
				session={chatSession}
				snapshot={idleSnapshot()}
				workspaceActiveTabKey="file:README.md"
				workspaceTabs={[
					{
						key: "file:README.md",
						content: <button aria-selected="true" role="tab">README.md</button>,
						onSelect: vi.fn(),
					},
				]}
			/>,
		);

		expect(screen.getByRole("tab", { name: "Reviewer" })).toHaveAttribute("aria-selected", "false");
		expect(screen.getByRole("tab", { name: "README.md" })).toHaveAttribute("aria-selected", "true");
	});

	it("hides and inerts the worker conversation while a typed reviewer owns the body", () => {
		render(<ChatWorkspace snapshot={idleSnapshot()} reviewerChatSelected session={chatSession} />);

		const workerConversation = screen.getByTestId("chat-conversation-panel");
		expect(workerConversation).toHaveAttribute("hidden");
		expect(workerConversation).toHaveAttribute("aria-hidden", "true");
		expect(workerConversation).toHaveAttribute("inert");
	});

	it("keeps the chat draft, attachments, edit, and scroll state mounted while Reviewer is selected", async () => {
		const user = userEvent.setup();
		const common = {
			snapshot: idleSnapshot(),
			session: chatSession,
			reviewerTerminal,
			onOpenReviewerTerminal: vi.fn(),
			onSelectChat: vi.fn(),
			onEditMessage: vi.fn(async () => undefined),
			onStageAttachments: vi.fn(async () => [
				".tallpine/attachments/attachment-review.png",
			]),
		};
		const view = render(<ChatWorkspace {...common} />);
		const composer = screen.getByRole("combobox", {
			name: "Message the agent",
		});
		await typeInLexicalEditor(composer, "unsent reviewer-switch draft");
		fireEvent.paste(composer, {
			clipboardData: {
				files: [
					new File([new Uint8Array([137, 80, 78, 71])], "review.png", {
						type: "image/png",
					}),
				],
				items: [],
			},
		});
		await waitFor(() => expect(screen.getByLabelText("Remove review.png")).toBeInTheDocument());
		const attachment = screen.getByLabelText("Remove review.png");
		await user.click(screen.getAllByRole("button", { name: "Edit user message" })[0]!);
		const editor = screen.getByRole("textbox", { name: "Edit message" });
		await user.clear(editor);
		await user.type(editor, "in-progress branch edit");
		const timeline = screen.getByRole("log", { name: "Conversation" });
		stubGeometry(timeline, {
			scrollHeight: 2_000,
			clientHeight: 500,
			scrollTop: 417,
		});

		view.rerender(<ChatWorkspace {...common} reviewerTarget={reviewerTarget} />);

		expect(composer).toBeInTheDocument();
		expect(attachment).toBeInTheDocument();
		expect(editor).toBeInTheDocument();
		expect(timeline).toBeInTheDocument();
		expect(screen.getByTestId("chat-conversation-panel")).toHaveAttribute("hidden");
		expect(screen.getByTestId("chat-conversation-panel")).toHaveAttribute("inert");

		view.rerender(<ChatWorkspace {...common} />);

		expect(screen.getByRole("combobox", { name: "Message the agent" })).toBe(composer);
		expect(composer).toHaveTextContent("unsent reviewer-switch draft");
		expect(screen.getByLabelText("Remove review.png")).toBe(attachment);
		expect(screen.getByRole("textbox", { name: "Edit message" })).toBe(editor);
		expect(editor).toHaveValue("in-progress branch edit");
		expect(screen.getByRole("log", { name: "Conversation" })).toBe(timeline);
		expect(timeline.scrollTop).toBe(417);
	});

	it("keeps the selected reviewer pane through temporary terminal unavailability", () => {
		const common = {
			snapshot: idleSnapshot(),
			session: chatSession,
			reviewerTarget,
			onSelectChat: vi.fn(),
		};
		const view = render(<ChatWorkspace {...common} reviewerTerminal={reviewerTerminal} />);
		expect(screen.getByTestId("chat-reviewer-terminal")).toBeInTheDocument();
		expect(screen.getByTestId("chat-reviewer-terminal")).not.toHaveClass("pl-2");

		view.rerender(<ChatWorkspace {...common} reviewerTerminal={undefined} />);

		expect(screen.getByTestId("chat-reviewer-terminal")).toBeInTheDocument();
		expect(screen.queryByRole("tab", { name: "Reviewer" })).not.toBeInTheDocument();
	});

	it("gives the reviewer terminal working zoom and fullscreen controls", async () => {
		window.localStorage.setItem("ao.terminal.fontSize", "14");
		render(
			<ChatWorkspace
				snapshot={idleSnapshot()}
				session={chatSession}
				reviewerTerminal={reviewerTerminal}
				reviewerTarget={reviewerTarget}
			/>,
		);

		expect(terminalPaneState.props).toMatchObject({
			fontSize: 14,
			isFullscreen: false,
		});
		expect(terminalPaneState.props?.onChangeFontSize).toEqual(expect.any(Function));
		expect(terminalPaneState.props?.onToggleFullscreen).toEqual(expect.any(Function));

		act(() => terminalPaneState.props?.onChangeFontSize?.(2));
		expect(window.localStorage.getItem("ao.terminal.fontSize")).toBe("16");
		expect(terminalPaneState.props?.fontSize).toBe(16);

		fireEvent.wheel(screen.getByTestId("chat-reviewer-panel"), {
			ctrlKey: true,
			deltaY: -80,
		});
		expect(window.localStorage.getItem("ao.terminal.fontSize")).toBe("17");
		expect(terminalPaneState.props?.fontSize).toBe(17);

		const surface = screen.getByLabelText("Chat");
		const requestFullscreen = vi.fn(async () => undefined);
		Object.defineProperty(surface, "requestFullscreen", {
			configurable: true,
			value: requestFullscreen,
		});
		await act(async () => terminalPaneState.props?.onToggleFullscreen?.());
		expect(requestFullscreen).toHaveBeenCalledOnce();
	});

	it("passes the chat surface's fullscreen state to the terminal as the document enters and leaves fullscreen", () => {
		render(
			<ChatWorkspace
				snapshot={idleSnapshot()}
				session={chatSession}
				reviewerTerminal={reviewerTerminal}
				reviewerTarget={reviewerTarget}
			/>,
		);
		const surface = screen.getByLabelText("Chat");
		const setFullscreenElement = (element: Element | null) => {
			Object.defineProperty(document, "fullscreenElement", { configurable: true, get: () => element });
			act(() => { document.dispatchEvent(new Event("fullscreenchange")); });
		};

		expect(terminalPaneState.props?.isFullscreen).toBe(false);
		setFullscreenElement(surface);
		expect(terminalPaneState.props?.isFullscreen).toBe(true);
		setFullscreenElement(document.body);
		expect(terminalPaneState.props?.isFullscreen).toBe(false);
		delete (document as { fullscreenElement?: unknown }).fullscreenElement;
	});

	it("supports arrow, Home/End, and desktop previous/next tab shortcuts", () => {
		const onOpenReviewerTerminal = vi.fn();
		const onSelectChat = vi.fn();
		const common = {
			snapshot: idleSnapshot(),
			session: chatSession,
			reviewerTerminal,
			onOpenReviewerTerminal,
			onSelectChat,
		};
		const view = render(<ChatWorkspace {...common} />);
		const chatTab = screen.getByRole("tab", {
			name: /^Reviewer chat/,
		});
		const reviewerTab = screen.getByRole("tab", { name: "Reviewer" });
		expect(chatTab).toHaveAttribute("tabindex", "0");
		expect(reviewerTab).toHaveAttribute("tabindex", "-1");

		chatTab.focus();
		fireEvent.keyDown(chatTab, { key: "End" });
		expect(reviewerTab).toHaveFocus();
		expect(onOpenReviewerTerminal).toHaveBeenCalledWith(reviewerTerminal);

		onOpenReviewerTerminal.mockClear();
		chatTab.focus();
		fireEvent.keyDown(chatTab, { key: "ArrowRight" });
		expect(reviewerTab).toHaveFocus();
		expect(onOpenReviewerTerminal).toHaveBeenCalledWith(reviewerTerminal);

		onOpenReviewerTerminal.mockClear();
		expect(nextTabListeners.size).toBe(1);
		act(() => [...nextTabListeners][0]?.());
		expect(onOpenReviewerTerminal).toHaveBeenCalledWith(reviewerTerminal);

		view.rerender(<ChatWorkspace {...common} reviewerTarget={reviewerTarget} />);
		const activeReviewerTab = screen.getByRole("tab", { name: "Reviewer" });
		expect(screen.getByRole("tab", { name: /^Reviewer chat/ })).toHaveAttribute(
			"tabindex",
			"-1",
		);
		expect(activeReviewerTab).toHaveAttribute("tabindex", "0");

		activeReviewerTab.focus();
		fireEvent.keyDown(activeReviewerTab, { key: "Home" });
		expect(onSelectChat).toHaveBeenCalledOnce();
		expect(screen.getByRole("tab", { name: /^Reviewer chat/ })).toHaveFocus();

		onSelectChat.mockClear();
		activeReviewerTab.focus();
		fireEvent.keyDown(activeReviewerTab, { key: "ArrowLeft" });
		expect(onSelectChat).toHaveBeenCalledOnce();

		onSelectChat.mockClear();
		expect(previousTabListeners.size).toBe(1);
		act(() => [...previousTabListeners][0]?.());
		expect(onSelectChat).toHaveBeenCalledOnce();
	});
});

describe("ChatWorkspace shell tabs", () => {
	const shells = [
		{
			handleId: "shell-1",
			sessionId: chatFixture.sessionId,
			title: "chat worktree shell",
			workingDir: "/p",
			createdAt: "2026-08-04T00:00:00Z",
		},
		{
			handleId: "shell-2",
			sessionId: chatFixture.sessionId,
			title: "second shell",
			workingDir: "/p",
			createdAt: "2026-08-04T00:10:00Z",
		},
	];
	const shellTarget = (handleId: string) => {
		const shell = shells.find((candidate) => candidate.handleId === handleId)!;
		return {
			kind: "shell" as const,
			generation: shell.createdAt,
			handleId,
			sessionId: chatFixture.sessionId,
			title: shell.title,
		};
	};

	// Shells are tabs inside the chat surface (#4033): opening one must not cost
	// the conversation — the conversation panel hides instead of unmounting,
	// exactly like the reviewer pane.
	it("renders a selected shell as the tab body and hides, not unmounts, the conversation", () => {
		render(
			<ChatWorkspace
				snapshot={idleSnapshot()}
				session={chatSession}
				shellTerminals={shells}
				shellTarget={shellTarget("shell-1")}
				onSelectChat={vi.fn()}
			/>,
		);
		expect(screen.getByTestId("chat-shell-terminal")).toBeInTheDocument();
		expect(screen.getByTestId("chat-shell-terminal")).not.toHaveClass("pl-2");
		expect(screen.getByTestId("chat-conversation-panel")).toHaveAttribute("hidden");
		expect(screen.getByTestId("chat-conversation-panel")).toHaveAttribute("inert");

		expect(screen.getByRole("tab", { name: "chat worktree shell" })).toHaveAttribute(
			"aria-selected",
			"true",
		);
		expect(screen.getByRole("tab", { name: "second shell" })).toHaveAttribute(
			"aria-selected",
			"false",
		);
	});

	it("passes the routed shell target straight to the terminal pane", () => {
		render(
			<ChatWorkspace
				snapshot={idleSnapshot()}
				session={chatSession}
				shellTerminals={shells}
				shellTarget={shellTarget("shell-2")}
			/>,
		);
		expect(terminalPaneState.props).toMatchObject({
			focusRequested: true,
			terminalTarget: shellTarget("shell-2"),
		});
	});

	it("cycles chat → reviewer → shells → chat on the desktop next-tab shortcut", () => {
		const onOpenReviewerTerminal = vi.fn();
		const onSelectShellTerminal = vi.fn();
		const onSelectChat = vi.fn();
		const common = {
			snapshot: idleSnapshot(),
			session: chatSession,
			shellTerminals: shells,
			onSelectShellTerminal,
			onSelectChat,
		};
		const view = render(
			<ChatWorkspace
				{...common}
				reviewerTerminal={{ handleId: "review-1", harness: "codex" }}
				onOpenReviewerTerminal={onOpenReviewerTerminal}
			/>,
		);

		// From chat: the reviewer comes first.
		act(() => [...nextTabListeners][0]?.());
		expect(onOpenReviewerTerminal).toHaveBeenCalledOnce();

		// From the reviewer: the first shell.
		view.rerender(
			<ChatWorkspace
				{...common}
				reviewerTerminal={{ handleId: "review-1", harness: "codex" }}
				onOpenReviewerTerminal={onOpenReviewerTerminal}
				reviewerTarget={{
					kind: "reviewer",
					handleId: "review-1",
					harness: "codex",
					sessionId: chatFixture.sessionId,
				}}
			/>,
		);
		act(() => [...nextTabListeners][0]?.());
		expect(onSelectShellTerminal).toHaveBeenCalledWith("shell-1");

		// From the last shell: wraps to chat.
		view.rerender(<ChatWorkspace {...common} shellTarget={shellTarget("shell-2")} />);
		act(() => [...nextTabListeners][0]?.());
		expect(onSelectChat).toHaveBeenCalledOnce();
	});

	it("cycles from the focused worker tab on Ctrl+Tab", () => {
		const onSelectShellTerminal = vi.fn();
		render(
			<ChatWorkspace
				snapshot={idleSnapshot()}
				session={chatSession}
				shellTerminals={shells}
				onSelectShellTerminal={onSelectShellTerminal}
				onSelectChat={vi.fn()}
			/>,
		);

		const workerTab = screen.getByRole("tab", { name: /^Reviewer chat/ });
		workerTab.focus();
		fireEvent.keyDown(workerTab, { key: "Tab", ctrlKey: true });

		expect(onSelectShellTerminal).toHaveBeenCalledWith("shell-1");
	});

	it("cycles shell → reviewer → chat on the desktop previous-tab shortcut", () => {
		const onOpenReviewerTerminal = vi.fn();
		const onSelectShellTerminal = vi.fn();
		const onSelectChat = vi.fn();
		const common = {
			snapshot: idleSnapshot(),
			session: chatSession,
			reviewerTerminal: { handleId: "review-1", harness: "codex" },
			onOpenReviewerTerminal,
			shellTerminals: shells,
			onSelectShellTerminal,
			onSelectChat,
		};
		const view = render(<ChatWorkspace {...common} shellTarget={shellTarget("shell-1")} />);

		act(() => [...previousTabListeners][0]?.());
		expect(onOpenReviewerTerminal).toHaveBeenCalledWith({
			handleId: "review-1",
			harness: "codex",
		});

		view.rerender(
			<ChatWorkspace
				{...common}
				reviewerTarget={{
					kind: "reviewer",
					handleId: "review-1",
					harness: "codex",
					sessionId: chatFixture.sessionId,
				}}
			/>,
		);
		act(() => [...previousTabListeners][0]?.());
		expect(onSelectChat).toHaveBeenCalledOnce();
	});

	it("enables the close-shell shortcut and closes the active shell tab", () => {
		const onCloseShellTerminal = vi.fn();
		const view = render(
			<ChatWorkspace
				snapshot={idleSnapshot()}
				session={chatSession}
				shellTerminals={shells}
				shellTarget={shellTarget("shell-2")}
				onCloseShellTerminal={onCloseShellTerminal}
			/>,
		);

		expect(closeShellTerminalShortcutStates.at(-1)).toBe(true);
		act(() => [...closeShellTerminalListeners][0]?.());
		expect(onCloseShellTerminal).toHaveBeenCalledWith("shell-2");

		view.rerender(
			<ChatWorkspace
				snapshot={idleSnapshot()}
				session={chatSession}
				shellTerminals={shells}
				onCloseShellTerminal={onCloseShellTerminal}
			/>,
		);
		expect(closeShellTerminalShortcutStates.at(-1)).toBe(false);
	});

	it("enables the close shortcut and closes the active workspace file tab", () => {
		const onClose = vi.fn();
		render(
			<ChatWorkspace
				snapshot={idleSnapshot()}
				workspaceActiveTabKey="file:README.md"
				workspaceTabs={[
					{
						key: "file:README.md",
						content: <button role="tab">README.md</button>,
						onSelect: vi.fn(),
						onClose,
					},
				]}
			/>,
		);

		expect(closeShellTerminalShortcutStates.at(-1)).toBe(true);
		act(() => [...closeShellTerminalListeners][0]?.());
		expect(onClose).toHaveBeenCalledOnce();
	});

	it("removes the active highlight from a shell while a workspace file is selected", () => {
		render(
			<ChatWorkspace
				snapshot={idleSnapshot()}
				shellTerminals={shells}
				shellTarget={shellTarget("shell-2")}
				workspaceActiveTabKey="file:README.md"
				workspaceTabs={[
					{
						key: "file:README.md",
						content: <button aria-selected="true" role="tab">README.md</button>,
						onSelect: vi.fn(),
					},
				]}
			/>,
		);

		expect(screen.getByRole("tab", { name: "second shell" })).toHaveAttribute("aria-selected", "false");
		expect(screen.getByRole("tab", { name: "README.md" })).toHaveAttribute("aria-selected", "true");
	});
});

