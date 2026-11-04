// The setup module registers vi.mock calls, so it must be imported before ChatWorkspace.
import {
	renameSessionMock,
	cleanupChatWorkspaceTest,
	resetChatWorkspaceMocks,
} from "./ChatWorkspace.test.setup";
import {
	fireEvent,
	screen,
	waitFor,
	within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
	afterEach,
	beforeEach,
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import {
	chatFixture,
	chatFixtureEmpty,
	chatFixtureLongHistory,
	chatFixtureSettled,
} from "../../lib/chat-fixture";
import type { ConversationSnapshot } from "../../types/conversation";
import { apiClient } from "../../lib/api-client";
import { useUiStore } from "../../stores/ui-store";
import { renderChat as render } from "../../test/render-chat";
import {
	idleSnapshot,
	stubGeometry,
	chatSession,
} from "./ChatWorkspaceTestHelpers";


beforeEach(resetChatWorkspaceMocks);

afterEach(async () => {
	vi.restoreAllMocks();
	await cleanupChatWorkspaceTest();
});

describe("ChatWorkspace timeline", () => {
	it("brings the reader to their prompt when they send after scrolling away", () => {
		// Pin the page clock to a freshly opened renderer (under the 1.5s smooth-scroll
		// window), so the outcome cannot depend on how long the worker has been up.
		vi.spyOn(performance, "now").mockReturnValue(200);
		const snapshot = chatFixtureLongHistory(8);
		const view = render(<ChatWorkspace snapshot={snapshot} />);
		const log = screen.getByRole("log");
		stubGeometry(log, { scrollHeight: 4000, clientHeight: 800, scrollTop: 1000 });
		// Reading older turns: the upward wheel holds the follow, and the scroll it causes releases it.
		fireEvent.wheel(log, { deltaY: -120 });
		fireEvent.scroll(log);
		expect(screen.getByRole("button", { name: "Jump to latest" })).toBeInTheDocument();

		const localEchos = [{ clientMessageId: "send-after-reading", text: "New question", createdAt: "2026-09-09T00:00:00Z" }];
		view.rerender(<ChatWorkspace snapshot={snapshot} localEchos={localEchos} />);
		expect(screen.queryByRole("button", { name: "Jump to latest" })).not.toBeInTheDocument();
		expect(log.scrollTop).toBe(4000);
	});

	it("releases on a small nudge up from the end and re-latches when the reader scrolls back down", () => {
		render(<ChatWorkspace snapshot={chatFixtureLongHistory(8)} />);
		const log = screen.getByRole("log");
		stubGeometry(log, { scrollHeight: 4000, clientHeight: 800, scrollTop: 3200 });
		fireEvent.scroll(log);
		expect(screen.queryByRole("button", { name: "Jump to latest" })).not.toBeInTheDocument();

		// 30px up is inside the re-latch zone, but an upward move is the reader leaving.
		log.scrollTop = 3170;
		fireEvent.scroll(log);
		expect(screen.getByRole("button", { name: "Jump to latest" })).toBeInTheDocument();

		log.scrollTop = 3190;
		fireEvent.scroll(log);
		expect(screen.queryByRole("button", { name: "Jump to latest" })).not.toBeInTheDocument();
	});

	it("keeps following when a wheel is consumed by a nested scroller or the reader clicks the margin", () => {
		render(<ChatWorkspace snapshot={chatFixtureLongHistory(8)} />);
		const log = screen.getByRole("log");
		stubGeometry(log, { scrollHeight: 4000, clientHeight: 800, scrollTop: 3200 });
		fireEvent.scroll(log);
		// A code block scrolls itself; the log never moves, so no scroll event follows.
		fireEvent.wheel(log.firstElementChild ?? log, { deltaY: -120 });
		fireEvent.pointerDown(log);
		expect(screen.queryByRole("button", { name: "Jump to latest" })).not.toBeInTheDocument();
	});

	it("shows a local human echo until the matching durable turn arrives", () => {
		const snapshot = idleSnapshot(chatFixtureEmpty);
		const localEchos = [
			{
				clientMessageId: "local-send",
				text: "Visible before the server snapshot",
				createdAt: "2026-09-09T00:00:00Z",
				turnId: "turn-local-send",
			},
		];
		const view = render(<ChatWorkspace snapshot={snapshot} localEchos={localEchos} />);
		expect(screen.getByText("Visible before the server snapshot")).toBeInTheDocument();

		const durable = structuredClone(snapshot);
		durable.turns.push({ id: "turn-local-send", state: "running", requestedAt: "2026-09-09T00:00:00Z" });
		durable.items.push({
			kind: "message",
			id: "durable-local-send",
			turnId: "turn-local-send",
			sequence: 1,
			revision: 0,
			role: "user",
			origin: "human",
			text: "Visible before the server snapshot",
			streaming: false,
			createdAt: "2026-09-09T00:00:00Z",
		});
		view.rerender(<ChatWorkspace snapshot={durable} localEchos={localEchos} />);
		expect(screen.getAllByText("Visible before the server snapshot")).toHaveLength(1);
	});

	it("hides an unacknowledged local echo when its durable message arrives first", () => {
		const snapshot = idleSnapshot(chatFixtureEmpty);
		const localEchos = [
			{
				clientMessageId: "local-send",
				text: "Already durable",
				createdAt: "2026-09-09T00:00:00Z",
			},
		];
		const durable = structuredClone(snapshot);
		durable.items.push({
			kind: "message",
			id: "durable-local-send",
			turnId: "turn-local-send",
			sequence: 1,
			revision: 0,
			role: "user",
			origin: "human",
			text: "Already durable",
			streaming: false,
			createdAt: "2026-09-09T00:00:01Z",
		});

		render(<ChatWorkspace snapshot={durable} localEchos={localEchos} />);

		expect(screen.getAllByText("Already durable")).toHaveLength(1);
	});

	it("keeps the same message node when the durable row replaces its echo", () => {
		const snapshot = idleSnapshot(chatFixtureEmpty);
		const localEchos = [
			{
				clientMessageId: "keyed-send",
				text: "Swapped without a remount",
				createdAt: "2026-09-09T00:00:05Z",
			},
		];
		const view = render(<ChatWorkspace snapshot={snapshot} localEchos={localEchos} />);
		const echoNode = screen.getByText("Swapped without a remount");

		// The daemon clock is behind the renderer's, and no turn id has reached the echo yet.
		const durable = structuredClone(snapshot);
		durable.turns.push({ id: "turn-keyed-send", state: "running", requestedAt: "2026-09-09T00:00:01Z" });
		durable.items.push({
			kind: "message",
			id: "durable-keyed-send",
			turnId: "turn-keyed-send",
			sequence: 1,
			revision: 0,
			role: "user",
			origin: "human",
			text: "Swapped without a remount",
			clientMessageId: "keyed-send",
			streaming: false,
			createdAt: "2026-09-09T00:00:01Z",
		});
		view.rerender(<ChatWorkspace snapshot={durable} localEchos={localEchos} />);

		const durableNode = screen.getAllByText("Swapped without a remount");
		expect(durableNode).toHaveLength(1);
		expect(durableNode[0]).toBe(echoNode);
	});

	it("shows a send made behind a running turn in the queue dock, not as a chat bubble", () => {
		const snapshot = idleSnapshot(chatFixtureEmpty);
		snapshot.turns.push({ id: "turn-running", state: "running", requestedAt: "2026-09-09T00:00:00Z" });
		const localEchos = [
			{
				clientMessageId: "queued-send",
				text: "Do this next",
				createdAt: "2026-09-09T00:00:05Z",
				queued: true,
			},
		];

		const view = render(<ChatWorkspace snapshot={snapshot} localEchos={localEchos} />);

		const dock = screen.getByTestId("queued-message-dock");
		expect(within(dock).getByText("Do this next")).toBeInTheDocument();
		expect(within(screen.getByRole("log")).queryByText("Do this next")).not.toBeInTheDocument();
		// Nothing to steer, edit or cancel until the daemon has given it a turn.
		expect(within(dock).queryByLabelText("Delete queued message")).not.toBeInTheDocument();
		expect(within(dock).queryByLabelText("Edit queued message")).not.toBeInTheDocument();

		const durable = structuredClone(snapshot);
		durable.turns.push({ id: "turn-queued", state: "queued", requestedAt: "2026-09-09T00:00:05Z" });
		durable.items.push({
			kind: "message",
			id: "durable-queued",
			turnId: "turn-queued",
			sequence: 1,
			revision: 0,
			role: "user",
			origin: "human",
			text: "Do this next",
			clientMessageId: "queued-send",
			streaming: false,
			createdAt: "2026-09-09T00:00:05Z",
		});
		view.rerender(<ChatWorkspace snapshot={durable} localEchos={localEchos} />);

		// The daemon's row replaces the pending one: still one row, now with its actions.
		expect(screen.getAllByText("Do this next")).toHaveLength(1);
		expect(within(screen.getByTestId("queued-message-dock")).getByLabelText("Delete queued message")).toBeInTheDocument();
	});

	it("keeps a hibernated send looking normal while its queued turn wakes", () => {
		const snapshot = { ...idleSnapshot(chatFixtureEmpty), controller: { state: "hibernated" as const } };
		const localEchos = [
			{
				clientMessageId: "cold-send",
				text: "Send while waking",
				createdAt: "2026-09-09T00:00:00Z",
				turnId: "turn-cold-send",
				backgroundWake: true,
			},
		];
		const durable = structuredClone(snapshot);
		durable.turns.push({ id: "turn-cold-send", state: "queued", requestedAt: "2026-09-09T00:00:00Z" });
		durable.items.push({
			kind: "message",
			id: "durable-cold-send",
			turnId: "turn-cold-send",
			sequence: 1,
			revision: 0,
			role: "user",
			origin: "human",
			text: "Send while waking",
			streaming: false,
			createdAt: "2026-09-09T00:00:00Z",
		});

		render(<ChatWorkspace snapshot={durable} localEchos={localEchos} />);

		expect(screen.getAllByText("Send while waking")).toHaveLength(1);
		expect(screen.queryByText("Sending")).not.toBeInTheDocument();
	});

	it.each([
		{ surface: "worker", draftOwner: undefined },
		{ surface: "reviewer", draftOwner: { sessionId: "review:review-1", incarnation: "review-1" } },
	])("resolves a relative image in $surface prose against the worker workspace", ({ draftOwner }) => {
		const snapshot = idleSnapshot(chatFixtureEmpty);
		snapshot.items.push({
			kind: "message",
			id: "assistant-image",
			turnId: "turn-image",
			sequence: 1,
			revision: 0,
			role: "assistant",
			origin: "provider",
			text: "![screenshot](docs/screenshot.png)",
			streaming: false,
			createdAt: "2026-09-09T00:00:00Z",
		});

		render(<ChatWorkspace snapshot={snapshot} draftOwner={draftOwner} />);

		const src = screen.getByRole("img", { name: "screenshot" }).getAttribute("src") ?? "";
		const url = new URL(src, "http://127.0.0.1");
		expect(url.pathname).toBe(`/api/v1/sessions/${encodeURIComponent(snapshot.sessionId)}/workspace/file/blob`);
		expect(url.searchParams.get("path")).toBe("docs/screenshot.png");
	});

	it("makes composer and history controls inert while a durable transition owns input", () => {
		render(<ChatWorkspace snapshot={idleSnapshot()} agentInputDisabled />);

		expect(screen.getByTestId("chat-conversation-panel")).toHaveAttribute("inert");
	});

	it("fences new work during a drain while keeping the current turn's approval interactive", async () => {
		const user = userEvent.setup();
		const onDecide = vi.fn();
		const view = render(<ChatWorkspace snapshot={idleSnapshot()} newWorkDisabled />);
		expect(screen.queryByText("Switching to terminal UI…")).not.toBeInTheDocument();
		expect(screen.queryByText("The controller is not connected")).not.toBeInTheDocument();

		expect(screen.getByTestId("chat-conversation-panel")).not.toHaveAttribute("inert");
		expect(screen.getByLabelText("Message the agent")).toHaveAttribute("aria-disabled", "true");

		const snapshot = structuredClone(chatFixture);
		snapshot.items.push({
			kind: "activity",
			id: "drain-approval",
			sequence: 99,
			revision: 1,
			turnId: "turn-2",
			activityKind: "approval",
			status: "pending",
			summary: "Run command",
			requestId: "drain-approval",
			decisions: [{ id: "allow_once", label: "Allow Once", kind: "allow_once" }],
			detail: { command: "npm test" },
			createdAt: "2026-08-08T00:00:00Z",
		});
		view.rerender(
			<ChatWorkspace snapshot={snapshot} newWorkDisabled onDecide={onDecide} />,
		);

		const approval = screen.getByRole("group", { name: "Approval request drain-approval" });
		await user.click(within(approval).getByRole("button", { name: /Allow once/ }));
		expect(onDecide).toHaveBeenCalledWith("drain-approval", "allow_once");
	});

	it("shows the skills offer as a compact card in the chat stream, not a banner", async () => {
		const get = vi.spyOn(apiClient, "GET").mockResolvedValue({ data: { projectId: "proj-1", workspace: "/repo", agentsMd: true, claudeMd: false, harnesses: ["claude-code"], instructions: [], skills: [], collisions: [], offersEnabled: true, pendingCommit: [],
			offers: [{ id: "claude-md", harness: "claude-code", items: [{ path: "CLAUDE.md", kind: "file", content: "@AGENTS.md\n" }] }] } } as never);
		try {
			render(<ChatWorkspace snapshot={chatFixture} session={{ ...chatSession, workspaceId: "proj-1" }} sessionRole="worker" />);
			const card = await screen.findByTestId("skills-offer-card");
			expect(card).toHaveTextContent("Claude Code can't see AGENTS.md in this project.");
			expect(screen.getByLabelText("Chat")).toContainElement(card);
			expect(get).toHaveBeenCalledWith("/api/v1/projects/{id}/skills", { params: { path: { id: "proj-1" } } });
		} finally {
			get.mockRestore();
		}
	});

	it("labels worker and orchestrator primary tabs with accessible provider context", () => {
		const view = render(<ChatWorkspace snapshot={chatFixture} session={chatSession} sessionRole="worker" />);

		expect(screen.getByLabelText("Chat")).toHaveAttribute("data-session-role", "worker");
		expect(screen.getByLabelText("Chat")).toHaveClass("min-w-0", "w-full", "overflow-hidden");
		expect(screen.getByTestId("session-workspace-topbar")).toBeInTheDocument();
		expect(screen.getByTestId("session-terminal-region")).toHaveStyle({ width: "100%" });
		const workerTab = screen.getByRole("tab", { name: "Reviewer chat · Codex · Working" });
		expect(workerTab).toHaveTextContent(chatSession.title);
		expect(workerTab).not.toHaveTextContent("Codex");
		expect(workerTab.querySelector('img[aria-hidden="true"], [style*="mask"][aria-hidden="true"]')).toBeInTheDocument();

		view.rerender(
			<ChatWorkspace
				snapshot={chatFixture}
				session={{ ...chatSession, id: "tallpine-demo-orchestrator", kind: "orchestrator" }}
				sessionRole="orchestrator"
			/>,
		);

		expect(screen.getByLabelText("Chat")).toHaveAttribute("data-session-role", "orchestrator");
		expect(screen.getByTestId("session-workspace-topbar")).toBeInTheDocument();
		const actionRegion = screen.getByTestId("session-action-region");
		expect(actionRegion).toHaveClass("pl-2", "pr-3");
		expect(actionRegion).not.toHaveClass("px-3");
		expect(screen.getByRole("tab", { name: "Orchestrator · Codex · Working" })).toBeInTheDocument();

		view.rerender(
			<ChatWorkspace
				snapshot={chatFixture}
				session={{ ...chatSession, id: "legacy-orchestrator", kind: undefined }}
			/>,
		);
		expect(screen.getByRole("tab", { name: "Orchestrator · Codex · Working" })).toBeInTheDocument();
	});

	it("shows live provider context usage beside the composer settings", () => {
		const reported = {
			...idleSnapshot(chatFixture),
			usage: {
				contextUsed: 18_055,
				contextWindow: 258_400,
				inputTokens: 18_050,
				outputTokens: 5,
				cachedTokens: 0,
				totalTokens: 18_055,
			},
		};
		const view = render(<ChatWorkspace snapshot={reported} />);
		const composer = screen.getByLabelText("Message the agent").closest("form") as HTMLElement;
		const gauge = within(composer).getByRole("progressbar", { name: "Context window used" });
		expect(gauge).toHaveAttribute("aria-valuetext", "18,055 / 258,400 tokens (7%)");
		gauge.focus();
		fireEvent.click(gauge.querySelector("svg") as SVGSVGElement);
		expect(gauge).toHaveFocus();

		view.rerender(<ChatWorkspace snapshot={{ ...reported, usage: { ...reported.usage, contextUsed: 129_200 } }} />);
		expect(within(composer).getByRole("progressbar", { name: "Context window used" })).toHaveAttribute("aria-valuetext", "129,200 / 258,400 tokens (50%)");
	});

	it("refreshes the owning workspace after renaming the primary chat tab", async () => {
		const user = userEvent.setup();
		const onSessionRenamed = vi.fn().mockResolvedValue(undefined);
		// The refresh fires only after the rename resolves; settle on a later task so the order is always exercised.
		renameSessionMock.mockImplementation(() => new Promise((resolve) => setTimeout(resolve, 20)));
		render(
			<ChatWorkspace
				snapshot={chatFixture}
				session={chatSession}
				sessionRole="worker"
				onSessionRenamed={onSessionRenamed}
			/>,
		);

		await user.dblClick(screen.getByRole("tab", { name: "Reviewer chat · Codex · Working" }));
		const input = screen.getByRole("textbox", { name: "Rename Reviewer chat" });
		await user.clear(input);
		await user.type(input, "Focused review{Enter}");

		await waitFor(() => expect(renameSessionMock).toHaveBeenCalledWith(chatSession.id, "Focused review"));
		await waitFor(() => expect(onSessionRenamed).toHaveBeenCalledOnce());
	});

	it("does not offer rename on the project orchestrator's chat tab", async () => {
		const user = userEvent.setup();
		const onSessionRenamed = vi.fn().mockResolvedValue(undefined);
		render(
			<ChatWorkspace
				snapshot={chatFixture}
				session={{ ...chatSession, id: "tallpine-demo-orchestrator", kind: "orchestrator" }}
				sessionRole="orchestrator"
				onSessionRenamed={onSessionRenamed}
			/>,
		);

		const tab = screen.getByRole("tab", { name: "Orchestrator · Codex · Working" });
		await user.dblClick(tab);
		fireEvent.contextMenu(tab);

		expect(screen.queryByRole("textbox", { name: /^Rename/ })).not.toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: /^Rename/ })).not.toBeInTheDocument();
		expect(renameSessionMock).not.toHaveBeenCalled();
		expect(onSessionRenamed).not.toHaveBeenCalled();
	});

	it("keeps titlebar clearance attached throughout sidebar expansion and collapse", () => {
		useUiStore.setState({ isSidebarOpen: false });
		const { rerender } = render(<ChatWorkspace snapshot={chatFixture} />);

		expect(screen.getByTestId("session-terminal-region")).toHaveClass(
			"session-topbar-titlebar-clearance-mac",
		);

		useUiStore.setState({ isSidebarOpen: true });
		rerender(<ChatWorkspace snapshot={chatFixture} />);

		expect(screen.getByTestId("session-terminal-region")).toHaveClass(
			"session-topbar-titlebar-clearance-mac",
		);
	});

	it("keeps session tab actions on the primary chat tab, like the terminal session", () => {
		render(
			<ChatWorkspace
				snapshot={idleSnapshot()}
				session={chatSession}
				sessionTabAction={<button type="button">Session tab action</button>}
				headerActions={<button type="button">Workspace action</button>}
			/>,
		);

		const terminalRegion = screen.getByTestId("session-terminal-region");
		expect(terminalRegion).toContainElement(screen.getByRole("tab", { name: /^Reviewer chat/ }));
		expect(terminalRegion).toContainElement(screen.getByRole("button", { name: "Session tab action" }));
		expect(screen.getByTestId("session-tab-action")).toContainElement(
			screen.getByRole("button", { name: "Session tab action" }),
		);
		const actionRegion = screen.getByTestId("session-action-region");
		expect(actionRegion).toContainElement(screen.getByRole("button", { name: "Workspace action" }));
		expect(actionRegion).not.toContainElement(screen.getByRole("button", { name: "Session tab action" }));
	});

	it("leaves new-terminal and display controls out of the chat strip, like the terminal session", () => {
		render(<ChatWorkspace snapshot={chatFixture} onOpenShell={vi.fn()} />);

		const terminalRegion = screen.getByTestId("session-terminal-region");
		expect(terminalRegion).toContainElement(screen.getByRole("tablist", { name: "Chat tabs" }));
		expect(terminalRegion).not.toContainElement(
			screen.queryByRole("button", { name: "New terminal" }),
		);
		expect(
			screen.queryByRole("toolbar", { name: "Chat display controls" }),
		).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Decrease font size" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Fullscreen" })).not.toBeInTheDocument();
	});

	it("starts chat text at 14px without a topbar font control", () => {
		render(<ChatWorkspace snapshot={chatFixture} />);
		const chat = screen.getByLabelText("Chat");

		expect(chat.style.getPropertyValue("--chat-font-size")).toBe("14px");
	});

	it("keeps the composer aligned to the readable conversation width", () => {
		render(<ChatWorkspace snapshot={idleSnapshot(chatFixtureSettled)} />);
		const composer = screen.getByLabelText("Message the agent").closest("form");
		expect(composer?.parentElement).toHaveClass("mx-auto", "w-full", "max-w-3xl");
	});

	it("keeps the live action row in the response while the composer owns the stop action", async () => {
		const user = userEvent.setup();
		const onInterrupt = vi.fn();
		const snapshot = structuredClone(chatFixture);
		snapshot.items = snapshot.items.filter(
			(item) =>
				!(
					item.kind === "activity" &&
					item.activityKind === "approval" &&
					item.status === "pending"
				),
		);

		render(<ChatWorkspace snapshot={snapshot} onInterrupt={onInterrupt} />);

		expect(screen.queryByTestId("live-turn-status")).not.toBeInTheDocument();
		// One Working row for the turn, owned by the turn group, never one per message.
		expect(screen.getAllByTestId("live-working-label")).toHaveLength(1);
		expect(screen.getByTestId("live-working-label")).toHaveTextContent(/^Working for /);
		expect(screen.getAllByTestId("response-spinner")).toHaveLength(1);

		const stop = screen.getByRole("button", { name: "Stop turn" });
		expect(screen.getByLabelText("Message the agent").closest("form")).toContainElement(stop);
		await user.click(stop);
		expect(onInterrupt).toHaveBeenCalledOnce();
	});

	it("shows Working when a running turn has not produced a timeline group yet", () => {
		const requestedAt = new Date(Date.now() - 2_000).toISOString();
		const snapshot: ConversationSnapshot = {
			...chatFixtureEmpty,
			controller: { state: "busy" },
			turns: [{ id: "turn-starting", state: "running", requestedAt }],
		};

		render(<ChatWorkspace snapshot={snapshot} />);

		expect(screen.getByTestId("live-working-label")).toHaveTextContent(/^Working for /);
		expect(screen.getByTestId("response-spinner")).toBeInTheDocument();
	});

	it("keeps single-item settled work inside the Worked accordion", async () => {
		const user = userEvent.setup();
		const snapshot: ConversationSnapshot = {
			...chatFixtureEmpty,
			latestSequence: 3,
			turns: [
				{
					id: "turn-with-prose-work",
					state: "completed",
					providerTurnId: "provider-turn-with-prose-work",
					requestedAt: "2026-08-11T10:01:00Z",
					startedAt: "2026-08-11T10:01:01Z",
					completedAt: "2026-08-11T10:01:04Z",
				},
			],
			items: [
				{
					kind: "message",
					id: "prompt",
					turnId: "turn-with-prose-work",
					sequence: 1,
					revision: 0,
					role: "user",
					origin: "human",
					text: "Inspect the implementation",
					streaming: false,
					createdAt: "2026-08-11T10:01:00Z",
				},
				{
					kind: "message",
					id: "intermediate-answer",
					turnId: "turn-with-prose-work",
					sequence: 2,
					revision: 0,
					role: "assistant",
					origin: "provider",
					text: "I found the relevant component.",
					streaming: false,
					createdAt: "2026-08-11T10:01:02Z",
				},
				{
					kind: "message",
					id: "final-answer",
					turnId: "turn-with-prose-work",
					sequence: 3,
					revision: 0,
					role: "assistant",
					origin: "provider",
					text: "The implementation is correct.",
					streaming: false,
					createdAt: "2026-08-11T10:01:04Z",
				},
			],
		};

		render(<ChatWorkspace snapshot={snapshot} />);
		const worked = screen.getByRole("button", { name: /Worked for/ });
		expect(screen.getByText("The implementation is correct.")).toBeVisible();

		await user.click(worked);
		expect(screen.getByText("I found the relevant component.")).toBeVisible();
	});

	it("keeps only the run the agent is working in open while the turn runs", () => {
		const turnId = "live-turn-runs";
		const at = "2026-08-11T10:01:00Z";
		const call = (id: string, sequence: number, summary: string) => ({
			kind: "activity" as const, id, turnId, sequence, revision: 0,
			activityKind: "command" as const, status: "completed" as const, summary,
			detail: { command: summary }, createdAt: at,
		});
		const snapshot: ConversationSnapshot = {
			...chatFixtureEmpty,
			controller: { state: "busy" },
			latestSequence: 7,
			turns: [{ id: turnId, state: "running", requestedAt: at, startedAt: at }],
			items: [
				{
					kind: "message", id: "prompt", turnId, sequence: 1, revision: 0,
					role: "user", origin: "human", text: "Fix the build",
					streaming: false, createdAt: at,
				},
				call("early-1", 2, "ls"),
				call("early-2", 3, "cat go.mod"),
				{
					kind: "message", id: "between", turnId, sequence: 4, revision: 0,
					role: "assistant", origin: "provider", text: "Found it, building now.",
					streaming: false, createdAt: at,
				},
				call("late-1", 5, "go build ./..."),
				call("late-2", 6, "go vet ./..."),
			],
		};

		render(<ChatWorkspace snapshot={snapshot} />);

		// The current run is open and shows each call; the earlier run is folded to
		// its summary line.
		const toggle = (name: RegExp) => screen.getByText(name).closest("button");
		expect(toggle(/^Explored 2 tool calls$/)).toHaveAttribute("aria-expanded", "false");
		expect(toggle(/^Ran 2 tool calls$/)).toHaveAttribute("aria-expanded", "true");
		expect(screen.getAllByText("Ran command")).toHaveLength(2);
	});

	it("starts a new visual turn at a steer: earlier work settles, the steer gets its own Working row", () => {
		const turnId = "turn-with-steer";
		const at = "2026-08-11T10:01:00Z";
		const snapshot: ConversationSnapshot = {
			...chatFixtureEmpty,
			controller: { state: "busy" },
			latestSequence: 5,
			turns: [{ id: turnId, state: "running", requestedAt: at, startedAt: at }],
			items: [
				{
					kind: "message", id: "prompt", turnId, sequence: 1, revision: 0,
					role: "user", origin: "human", text: "Run the unit tests",
					streaming: false, createdAt: at,
				},
				{
					kind: "message", id: "prose", turnId, sequence: 2, revision: 0,
					role: "assistant", origin: "provider", text: "Starting with the test suite.",
					streaming: false, createdAt: at,
				},
				{
					kind: "activity", id: "tool", turnId, sequence: 3, revision: 0,
					activityKind: "command", status: "completed", summary: "go test ./...",
					detail: { command: "go test ./..." }, createdAt: at,
				},
				{
					kind: "activity", id: "steer", turnId, sequence: 4, revision: 0,
					activityKind: "system", status: "completed", summary: "Only the unit ones, please",
					detail: { event: "steer", text: "Only the unit ones, please", origin: "human" },
					createdAt: at,
				},
				{
					kind: "message", id: "reply", turnId, sequence: 5, revision: 0,
					role: "assistant", origin: "provider", text: "Switching to the unit tests.",
					streaming: false, createdAt: at,
				},
			],
		};

		render(<ChatWorkspace snapshot={snapshot} />);

		// Read the transcript in document order: the work before the steer settles into
		// its own Worked row, and the steer opens a fresh Working row for the reply.
		const text = screen.getByRole("log").textContent ?? "";
		const positions = [
			"Run the unit tests",
			"Worked for",
			"Starting with the test suite.",
			"Only the unit ones, please",
			"Working for",
			"Switching to the unit tests.",
		].map((fragment) => text.indexOf(fragment));
		expect(positions.every((position) => position >= 0)).toBe(true);
		expect(positions).toEqual([...positions].sort((a, b) => a - b));
	});

	it("keeps a steer visible outside the folded work once the turn settles", async () => {
		const user = userEvent.setup();
		const turnId = "settled-turn-with-steer";
		const at = "2026-08-11T10:01:00Z";
		const snapshot: ConversationSnapshot = {
			...chatFixtureEmpty,
			controller: { state: "ready" },
			latestSequence: 5,
			turns: [{ id: turnId, state: "completed", requestedAt: at, startedAt: at, completedAt: "2026-08-11T10:02:00Z" }],
			items: [
				{
					kind: "message", id: "prompt", turnId, sequence: 1, revision: 0,
					role: "user", origin: "human", text: "Run the unit tests",
					streaming: false, createdAt: at,
				},
				{
					kind: "activity", id: "tool", turnId, sequence: 2, revision: 0,
					activityKind: "command", status: "completed", summary: "go test ./...",
					detail: { command: "go test ./..." }, createdAt: at,
				},
				{
					kind: "activity", id: "steer", turnId, sequence: 3, revision: 0,
					activityKind: "system", status: "completed", summary: "Only the unit ones, please",
					detail: { event: "steer", text: "Only the unit ones, please", origin: "human" },
					createdAt: at,
				},
				{
					kind: "message", id: "reply", turnId, sequence: 4, revision: 0,
					role: "assistant", origin: "provider", text: "Switching to the unit tests.",
					streaming: false, createdAt: at,
				},
			],
		};

		render(<ChatWorkspace snapshot={snapshot} />);

		expect(screen.getByText("Only the unit ones, please")).toBeVisible();
		const worked = screen.getByRole("button", { name: /Worked for/ });
		await user.click(worked);
		expect(screen.getAllByText("Only the unit ones, please")).toHaveLength(1);
		await user.click(worked);
		expect(screen.getByText("Only the unit ones, please")).toBeVisible();
	});

	it("replaces the generic working label with Claude's live retry count and backoff", () => {
		const snapshot = structuredClone(chatFixture);
		snapshot.items = snapshot.items.filter(
			(item) =>
				!(
					item.kind === "activity" &&
					item.activityKind === "approval" &&
					item.status === "pending"
				),
		);
		snapshot.items.push({
			kind: "activity",
			id: "retry-1",
			turnId: "turn-2",
			sequence: 100,
			revision: 2,
			activityKind: "system",
			status: "running",
			summary: "Reconnecting to Claude, attempt 2 of 10.",
			detail: {
				event: "provider.failure",
				category: "connection",
				severity: "warning",
				text: "The API request failed. Trying again in 4s.",
			},
			createdAt: "2026-08-28T00:00:00Z",
		});

		render(<ChatWorkspace snapshot={snapshot} onInterrupt={vi.fn()} />);

		const status = screen.getByTestId("live-turn-status");
		expect(status).toHaveTextContent("Reconnecting to Claude, attempt 2 of 10.");
		expect(status).toHaveTextContent("The API request failed. Trying again in 4s.");
		expect(status).not.toHaveTextContent("Working for");
		expect(screen.queryByTestId("live-working-label")).not.toBeInTheDocument();
	});

	it("interrupts the active turn when Escape is pressed", () => {
		const onInterrupt = vi.fn();
		const snapshot = structuredClone(chatFixture);
		snapshot.items = snapshot.items.filter(
			(item) =>
				!(
					item.kind === "activity" &&
					item.activityKind === "approval" &&
					item.status === "pending"
				),
		);

		render(<ChatWorkspace snapshot={snapshot} onInterrupt={onInterrupt} />);
		fireEvent.keyDown(screen.getByLabelText("Message the agent"), {
			key: "Escape",
		});

		expect(onInterrupt).toHaveBeenCalledOnce();
	});

	it("focuses the composer from conversation whitespace but preserves button focus", async () => {
		const user = userEvent.setup();
		const snapshot = structuredClone(chatFixture);
		snapshot.items = snapshot.items.filter(
			(item) =>
				!(
					item.kind === "activity" &&
					item.activityKind === "approval" &&
					item.status === "pending"
				),
		);

		render(<ChatWorkspace snapshot={snapshot} onInterrupt={vi.fn()} />);
		const composer = screen.getByLabelText("Message the agent");
		await user.click(screen.getByRole("log", { name: "Conversation" }));
		expect(document.activeElement).toBe(composer);

		const stop = screen.getByRole("button", { name: "Stop turn" });
		await user.click(stop);
		expect(document.activeElement).toBe(stop);
	});

	it("does not steal focus from a text selection in the conversation", () => {
		const snapshot = structuredClone(chatFixture);
		snapshot.items = snapshot.items.filter(
			(item) =>
				!(
					item.kind === "activity" &&
					item.activityKind === "approval" &&
					item.status === "pending"
				),
		);
		render(<ChatWorkspace snapshot={snapshot} onInterrupt={vi.fn()} />);

		const selection = window.getSelection();
		const range = document.createRange();
		const text = screen.getByRole("log", { name: "Conversation" }).querySelector("p")?.firstChild;
		expect(text).not.toBeNull();
		range.setStart(text as Text, 0);
		range.setEnd(text as Text, Math.min(4, text?.textContent?.length ?? 0));
		selection?.removeAllRanges();
		selection?.addRange(range);

		fireEvent.click(screen.getByRole("log", { name: "Conversation" }));
		expect(selection?.isCollapsed).toBe(false);
	});

	it("adds the selected excerpt through a compact neutral action without clearing selection on mouse down", () => {
		render(<ChatWorkspace snapshot={chatFixtureSettled} excerptsEnabled />);
		const log = screen.getByRole("log", { name: "Conversation" });
		const source = log.querySelector("[data-chat-message-id] [data-chat-message-body]")!;
		const range = document.createRange();
		range.selectNodeContents(source);
		const selectedText = range.toString().trim();
		const selection = window.getSelection()!;
		selection.removeAllRanges();
		selection.addRange(range);
		vi.spyOn(Range.prototype, "getClientRects").mockReturnValue([new DOMRect(40, 150, 100, 20)] as unknown as DOMRectList);
		vi.spyOn(log, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 0, 600, 500));
		fireEvent.mouseUp(log);
		const button = screen.getByRole("button", { name: "Add to chat" });
		expect(button).toHaveClass("shrink-0", "whitespace-nowrap", "hover:bg-interactive-hover");
		expect(button.parentElement).toHaveClass("bg-card", "border-border", "rounded-lg", "w-max");
		fireEvent.mouseDown(button);
		expect(selection.toString().trim()).toBe(selectedText);
		fireEvent.click(button);
		expect(screen.getByRole("button", { name: "1 annotation" })).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "1 annotation" }));
		expect(screen.getByRole("button", { name: selectedText })).toBeInTheDocument();
		selection.removeAllRanges();
	});

	it("hides Add to chat on surfaces whose send does not forward excerpts", () => {
		render(<ChatWorkspace snapshot={chatFixtureSettled} />);
		const log = screen.getByRole("log", { name: "Conversation" });
		const range = document.createRange();
		range.selectNodeContents(log.querySelector("[data-chat-message-id] [data-chat-message-body]")!);
		const selection = window.getSelection()!;
		selection.removeAllRanges();
		selection.addRange(range);
		fireEvent.mouseUp(log);
		expect(screen.queryByRole("button", { name: "Add to chat" })).not.toBeInTheDocument();
		selection.removeAllRanges();
	});
});
