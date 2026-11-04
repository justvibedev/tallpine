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
import { placeLexicalCaret, typeInLexicalEditor } from "../../test/lexical";
import {
	afterEach,
	beforeEach,
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { ChatWorkspace, promptSpacerHeight, promptTopInset } from "./ChatWorkspace";
import { AssistantMessage, HumanMessage, OriginMessage } from "./ChatTimelineItems";
import { AutomationMessage } from "./AutomationMessage";
import { ChatLinkProvider } from "./ChatMarkdown";
import { chatFixture, chatFixtureEmpty, chatFixtureSettled } from "../../lib/chat-fixture";
import type { ConversationItem, ConversationMessage, ConversationSnapshot } from "../../types/conversation";
import { readChatSessionDraft, writeChatComposerText } from "../../lib/chat-drafts";
import { getChatDraftBoundaries } from "../../lib/chat-draft-boundary";
import { renderChat as render } from "../../test/render-chat";
import {
	humanMessage,
} from "./ChatWorkspaceTestHelpers";


beforeEach(resetChatWorkspaceMocks);

afterEach(async () => {
	await cleanupChatWorkspaceTest();
});

describe("ChatWorkspace prompt suggestions", () => {
	it("inserts a contextual suggestion into the normal draft on Tab without sending it", async () => {
		const onSend = vi.fn();
		const promptSuggestion = { id: "context-1", text: "Explain the fix." };
		const snapshot = { ...chatFixtureSettled, sessionId: "suggestion-workspace" };
		render(<ChatWorkspace snapshot={snapshot} promptSuggestion={promptSuggestion} onSend={onSend} />);
		expect(screen.getByTestId("prompt-suggestion")).toHaveTextContent(promptSuggestion.text);
		const editor = screen.getByRole("combobox", { name: "Message the agent" });
		await userEvent.click(editor);
		fireEvent.keyDown(editor, { key: "Tab", code: "Tab" });
		await waitFor(() => expect(editor).toHaveTextContent(promptSuggestion.text));
		expect(readChatSessionDraft(snapshot.sessionId).composer.text).toBe(promptSuggestion.text);
		expect(onSend).not.toHaveBeenCalled();
	});

	it("withholds suggestions while the normal entry is locked", () => {
		const props = {
			snapshot: { ...chatFixtureSettled, sessionId: "suggestion-workspace-locked" },
			promptSuggestion: { id: "context-1", text: "Explain the fix." },
		};
		const view = render(<ChatWorkspace {...props} newWorkDisabled />);
		expect(screen.queryByTestId("prompt-suggestion")).not.toBeInTheDocument();
		view.rerender(<ChatWorkspace {...props} />);
		expect(screen.getByTestId("prompt-suggestion")).toHaveTextContent(props.promptSuggestion.text);
	});
});

describe("HumanMessage attachments", () => {
	it("loads a remote session's staged image from its proxy, not the local daemon", () => {
		const snapshot: ConversationSnapshot = {
			...chatFixtureEmpty,
			items: [humanMessage("See image\n\nAttached files (read these files in the workspace):\n- .tallpine/attachments/attachment-remote.png")],
			latestSequence: 1,
		};
		render(<ChatWorkspace snapshot={snapshot} assetBaseUrl="http://127.0.0.1:4000/token-a" />);
		expect(screen.getByRole("img", { name: "Image 1" })).toHaveAttribute(
			"src",
			`http://127.0.0.1:4000/token-a/api/v1/sessions/${encodeURIComponent(snapshot.sessionId)}/preview/files/.tallpine/attachments/attachment-remote.png`,
		);
	});

	it("does not read an offline remote session's image from the laptop daemon", () => {
		const snapshot: ConversationSnapshot = {
			...chatFixtureEmpty,
			items: [humanMessage("See image\n\nAttached files (read these files in the workspace):\n- .tallpine/attachments/attachment-remote.png")],
			latestSequence: 1,
		};
		render(<ChatWorkspace snapshot={snapshot} remoteHostId="box-a" />);
		expect(screen.queryByRole("img", { name: "Image 1" })).not.toBeInTheDocument();
		expect(screen.getByText("attachment-remote.png")).toBeInTheDocument();
	});

	it("keeps offline remote preview links classified as remote", () => {
		const assistant = chatFixture.items.find((item): item is ConversationMessage => item.kind === "message" && item.role === "assistant");
		if (!assistant) throw new Error("Fixture needs an assistant message");
		const snapshot: ConversationSnapshot = {
			...chatFixtureEmpty,
			items: [{ ...assistant, id: "offline-preview", sequence: 1, text: "[preview](http://localhost:5173)" }],
			latestSequence: 1,
		};
		render(<ChatWorkspace snapshot={snapshot} remoteHostId="box-a" />);
		expect(screen.getByText("preview")).toBeInTheDocument();
		expect(screen.queryByRole("link", { name: "preview" })).not.toBeInTheDocument();
	});

	it("hides appended worker report context from the human message", async () => {
		const text =
			"Please continue\n\n<ao-worker-reports>\nReports since your previous turn:\n\n[done] [worker](tallpine://sessions/project/worker)\nFinished\n</ao-worker-reports>";
		render(<HumanMessage message={humanMessage(text)} sessionId="tallpine-1" />);

		expect(screen.getByText("Please continue")).toBeInTheDocument();
		expect(screen.queryByText(/Reports since your previous turn/)).not.toBeInTheDocument();
		expect(screen.queryByRole("link", { name: "worker" })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Copy user message" }));
		expect(writeText).toHaveBeenCalledWith("Please continue");
	});

	function renderImageAttachment(header: string, name: string) {
		render(
			<HumanMessage
				message={humanMessage(`check again\n\n${header}\n- .tallpine/attachments/${name}`)}
				sessionId="tallpine session/1"
			/>,
		);

		const image = screen.getByRole("img", { name: "Image 1" });
		expect(image).toHaveAttribute(
			"src",
			`http://127.0.0.1:3101/api/v1/sessions/tallpine%20session%2F1/preview/files/.tallpine/attachments/${name}`,
		);
		expect(screen.getByText("check again")).toBeInTheDocument();
		expect(
			screen.queryByText(/Attached (?:files|images) \(read these files/),
		).not.toBeInTheDocument();
	}

	it("renders staged image references in human messages as images", () => {
		renderImageAttachment(
			"Attached files (read these files in the workspace):",
			"attachment-d9014f798f.png",
		);
	});

	it.each([
		[
			"spawn",
			"Attached files (read these files in the workspace for context):",
			"attachment-1.jpg",
		],
		[
			"legacy chat",
			"Attached images (read these files in the workspace for visual context):",
			"image-a1b2c3d4.webp",
		],
	])("renders a Tallpine-generated %s image reference as an image", (_source, header, name) => {
		renderImageAttachment(header, name);
	});

	it("preserves authored trailing whitespace before generated references", () => {
		const authoredBody = "keep my spacing  \n";
		const { container } = render(
			<HumanMessage
				message={humanMessage(
					`${authoredBody}\n\nAttached files (read these files in the workspace):\n- .tallpine/attachments/attachment-ab12.png`,
				)}
				sessionId="tallpine-1"
			/>,
		);

		expect(container.querySelector(".cursor-chat-human-message > p")?.textContent).toBe(
			authoredBody,
		);
	});

	it("shows non-image attachments as file labels instead of internal prompt text", () => {
		render(
			<HumanMessage
				message={humanMessage(
					"inspect these\n\nAttached files (read these files in the workspace):\n- .tallpine/attachments/attachment-ab12.png\n- .tallpine/attachments/attachment-cd34.pdf",
				)}
				sessionId="tallpine-1"
			/>,
		);

		expect(screen.getByRole("img", { name: "Image 1" })).toBeInTheDocument();
		expect(screen.getByText("attachment-cd34.pdf")).toBeInTheDocument();
		expect(screen.getByRole("list", { name: "Attached files" })).toBeInTheDocument();
		expect(screen.queryByText(/Attached files \(read these files/)).not.toBeInTheDocument();
	});

	it("opens an attached image at full size when clicked", async () => {
		render(
			<HumanMessage
				message={humanMessage(
					"look\n\nAttached files (read these files in the workspace):\n- .tallpine/attachments/attachment-ab12.png",
				)}
				sessionId="tallpine-1"
			/>,
		);

		await userEvent.click(screen.getByRole("button", { name: "Open image: Image 1" }));
		const dialog = await screen.findByRole("dialog");
		expect(within(dialog).getByRole("img", { name: "Image 1" })).toHaveAttribute(
			"src",
			expect.stringContaining("/api/v1/sessions/tallpine-1/preview/files/.tallpine/attachments/attachment-ab12.png"),
		);
	});

	it("shows an image named in the prose as an inline chip that opens it", async () => {
		const path = ".tallpine/attachments/attachment-ab12.png";
		const { container } = render(
			<HumanMessage
				message={humanMessage(
					`before ${path} after\n\nAttached files (read these files in the workspace):\n- ${path}`,
				)}
				sessionId="tallpine-1"
			/>,
		);

		const paragraph = container.querySelector(".cursor-chat-human-message > p");
		expect(paragraph).toHaveTextContent("before Image 1 after");
		expect(paragraph).not.toHaveTextContent(".tallpine/attachments");
		await userEvent.click(within(paragraph as HTMLElement).getByRole("button", { name: "Open image: Image 1" }));
		expect(within(await screen.findByRole("dialog")).getByRole("img", { name: "Image 1" })).toHaveAttribute(
			"src",
			expect.stringContaining(`/api/v1/sessions/tallpine-1/preview/files/${path}`),
		);
	});

	it("leaves ordinary user-authored path lists untouched", () => {
		const text =
			"Document this example:\n\nAttached files (read these files in the workspace):\n- docs/screenshot.png";
		render(<HumanMessage message={humanMessage(text)} sessionId="tallpine-1" />);

		expect(screen.queryByRole("img")).not.toBeInTheDocument();
		expect(document.body.textContent).toContain(text);
	});

	it("linkifies session URLs without parsing the human message as Markdown", () => {
		const text = "Notes:\n- fix *bug*\ntallpine://sessions/proj/sess\n> write test";
		const { container } = render(<HumanMessage message={humanMessage(text)} sessionId="tallpine-1" />);

		const paragraph = container.querySelector(".cursor-chat-human-message > p");
		expect(paragraph).toHaveClass("whitespace-pre-wrap");
		expect(paragraph?.textContent).toBe(text);
		expect(screen.getByRole("link", { name: "tallpine://sessions/proj/sess" })).toBeInTheDocument();
		expect(container.querySelector("ul, blockquote, em")).toBeNull();
	});
});

describe("Chat message timestamps", () => {
	it("exposes user and assistant sent times as 24-hour hover labels", () => {
		const today = new Date().toISOString();
		const user = { ...humanMessage("user message"), createdAt: today };
		const assistant = {
			...user,
			id: "assistant-message",
			role: "assistant",
			origin: "provider",
			text: "assistant message",
		} satisfies ConversationMessage;
		render(
			<>
				<HumanMessage message={user} sessionId="tallpine-1" />
				<AssistantMessage message={assistant} showCopy />
			</>,
		);

		expect(screen.getAllByLabelText(/^Sent \d{2}:\d{2}$/)).toHaveLength(2);
	});

	it("labels yesterday and older messages with calendar dates", () => {
		const now = new Date();
		const relativeDate = (daysAgo: number) => {
			const date = new Date(now);
			date.setDate(date.getDate() - daysAgo);
			return date.toISOString();
		};
		const messages = [
			{
				...humanMessage("yesterday"),
				id: "yesterday",
				createdAt: relativeDate(1),
			},
			{
				...humanMessage("older"),
				id: "older",
				createdAt: relativeDate(3),
			},
		];
		render(
			<>
				{messages.map((message) => (
					<HumanMessage key={message.id} message={message} sessionId="tallpine-1" />
				))}
			</>,
		);

		expect(screen.getByLabelText(/^Sent Yesterday · \d{2}:\d{2}$/)).toBeInTheDocument();
		expect(screen.getByLabelText(/^Sent .+\d{4}$/)).toBeInTheDocument();
	});
});

describe("automation reports", () => {
	it("keeps queued browser feedback visible while the agent is busy", () => {
		const snapshot: ConversationSnapshot = {
			...chatFixture,
			turns: [
				...chatFixture.turns,
				{ id: "queued-feedback", state: "queued", requestedAt: "2026-09-28T12:00:00Z" },
			],
			items: [
				...chatFixture.items,
				{
					kind: "message",
					id: "queued-feedback-message",
					turnId: "queued-feedback",
					sequence: 15,
					revision: 0,
					role: "user",
					origin: "automation",
					text: "<browser_annotations>\nBrowser feedback\nPage: Example\nURL: https://example.com/\nAnnotations: 1\n\nAnnotation 1 (comment):\nTarget: button\nComment: Make this clearer.\n\n</browser_annotations>",
					streaming: false,
					createdAt: "2026-09-28T12:00:00Z",
				},
			],
		};

		render(<ChatWorkspace snapshot={snapshot} />);

		const feedback = screen.getByTestId("your-action");
		expect(feedback).toHaveTextContent("Example");
		expect(feedback).toHaveTextContent("Make this clearer.");
		expect(feedback).toHaveTextContent("Queued · sends when the agent finishes");
	});

	it("renders browser annotation transport as the user's feedback, without its transport details", () => {
		const source = chatFixture.items.find((item) => item.id === "m-4") as ConversationMessage;
		const message: ConversationMessage = {
			...source,
			text: `<browser_annotations>
Browser feedback
Page: Google
URL: https://www.google.com/
Annotations: 1

Annotation 1 (adjustment):
Target: div.badge
Selector: body > div.badge
Dimensions: 120×24
Element text: "New"
Requested visual changes:
- Text color: "rgb(0, 0, 0)" → "#d7193f"
- Background: "transparent" → "#32c873"

Reference screenshots:
- .tallpine/attachments/browser.png

Task: Address the feedback below according to its wording. Visual adjustments are already previewed in Tallpine's shared browser and describe the intended result.
</browser_annotations>`,
		};

		render(<AutomationMessage message={message} fallback={null} />);

		expect(screen.getByText("Google")).toBeInTheDocument();
		expect(screen.getByText("2 visual changes")).toBeInTheDocument();
		expect(screen.getByText("div.badge")).toBeInTheDocument();
		expect(screen.getByText("New")).toBeInTheDocument();
		expect(screen.getByText("1 reference screenshot")).toBeInTheDocument();
		expect(screen.queryByText(/body > div\.badge/)).not.toBeInTheDocument();
		expect(screen.queryByText(/Task: Address/)).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Show full report" })).not.toBeInTheDocument();
	});

	it("collapses a long report until the reader asks to expand it", async () => {
		const user = userEvent.setup();
		const source = chatFixture.items.find((item) => item.id === "m-4") as ConversationMessage;
		const message: ConversationMessage = {
			...source,
			text: `READ-ONLY follow-up report. ${"Adapter evidence and implementation details. ".repeat(20)}END OF REPORT`,
		};
		render(<OriginMessage message={message} />);

		expect(screen.getByRole("button", { name: "Show full report" })).toHaveAttribute(
			"aria-expanded",
			"false",
		);
		expect(screen.queryByText(/END OF REPORT/)).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Show full report" }));
		expect(screen.getByText(/END OF REPORT/)).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Hide report" })).toHaveAttribute(
			"aria-expanded",
			"true",
		);
	});

	it("keeps truncated report previews inert and Markdown-safe", () => {
		const source = chatFixture.items.find((item) => item.id === "m-4") as ConversationMessage;
		const message: ConversationMessage = {
			...source,
			text: [
				"**checkpoint**: [Open worker](tallpine://sessions/proj/sess)",
				"![tracking pixel](https://attacker.example/pixel.png)",
				"[Approve deployment](https://attacker.example/approve)",
				"```python",
				...Array.from({ length: 80 }, (_, index) => `print(${index})`),
				"```",
			].join("\n"),
		};

		const { container } = render(<OriginMessage message={message} />);

		expect(screen.getByText("checkpoint").tagName).toBe("STRONG");
		expect(screen.getByRole("link", { name: "Open worker" })).toHaveAttribute("href", "tallpine://sessions/proj/sess");
		expect(screen.getByText("tracking pixel")).toBeInTheDocument();
		expect(screen.queryByRole("img", { name: "tracking pixel" })).not.toBeInTheDocument();
		expect(container).toHaveTextContent("Approve deployment");
		expect(screen.queryByRole("link", { name: "Approve deployment" })).not.toBeInTheDocument();
		expect(document.querySelector("pre")).toBeNull();
		expect(screen.queryByText(/diagram could not be rendered/i)).not.toBeInTheDocument();
	});

	it("keeps a short automation alert fully visible", () => {
		const message = chatFixture.items.find((item) => item.id === "m-4") as ConversationMessage;
		const { container } = render(<OriginMessage message={{ ...message, text: "Checks failed:\nlint exited 1\ntests exited 2" }} />);
		expect(screen.getByText(/Checks failed:/)).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Show full report" })).not.toBeInTheDocument();
		expect(container.querySelector(".chat-md")).toHaveClass("whitespace-pre-wrap");
	});

	it("renders automation formatting and opens labeled session links in app", async () => {
		const source = chatFixture.items.find((item) => item.id === "m-4") as ConversationMessage;
		const onSessionLinkOpen = vi.fn();
		const text = "Reports since your previous turn:\n\n- **checkpoint**: [Open worker](tallpine://sessions/proj/sess)\n\n> Ready for review";
		const { container } = render(
			<ChatLinkProvider onSessionLinkOpen={onSessionLinkOpen}>
				<OriginMessage message={{ ...source, origin: "automation", text }} />
			</ChatLinkProvider>,
		);

		expect(screen.getByText("checkpoint").tagName).toBe("STRONG");
		expect(screen.getByRole("listitem")).toHaveTextContent("checkpoint: Open worker");
		expect(screen.getByText("Ready for review").closest("blockquote")).not.toBeNull();
		const link = screen.getByRole("link", { name: "Open worker" });
		expect(link).toHaveAttribute("href", "tallpine://sessions/proj/sess");
		expect(link).toHaveAttribute("rel", expect.stringContaining("noreferrer"));
		expect(container.querySelector(".cursor-chat-origin-message")).toHaveClass("border-l-logo-accent/60");

		await userEvent.click(link);
		expect(onSessionLinkOpen).toHaveBeenCalledWith("tallpine://sessions/proj/sess");
	});

	it("keeps short automation reports safe without dropping Markdown structure", () => {
		const source = chatFixture.items.find((item) => item.id === "m-4") as ConversationMessage;
		const { container } = render(
			<OriginMessage
				message={{
					...source,
					origin: "automation",
					text: "**Status**\n\n![tracking pixel](https://attacker.example/pixel.png)\n\n[Approve deployment](https://attacker.example/approve)",
				}}
			/>,
		);

		expect(screen.getByText("Status").tagName).toBe("STRONG");
		expect(screen.getByText("tracking pixel")).toBeInTheDocument();
		expect(screen.queryByRole("img", { name: "tracking pixel" })).not.toBeInTheDocument();
		expect(container).toHaveTextContent("Approve deployment");
		expect(screen.queryByRole("link", { name: "Approve deployment" })).not.toBeInTheDocument();
	});
});

describe("promptSpacerHeight", () => {
	it("leaves room below a short prompt and collapses once the reply fills the viewport", () => {
		expect(
			promptSpacerHeight({
				viewportHeight: 800,
				contentHeightWithoutSpacer: 120,
				anchorOffset: 40,
				topInset: promptTopInset(800),
			}),
		).toBe(560);

		expect(
			promptSpacerHeight({
				viewportHeight: 800,
				contentHeightWithoutSpacer: 1200,
				anchorOffset: 40,
				topInset: promptTopInset(800),
			}),
		).toBe(0);
	});

	it("keeps a prior-chat band above the latest prompt instead of pinning flush to the top", () => {
		expect(promptTopInset(800)).toBe(160);
		expect(promptTopInset(300)).toBe(80);
	});
});

describe("durable queued edits", () => {
	function queuedSnapshot(): ConversationSnapshot {
		return {
			...chatFixtureEmpty,
			sessionId: "queued-draft-session",
			turns: [
				{ id: "running", state: "running", requestedAt: "2026-09-07T00:00:00Z" },
				{ id: "queued", state: "queued", requestedAt: "2026-09-07T00:00:00Z" },
			],
			items: [{ kind: "message", id: "queued-message", turnId: "queued", role: "user", origin: "human", streaming: false, text: "queued text", sequence: 1, revision: 0, createdAt: "2026-09-07T00:00:00Z" }],
		};
	}

	it("restores the queue target across remount and keeps the independent composer after accepted save", async () => {
		const snapshot = queuedSnapshot();
		const save = vi.fn().mockResolvedValue(undefined);
		const send = vi.fn();
		const props = { snapshot, onSend: send, onEditQueuedTurn: save };
		const first = render(<ChatWorkspace {...props} />);
		await typeInLexicalEditor(screen.getByLabelText("Message the agent"), "independent prompt");
		await userEvent.click(screen.getByRole("button", { name: "Edit queued message" }));
		await typeInLexicalEditor(screen.getByLabelText("Message the agent"), " updated");
		expect(readChatSessionDraft(snapshot.sessionId).composer.text).toBe("independent prompt");
		expect(readChatSessionDraft(snapshot.sessionId).queuedEdit?.text).toBe("queued text updated");
		first.unmount();
		const second = render(<ChatWorkspace {...props} />);
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent("queued text updated");
		await userEvent.click(screen.getByRole("button", { name: "Send message" }));
		await waitFor(() => expect(save).toHaveBeenCalledWith("queued", "queued text updated", { clientMessageId: expect.any(String), expectedRevision: 0, retainedContent: [] }));
		await waitFor(() => expect(readChatSessionDraft(snapshot.sessionId).queuedEdit).toBeUndefined());
		expect(send).not.toHaveBeenCalled();
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent("independent prompt");
		second.unmount();
		render(<ChatWorkspace {...props} />);
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent("independent prompt");
	});

	it("finishes an ordinary send before allowing a queued editor to replace it", async () => {
		const snapshot = queuedSnapshot();
		let settle!: (paths: string[]) => void;
		const stage = vi.fn(() => new Promise<string[]>((resolve) => { settle = resolve; }));
		const save = vi.fn();
		const send = vi.fn().mockResolvedValue(undefined);
		render(<ChatWorkspace snapshot={snapshot} onSend={send} onEditQueuedTurn={save} onStageAttachments={stage} />);
		const composer = screen.getByLabelText("Message the agent");
		await typeInLexicalEditor(composer, "ordinary prompt");
		fireEvent.paste(composer, { clipboardData: { files: [new File(["file"], "note.txt", { type: "text/plain" })], items: [] } });
		await waitFor(() => expect(stage).toHaveBeenCalledOnce());
		fireEvent.keyDown(composer, { key: "Enter" });
		expect(screen.getByRole("button", { name: "Edit queued message" })).toBeDisabled();
		await act(async () => settle([".tallpine/attachments/note.txt"]));
		await waitFor(() => expect(send).toHaveBeenCalledOnce());
		expect(send.mock.calls[0]?.[0]).toContain("ordinary prompt");
		await waitFor(() => expect(screen.getByRole("button", { name: "Edit queued message" })).toBeEnabled());
		await userEvent.click(screen.getByRole("button", { name: "Edit queued message" }));
		expect(save).not.toHaveBeenCalled();
		expect(readChatSessionDraft(snapshot.sessionId).queuedEdit?.text).toBe("queued text");
		expect(readChatSessionDraft(snapshot.sessionId).composer.text).toBe("");
	});

	it("keeps queued text visible when durable cancellation fails", async () => {
		const snapshot = queuedSnapshot();
		render(<ChatWorkspace snapshot={snapshot} onSend={vi.fn()} onEditQueuedTurn={vi.fn()} />);
		await userEvent.click(screen.getByRole("button", { name: "Edit queued message" }));
		const composer = screen.getByLabelText("Message the agent");
		await typeInLexicalEditor(composer, " keep this");
		const durableStorage = window.localStorage;
		const storage = vi.spyOn(window, "localStorage", "get").mockReturnValue({
			length: 0,
			clear: durableStorage.clear.bind(durableStorage),
			key: () => null,
			getItem: durableStorage.getItem.bind(durableStorage),
			setItem: () => { throw new DOMException("full", "QuotaExceededError"); },
			removeItem: () => { throw new DOMException("blocked", "SecurityError"); },
		});
		try {
			fireEvent.keyDown(composer, { key: "Escape" });
			expect(composer).toHaveTextContent("queued text keep this");
			expect(readChatSessionDraft(snapshot.sessionId).queuedEdit?.text).toBe("queued text keep this");
			expect(screen.getByRole("alert")).toHaveTextContent("Queued edit could not be saved");
		} finally { storage.mockRestore(); }
	});

	it("preserves an unavailable queue target and never converts its recovery into a send", async () => {
		const snapshot = queuedSnapshot();
		const save = vi.fn().mockRejectedValue(new Error("response lost"));
		const send = vi.fn();
		const first = render(<ChatWorkspace snapshot={snapshot} onSend={send} onEditQueuedTurn={save} />);
		await userEvent.click(screen.getByRole("button", { name: "Edit queued message" }));
		await typeInLexicalEditor(screen.getByLabelText("Message the agent"), " uncertain");
		await userEvent.click(screen.getByRole("button", { name: "Send message" }));
		await waitFor(() => expect(save).toHaveBeenCalledOnce());
		first.unmount();
		render(<ChatWorkspace snapshot={{ ...snapshot, turns: [] }} onSend={send} onEditQueuedTurn={save} />);
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent("queued text uncertain");
		expect(screen.getByLabelText("Message the agent")).toHaveAttribute("contenteditable", "false");
		await userEvent.click(screen.getByRole("button", { name: "Retry edit safely" }));
		await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
		expect(save.mock.calls[1]).toEqual(save.mock.calls[0]);
		expect(send).not.toHaveBeenCalled();
		const pending = readChatSessionDraft(snapshot.sessionId).queuedEdit;
		expect(pending?.clientMessageId).toBeTruthy();
		fireEvent.keyDown(screen.getByLabelText("Message the agent"), { key: "Escape" });
		expect(readChatSessionDraft(snapshot.sessionId).queuedEdit).toEqual(pending);
	});

	it("blocks destructive boundaries while a queued edit cannot be persisted, and lifts them when it is dismissed", async () => {
		const user = userEvent.setup();
		const snapshot = queuedSnapshot();
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
		const view = render(<ChatWorkspace snapshot={snapshot} onEditQueuedTurn={vi.fn()} />);
		try {
			await user.click(screen.getByRole("button", { name: "Edit queued message" }));
			await waitFor(() =>
				expect(getChatDraftBoundaries(snapshot.sessionId)).toContain("persistence-failed"),
			);
			view.unmount();
			expect(getChatDraftBoundaries(snapshot.sessionId)).not.toContain("persistence-failed");
		} finally {
			localStorage.mockRestore();
		}
	});
});

describe("sent message recall", () => {
	/** Fires one arrow key; true means the composer claimed it rather than moving the caret. */
	const press = (key: "ArrowUp" | "ArrowDown") =>
		!fireEvent.keyDown(screen.getByRole("combobox", { name: "Message the agent" }), { key });
	const composerText = () => screen.getByRole("combobox", { name: "Message the agent" }).textContent;
	const human = (id: string, turnId: string, sequence: number, text: string, extra: Partial<ConversationMessage> = {}): ConversationMessage => ({
		kind: "message", id, turnId, sequence, revision: 0, role: "user", origin: "human", text, streaming: false,
		createdAt: "2026-09-09T00:00:00Z", ...extra,
	});
	const steer = (id: string, turnId: string, sequence: number, text: string, detail: Record<string, string> = { origin: "human" }): ConversationItem => ({
		kind: "activity", id, turnId, sequence, revision: 0, activityKind: "system", status: "completed", summary: text,
		detail: { event: "steer", text, ...detail }, createdAt: "2026-09-09T00:00:00Z",
	});
	function historySnapshot(sessionId: string, items: ConversationItem[], activeBranchId = "branch-1"): ConversationSnapshot {
		return {
			...chatFixtureEmpty,
			sessionId,
			activeBranchId,
			latestSequence: items.length,
			turns: [...new Set(items.map((item) => item.turnId as string))].map((id) => ({
				id, state: "completed" as const, requestedAt: "2026-09-09T00:00:00Z",
			})),
			items,
		};
	}

	it("recalls only the human prompts this conversation shows as sent", async () => {
		const snapshot = historySnapshot("recall-sources", [
			human("first", "t1", 1, "First ask", { clientMessageId: "echo-durable" }),
			{ ...human("reply", "t1", 2, "An answer"), role: "assistant" },
			{ ...human("relay", "t2", 3, "A worker report"), origin: "automation" },
			human("attached", "t3", 4, "Look at this\n\nAttached files (read these files in the workspace):\n- .tallpine/attachments/attachment-notes.txt"),
			human("undone", "t4", 5, "Undone ask"),
			human("second", "t5", 6, "Second ask"),
			human("repeat", "t6", 7, "Second ask"),
			human("queued", "t7", 8, "Queued ask"),
		]);
		snapshot.turns = snapshot.turns.map((turn) =>
			turn.id === "t4" ? { ...turn, rolledBack: true } : turn.id === "t7" ? { ...turn, state: "queued" as const } : turn);
		const localEchos = [
			{ clientMessageId: "echo-durable", text: "First ask", createdAt: "2026-09-09T00:00:00Z" },
			{ clientMessageId: "echo-queued", text: "Queued echo", createdAt: "2026-09-09T00:00:01Z", queued: true },
			{ clientMessageId: "echo-new", text: "Just sent", createdAt: "2026-09-09T00:00:02Z" },
		];
		render(<ChatWorkspace snapshot={snapshot} localEchos={localEchos} />);
		await typeInLexicalEditor(screen.getByRole("combobox", { name: "Message the agent" }), "");

		const recalled: (string | null)[] = [];
		while (press("ArrowUp")) recalled.push(composerText());
		expect(recalled).toEqual(["Just sent", "Second ask", "Look at this", "First ask"]);
	});

	it("recalls the user's steers in sequence order and leaves automation steers out", async () => {
		const snapshot = historySnapshot("recall-steers", [
			human("first", "t1", 1, "Earlier ask"),
			{ ...human("reply", "t1", 3, "An answer"), role: "assistant" },
			human("second", "t2", 4, "Second ask"),
			human("undone", "t3", 8, "Undone ask"),
			// Activities come after the messages here; recall follows their sequence.
			steer("legacy", "t1", 2, "Legacy guidance", {}),
			steer("guidance", "t2", 5, "Latest human guidance\n\nAttached files (read these files in the workspace):\n- .tallpine/attachments/attachment-steer.txt"),
			steer("automation", "t2", 6, "Automation guidance", { origin: "automation" }),
			steer("relayed", "t2", 7, "Relayed guidance", { senderSessionId: "worker-1", senderProjectId: "project-1" }),
			steer("undone-steer", "t3", 9, "Undone guidance"),
		]);
		snapshot.turns = snapshot.turns.map((turn) => turn.id === "t3" ? { ...turn, rolledBack: true } : turn);
		render(<ChatWorkspace snapshot={snapshot} />);
		expect(screen.getByText("Automation guidance")).toBeInTheDocument();
		await typeInLexicalEditor(screen.getByRole("combobox", { name: "Message the agent" }), "");

		const recalled: (string | null)[] = [];
		while (press("ArrowUp")) recalled.push(composerText());
		expect(recalled).toEqual(["Latest human guidance", "Second ask", "Legacy guidance", "Earlier ask"]);
	});

	it("keeps a walk steady while the same branch gains messages", async () => {
		const sessionId = "recall-same-branch";
		const before = historySnapshot(sessionId, [human("a", "t1", 1, "First ask"), human("b", "t2", 2, "Second ask")]);
		const view = render(<ChatWorkspace snapshot={before} />);
		await typeInLexicalEditor(screen.getByRole("combobox", { name: "Message the agent" }), "");
		press("ArrowUp");
		expect(composerText()).toBe("Second ask");

		// A prompt from elsewhere and a streaming answer land under the open walk.
		const after = historySnapshot(sessionId, [
			human("a", "t1", 1, "First ask"),
			human("b", "t2", 2, "Second ask"),
			human("c", "t3", 3, "Third ask"),
			{ ...human("reply", "t3", 4, "Streaming answer"), role: "assistant", streaming: true },
		]);
		view.rerender(<ChatWorkspace snapshot={after} />);
		press("ArrowUp");
		expect(composerText()).toBe("First ask");
		press("ArrowDown");
		expect(composerText()).toBe("Second ask");
		press("ArrowDown");
		expect(composerText()).toBe("");

		press("ArrowUp");
		expect(composerText()).toBe("Third ask");
	});

	it.each([
		{ change: "another branch is activated", conversationId: chatFixtureEmpty.conversationId, activeBranchId: "branch-2" },
		{ change: "the conversation is replaced", conversationId: "conv-replacement", activeBranchId: "branch-1" },
	])("drops a walk and restores the draft when $change", async ({ conversationId, activeBranchId }) => {
		const sessionId = `recall-identity-${activeBranchId}-${conversationId}`;
		writeChatComposerText(sessionId, "my draft");
		const view = render(<ChatWorkspace snapshot={historySnapshot(sessionId, [human("a", "t1", 1, "Shared start"), human("b", "t2", 2, "Old branch ask")])} />);
		const composer = () => screen.getByRole("combobox", { name: "Message the agent" });
		await waitFor(() => expect(composer()).toHaveTextContent("my draft"));
		await placeLexicalCaret(composer(), 0);
		press("ArrowUp");
		expect(composerText()).toBe("Old branch ask");

		const replaced = historySnapshot(sessionId, [human("a", "t1", 1, "Shared start"), human("c", "t3", 3, "New branch ask")], activeBranchId);
		view.rerender(<ChatWorkspace snapshot={{ ...replaced, conversationId }} />);
		expect(composerText()).toBe("my draft");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("my draft");

		const recalled: (string | null)[] = [];
		while (press("ArrowUp")) recalled.push(composerText());
		expect(recalled).toEqual(["New branch ask", "Shared start"]);
	});

	it("restores the user's own draft, after a remount too, and sends a recalled prompt", async () => {
		const sessionId = "recall-draft";
		writeChatComposerText(sessionId, "my own draft");
		const send = vi.fn().mockResolvedValue(undefined);
		const snapshot = historySnapshot(sessionId, [human("a", "t1", 1, "Earlier ask")]);
		const first = render(<ChatWorkspace snapshot={snapshot} onSend={send} />);
		const composer = () => screen.getByRole("combobox", { name: "Message the agent" });
		await waitFor(() => expect(composer()).toHaveTextContent("my own draft"));

		await placeLexicalCaret(composer(), 0);
		press("ArrowUp");
		expect(composerText()).toBe("Earlier ask");
		expect(readChatSessionDraft(sessionId).composer.text).toBe("my own draft");
		press("ArrowDown");
		expect(composerText()).toBe("my own draft");

		await placeLexicalCaret(composer(), 0);
		press("ArrowUp");
		first.unmount();
		render(<ChatWorkspace snapshot={snapshot} onSend={send} />);
		await waitFor(() => expect(composer()).toHaveTextContent("my own draft"));

		await placeLexicalCaret(composer(), 0);
		press("ArrowUp");
		expect(send).not.toHaveBeenCalled();
		fireEvent.keyDown(composer(), { key: "Enter" });
		await waitFor(() => expect(send).toHaveBeenCalledOnce());
		expect(send.mock.calls[0]?.[0]).toBe("Earlier ask");
		await waitFor(() => expect(readChatSessionDraft(sessionId).composer.delivery).toBeUndefined());
		expect(readChatSessionDraft(sessionId).composer.text).toBe("");
	});

	it("walks only the active session's messages", async () => {
		const view = render(<ChatWorkspace snapshot={historySnapshot("recall-alpha", [human("a", "t1", 1, "Alpha ask")])} />);
		await typeInLexicalEditor(screen.getByRole("combobox", { name: "Message the agent" }), "");
		press("ArrowUp");
		expect(composerText()).toBe("Alpha ask");

		view.rerender(<ChatWorkspace snapshot={historySnapshot("recall-beta", [human("b", "t1", 1, "Beta ask")])} />);
		await typeInLexicalEditor(screen.getByRole("combobox", { name: "Message the agent" }), "");
		expect(composerText()).toBe("");
		expect(press("ArrowDown")).toBe(false);
		expect(press("ArrowUp")).toBe(true);
		expect(composerText()).toBe("Beta ask");
		expect(press("ArrowUp")).toBe(false);
		expect(readChatSessionDraft("recall-alpha").composer.text).toBe("");
	});
});

