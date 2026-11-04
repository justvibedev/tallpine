// The setup module registers vi.mock calls, so it must be imported before ChatWorkspace.
import {
	writeText,
	cleanupChatWorkspaceTest,
	resetChatWorkspaceMocks,
} from "./ChatWorkspace.test.setup";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
	fireEvent,
	screen,
	waitFor,
	within,
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
import { chatFixture, chatFixtureEmpty, chatFixtureSettled } from "../../lib/chat-fixture";
import { appI18n } from "../../i18n";
import type { ConversationMessage } from "../../types/conversation";
import { STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { writeChatComposerText } from "../../lib/chat-drafts";
import { renderChat as render } from "../../test/render-chat";
import {
	humanMessage,
	startingSnapshot,
	startingSteps,
	chatSession,
} from "./ChatWorkspaceTestHelpers";


beforeEach(resetChatWorkspaceMocks);

afterEach(async () => {
	await cleanupChatWorkspaceTest();
});

describe("ChatWorkspace timeline", () => {
	function withUserInput(status: "pending" | "resolved") {
		const snapshot = structuredClone(chatFixture);
		snapshot.turns[0] = { ...snapshot.turns[0], state: "running" };
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
			id: "input-1",
			sequence: 100,
			revision: 1,
			turnId: "turn-1",
			activityKind: "user_input",
			status,
			summary: "Choose a direction",
			requestId: "input-1",
			inputResponse: status === "resolved" ? {
				action: "accept",
				content: { question_0: "acp", question_0_custom: "Please tell me the worktree names first." },
			} : undefined,
			detail: {
				inputMode: "form",
				message: "Choose a direction",
				schema: {
					type: "object",
					properties: {
						question_0: {
							type: "string",
							title: "Which harness?",
							oneOf: [{ const: "acp", title: "ACP" }],
						},
						question_0_custom: { type: "string", title: "Other" },
					},
				},
			},
			createdAt: "2026-08-24T00:00:00Z",
		});
		return snapshot;
	}

	it("docks a pending question on the composer instead of the transcript", () => {
		render(<ChatWorkspace snapshot={withUserInput("pending")} onResolveInput={vi.fn()} />);

		expect(screen.getByTestId("elicitation-composer-dock")).toBeInTheDocument();
		expect(screen.getByRole("group", { name: "Agent question" })).toBeInTheDocument();
		expect(
			within(screen.getByRole("log", { name: "Conversation" })).queryByText("Which harness?"),
		).not.toBeInTheDocument();
	});

	it.each(["running", "completed"] as const)("keeps answered questions and feedback readable during a %s turn", async (state) => {
		const snapshot = withUserInput("resolved");
		snapshot.turns[0] = { ...snapshot.turns[0], state };
		render(<ChatWorkspace snapshot={snapshot} onResolveInput={vi.fn()} />);

		expect(screen.queryByTestId("elicitation-composer-dock")).not.toBeInTheDocument();
		expect(screen.queryByRole("group", { name: "Agent question" })).not.toBeInTheDocument();
		const history = within(screen.getByRole("region", { name: "Agent question history" }));
		await waitFor(() => expect(history.getByText("Choose a direction")).toBeVisible());
		expect(history.getByText("ACP")).toBeVisible();
		expect(history.getByText("Please tell me the worktree names first.")).toBeVisible();
		expect(history.queryByRole("button")).not.toBeInTheDocument();
	});

	it("retains the earlier reply when the agent asks another question in the same turn", () => {
		const snapshot = withUserInput("resolved");
		const pending = withUserInput("pending").items.find((item) => item.id === "input-1");
		if (!pending || pending.kind !== "activity") throw new Error("missing pending question fixture");
		snapshot.items.push({ ...pending, id: "input-2", requestId: "input-2", sequence: 101 });
		render(<ChatWorkspace snapshot={snapshot} onResolveInput={vi.fn()} />);

		expect(screen.getByTestId("elicitation-composer-dock")).toBeVisible();
		expect(within(screen.getByRole("region", { name: "Agent question history" })).getByText("Please tell me the worktree names first.")).toBeVisible();
	});

	it.each([false, true])("does not repeat a docked question in tool activity (other tools: %s)", async (withOtherTool) => {
		const snapshot = withUserInput("pending");
		snapshot.items = snapshot.items.filter((item) => item.kind === "activity" && item.activityKind === "user_input");
		snapshot.items.unshift({
			kind: "activity", id: "ask-tool", sequence: 99, revision: 0, turnId: "turn-1",
			activityKind: "mcp_tool", status: "pending", summary: "Choose a direction",
			detail: { providerToolName: "AskUserQuestion" }, createdAt: "2026-08-24T00:00:00Z",
		});
		if (withOtherTool) snapshot.items.unshift({
			kind: "activity", id: "other-tool", sequence: 98, revision: 0, turnId: "turn-1",
			activityKind: "mcp_tool", status: "completed", summary: "Inspect workspace metadata",
			detail: { providerToolName: "InspectWorkspace" }, createdAt: "2026-08-24T00:00:00Z",
		});
		render(<ChatWorkspace snapshot={snapshot} onResolveInput={vi.fn()} />);

		expect(screen.getAllByText("Choose a direction")).toHaveLength(1);
		expect(within(screen.getByTestId("elicitation-composer-dock")).getByText("Choose a direction")).toBeVisible();
		expect(within(screen.getByRole("log", { name: "Conversation" })).queryByText("Choose a direction")).not.toBeInTheDocument();
		if (withOtherTool) await waitFor(() => expect(screen.getByText("Inspect workspace metadata")).toBeVisible());
	});

	it.each(["no form", "failed tool", "other turn"])("keeps question tool activity visible for %s", async (scenario) => {
		const snapshot = withUserInput("pending");
		snapshot.items = snapshot.items.filter((item) => scenario !== "no form" && item.kind === "activity" && item.activityKind === "user_input");
		snapshot.items.unshift({
			kind: "activity", id: "ask-tool", sequence: 99, revision: 0,
			turnId: scenario === "other turn" ? "turn-2" : "turn-1",
			activityKind: "mcp_tool", status: scenario === "failed tool" ? "failed" : "pending",
			summary: "Choose a direction", detail: { providerToolName: "AskUserQuestion" },
			createdAt: "2026-08-24T00:00:00Z",
		});
		render(<ChatWorkspace snapshot={snapshot} onResolveInput={vi.fn()} />);

		await waitFor(() => expect(within(screen.getByRole("log", { name: "Conversation" })).getByText("Choose a direction")).toBeVisible());
	});

	it("does not interrupt while an elicitation is open", () => {
		const onInterrupt = vi.fn();
		const snapshot = structuredClone(chatFixture);
		snapshot.turns[0] = { ...snapshot.turns[0], state: "running" };
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
			id: "input-1",
			sequence: 100,
			revision: 1,
			turnId: "turn-1",
			activityKind: "user_input",
			status: "pending",
			summary: "Choose a direction",
			requestId: "input-1",
			detail: { inputMode: "form", message: "Choose a direction" },
			createdAt: "2026-08-24T00:00:00Z",
		});
		render(<ChatWorkspace snapshot={snapshot} onInterrupt={onInterrupt} />);
		fireEvent.keyDown(screen.getByLabelText("Message the agent"), {
			key: "Escape",
		});
		expect(onInterrupt).not.toHaveBeenCalled();
	});

	it("does not interrupt while a menu is open", () => {
		const onInterrupt = vi.fn();
		const snapshot = structuredClone(chatFixture);
		snapshot.turns[0] = { ...snapshot.turns[0], state: "running" };
		snapshot.items = snapshot.items.filter(
			(item) =>
				!(
					item.kind === "activity" &&
					item.activityKind === "approval" &&
					item.status === "pending"
				),
		);
		render(<ChatWorkspace snapshot={snapshot} onInterrupt={onInterrupt} />);
		const menu = document.createElement("div");
		menu.setAttribute("role", "menu");
		menu.setAttribute("data-state", "open");
		document.body.appendChild(menu);
		fireEvent.keyDown(screen.getByLabelText("Message the agent"), {
			key: "Escape",
		});
		expect(onInterrupt).not.toHaveBeenCalled();
		menu.remove();
	});

	it("shows the agent's reason and collapses a long approval command", async () => {
		const user = userEvent.setup();
		const snapshot = structuredClone(chatFixture);
		const longCommand = `git commit -m "${"x".repeat(400)}"`;
		snapshot.items.push({
			kind: "activity",
			id: "approval-1",
			sequence: 99,
			revision: 1,
			turnId: "turn-2",
			activityKind: "approval",
			status: "pending",
			summary: "bash",
			requestId: "approval-1",
			decisions: [{ id: "allow", label: "Allow", kind: "allow_once" }],
			detail: { command: longCommand, rawCommand: longCommand, reason: "Committing writes the git index" },
			createdAt: "2026-08-08T00:00:00Z",
		});

		render(<ChatWorkspace snapshot={snapshot} onDecide={vi.fn()} onInterrupt={vi.fn()} />);

		expect(screen.getByText("Committing writes the git index")).toBeInTheDocument();
		expect(screen.queryByText("Do you want to run this command?")).not.toBeInTheDocument();
		const toggle = screen.getByRole("button", { name: "Show full command" });
		expect(toggle).toHaveAttribute("aria-expanded", "false");
		await user.click(toggle);
		expect(screen.getByRole("button", { name: "Show less" })).toHaveAttribute("aria-expanded", "true");
	});

	it("moves a pending approval into the chat composer", async () => {
		const user = userEvent.setup();
		const onDecide = vi.fn();
		const snapshot = structuredClone(chatFixture);
		snapshot.items.push({
			kind: "activity",
			id: "approval-1",
			sequence: 99,
			revision: 1,
			turnId: "turn-2",
			activityKind: "approval",
			status: "pending",
			summary: "Run command",
			requestId: "approval-1",
			decisions: [
				{ id: "deny", label: "Deny", kind: "reject_once" },
				{ id: "allow_once", label: "Allow Once", kind: "allow_once" },
				{
					id: "always_allow",
					label: "Always Allow",
					kind: "allow_always",
				},
			],
			detail: { command: "npm test" },
			createdAt: "2026-08-08T00:00:00Z",
		});

		render(<ChatWorkspace snapshot={snapshot} onDecide={onDecide} onInterrupt={vi.fn()} />);

		expect(screen.getByRole("alert")).toHaveTextContent("The agent is waiting for your decision.");
		expect(screen.getByText("Do you want to run this command?")).toBeInTheDocument();
		expect(screen.queryByText("Waiting for your decision")).not.toBeInTheDocument();
		expect(screen.getAllByTestId("live-working-label")).toHaveLength(1);
		expect(screen.getByTestId("live-working-label")).toHaveTextContent(/^Working for /);
		const approval = screen.getByRole("group", {
			name: "Approval request approval-1",
		});
		const composer = approval.closest("form");
		expect(composer).toHaveClass("cursor-chat-composer");
		expect(composer).not.toHaveClass("border");
		expect(screen.getByRole("log", { name: "Conversation" })).not.toContainElement(approval);
		expect(screen.queryByLabelText("Message the agent")).not.toBeInTheDocument();
		expect(within(approval).queryByText("Terminal")).not.toBeInTheDocument();
		expect(within(approval).getByRole("button", { name: /Deny/ })).toHaveTextContent("DenyEsc");
		expect(within(approval).getByRole("button", { name: /Allow once/ })).toBeInTheDocument();
		expect(
			within(approval).getByRole("button", {
				name: "More approval options",
			}),
		).toBeInTheDocument();
		expect(within(approval).queryByRole("button", { name: "Allow Once" })).not.toBeInTheDocument();
		expect(
			within(approval).queryByRole("button", { name: "Always Allow" }),
		).not.toBeInTheDocument();
		expect(within(approval).queryByRole("button", { name: "Deny" })).not.toBeInTheDocument();
		await user.click(
			within(approval).getByRole("button", {
				name: "More approval options",
			}),
		);
		await user.keyboard("{Escape}");
		expect(onDecide).not.toHaveBeenCalled();

		approval.focus();
		fireEvent.keyDown(approval, { key: "Enter" });
		expect(onDecide).toHaveBeenCalledWith("approval-1", "allow_once");
		fireEvent.keyDown(approval, { key: "Escape" });
		expect(onDecide).toHaveBeenCalledWith("approval-1", "deny");
		onDecide.mockClear();

		const deny = within(approval).getByRole("button", { name: /Deny/ });
		deny.focus();
		fireEvent.keyDown(deny, { key: "Enter" });
		expect(onDecide).not.toHaveBeenCalled();
		fireEvent.click(deny);
		expect(onDecide).toHaveBeenCalledWith("approval-1", "deny");
		onDecide.mockClear();

		document.body.focus();
		fireEvent.keyDown(window, { key: "Enter" });
		expect(onDecide).not.toHaveBeenCalled();

		await user.click(within(approval).getByRole("button", { name: /Allow once/ }));
		expect(onDecide).toHaveBeenCalledWith("approval-1", "allow_once");

		await user.click(
			within(approval).getByRole("button", {
				name: "More approval options",
			}),
		);
		await user.click(screen.getByRole("menuitem", { name: "Always allow this command" }));
		expect(onDecide).toHaveBeenCalledWith("approval-1", "always_allow");
	});

	it("shows an unbound pending approval in the composer", () => {
		const snapshot = structuredClone(chatFixture);
		snapshot.items.push({
			kind: "activity",
			id: "approval-unbound",
			sequence: 100,
			revision: 1,
			activityKind: "approval",
			status: "pending",
			summary: "Run command",
			requestId: "0",
			decisions: [
				{ id: "cancel", label: "Cancel", kind: "reject_once" },
				{ id: "accept", label: "Approve", kind: "allow_once" },
			],
			detail: { command: "touch /tmp/marker" },
			createdAt: "2026-08-08T00:00:00Z",
		});

		render(<ChatWorkspace snapshot={snapshot} onDecide={vi.fn()} onInterrupt={vi.fn()} />);

		const approval = screen.getByRole("group", {
			name: "Approval request 0",
		});
		expect(approval.closest("form")).toHaveClass("cursor-chat-composer");
		expect(screen.queryByLabelText("Message the agent")).not.toBeInTheDocument();
	});

	it("uses ACP edit metadata and semantic kinds instead of guessing opaque labels", () => {
		const onDecide = vi.fn();
		const snapshot = structuredClone(chatFixture);
		snapshot.items.push({
			kind: "activity",
			id: "approval-edit",
			sequence: 101,
			revision: 1,
			turnId: "turn-2",
			activityKind: "approval",
			status: "pending",
			summary: "Write marker.txt",
			requestId: "opaque-request",
			decisions: [
				{ id: "option-a", label: "Einmal", kind: "allow_once" },
				{ id: "option-b", label: "Nein", kind: "reject_once" },
				{ id: "option-c", label: "Immer", kind: "allow_always" },
			],
			detail: { subjectKind: "file_change", toolKind: "edit" },
			createdAt: "2026-08-08T00:00:00Z",
		});

		render(<ChatWorkspace snapshot={snapshot} onDecide={onDecide} onInterrupt={vi.fn()} />);

		expect(screen.getByText("Do you want to allow these file changes?")).toBeInTheDocument();
		expect(screen.queryByText("Do you want to run this command?")).not.toBeInTheDocument();
		const approval = screen.getByRole("group", {
			name: "Approval request opaque-request",
		});
		approval.focus();
		fireEvent.keyDown(approval, { key: "Enter" });
		expect(onDecide).toHaveBeenCalledWith("opaque-request", "option-a");
	});

	it("keeps each answer's own wording when a question offers several options of one kind", async () => {
		const user = userEvent.setup();
		const onDecide = vi.fn();
		const snapshot = structuredClone(chatFixture);
		snapshot.items.push({
			kind: "activity",
			id: "approval-question",
			sequence: 102,
			revision: 1,
			turnId: "turn-2",
			activityKind: "approval",
			status: "pending",
			summary: "What topic or content would you like the 10 lines written about?",
			requestId: "question-request",
			decisions: [
				{ id: "1", label: "A 10-line bio for your portfolio", kind: "allow_once" },
				{ id: "2", label: "10 lines of code", kind: "allow_once" },
				{ id: "3", label: "A general 10-line poem", kind: "allow_once" },
			],
			detail: { subjectKind: "mcp_tool" },
			createdAt: "2026-08-08T00:00:00Z",
		});

		render(<ChatWorkspace snapshot={snapshot} onDecide={onDecide} onInterrupt={vi.fn()} />);

		const approval = screen.getByRole("group", { name: "Approval request question-request" });
		expect(within(approval).queryByRole("button", { name: /Allow once/ })).not.toBeInTheDocument();
		for (const label of [/A 10-line bio for your portfolio/, /10 lines of code/, /A general 10-line poem/]) {
			expect(within(approval).getByRole("button", { name: label })).toBeInTheDocument();
		}
		await user.click(within(approval).getByRole("button", { name: /A general 10-line poem/ }));
		expect(onDecide).toHaveBeenCalledWith("question-request", "3");
	});

	it("requires an explicit click for decisions without a semantic kind", () => {
		const onDecide = vi.fn();
		const snapshot = structuredClone(chatFixture);
		snapshot.items.push({
			kind: "activity",
			id: "approval-unknown",
			sequence: 102,
			revision: 1,
			turnId: "turn-2",
			activityKind: "approval",
			status: "pending",
			summary: "Unknown provider decision",
			requestId: "unknown-request",
			decisions: [
				{
					id: "acceptWithDifferentSemantics",
					label: "acceptWithDifferentSemantics",
				},
				{ id: "cancel", label: "Cancel", kind: "reject_once" },
			],
			detail: { subjectKind: "command" },
			createdAt: "2026-08-08T00:00:00Z",
		});

		render(<ChatWorkspace snapshot={snapshot} onDecide={onDecide} onInterrupt={vi.fn()} />);

		const approval = screen.getByRole("group", {
			name: "Approval request unknown-request",
		});
		expect(within(approval).queryByRole("button", { name: /Allow once/ })).not.toBeInTheDocument();
		const providerDecision = within(approval).getByRole("button", {
			name: "acceptWithDifferentSemantics",
		});

		approval.focus();
		fireEvent.keyDown(approval, { key: "Enter" });
		expect(onDecide).not.toHaveBeenCalled();

		fireEvent.click(providerDecision);
		expect(onDecide).toHaveBeenCalledWith("unknown-request", "acceptWithDifferentSemantics");
	});

	it.each([
		["accept", "Approved"],
		["acceptWithExecpolicyAmendment", "Approved and remembered"],
		["cancel", "Cancelled"],
	] as const)("keeps a resolved %s approval compact and explicit", (decision, label) => {
		const snapshot = structuredClone(chatFixture);
		snapshot.items = [
			{
				kind: "activity",
				id: "approval-resolved-1",
				sequence: 99,
				revision: 2,
				turnId: "turn-2",
				activityKind: "approval",
				status: "resolved",
				summary: "Run gh pr create --base main --head ao/example",
				requestId: "approval-resolved-1",
				detail: { decision },
				createdAt: "2026-08-08T00:00:00Z",
			},
		];

		render(<ChatWorkspace snapshot={snapshot} />);

		expect(screen.getByText(label)).toBeInTheDocument();
		expect(screen.getByText("Run gh pr create --base main --head ao/example")).toBeInTheDocument();
		expect(screen.queryByText(/req approval-resolved-1/)).not.toBeInTheDocument();
		expect(
			screen.queryByText("Already answered. This card is kept for the record."),
		).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Approve" })).not.toBeInTheDocument();
	});

	it("uses the preserved semantic kind for an opaque resolved decision", () => {
		const snapshot = structuredClone(chatFixture);
		snapshot.items = [
			{
				kind: "activity",
				id: "approval-resolved-opaque",
				sequence: 99,
				revision: 2,
				turnId: "turn-2",
				activityKind: "approval",
				status: "resolved",
				summary: "Opaque provider decision",
				requestId: "approval-resolved-opaque",
				decisions: [{ id: "option-b", label: "Nein", kind: "reject_once" }],
				detail: { decision: "option-b" },
				createdAt: "2026-08-08T00:00:00Z",
			},
		];

		render(<ChatWorkspace snapshot={snapshot} />);

		expect(screen.getByText("Cancelled")).toBeInTheDocument();
		expect(screen.queryByText("Resolved")).not.toBeInTheDocument();
	});

	it("does not describe an expired approval as approved", () => {
		const snapshot = structuredClone(chatFixture);
		snapshot.items = [
			{
				kind: "activity",
				id: "approval-expired-1",
				sequence: 99,
				revision: 2,
				turnId: "turn-2",
				activityKind: "approval",
				status: "failed",
				summary: "Run npm test",
				requestId: "approval-expired-1",
				createdAt: "2026-08-08T00:00:00Z",
			},
		];

		render(<ChatWorkspace snapshot={snapshot} />);

		expect(screen.getByText("Approval expired")).toBeInTheDocument();
		expect(screen.queryByText("Approved")).not.toBeInTheDocument();
	});

	it("lets readers select conversation text", () => {
		render(<ChatWorkspace snapshot={chatFixture} />);

		expect(screen.getByRole("log", { name: "Conversation" })).toHaveClass("select-text");
		expect(screen.getByTestId("chat-timeline")).toHaveStyle({ contain: "layout paint" });
	});

	it("copies rendered message links on plain click", async () => {
		const user = userEvent.setup();
		const snapshot = structuredClone(chatFixtureSettled);
		const message = snapshot.items.find(
			(item): item is ConversationMessage => item.kind === "message" && item.role === "assistant",
		);
		if (!message) throw new Error("fixture has no assistant message");
		message.text = "Open the [local preview](http://localhost:5173).";
		const onLinkOpen = vi.fn();

		render(<ChatWorkspace snapshot={snapshot} onLinkOpen={onLinkOpen} />);
		await user.click(screen.getByRole("link", { name: "local preview" }));

		expect(writeText).toHaveBeenCalledWith("http://localhost:5173");
		expect(onLinkOpen).not.toHaveBeenCalled();
	});

	it.each([
		["human", "user"],
		["automation", "user"],
		["daemon", "user"],
		["provider", "assistant"],
	] as const)("activates session links from %s messages", async (origin, role) => {
		const snapshot = structuredClone(chatFixtureSettled);
		const template = snapshot.items.find((item): item is ConversationMessage => item.kind === "message");
		if (!template) throw new Error("fixture has no message");
		snapshot.items = [{ ...template, id: `link-${origin}`, origin, role, text: "tallpine://sessions/project/session", streaming: false }];
		const onSessionLinkOpen = vi.fn();
		render(<ChatWorkspace snapshot={snapshot} onSessionLinkOpen={onSessionLinkOpen} />);
		await userEvent.setup().click(screen.getByRole("link"));
		expect(onSessionLinkOpen).toHaveBeenCalledWith("tallpine://sessions/project/session");
	});

	it("offers real recovery actions when the controller stops", async () => {
		const user = userEvent.setup();
		const resume = vi.fn();
		const openShell = vi.fn();
		render(
			<ChatWorkspace
				snapshot={{
					...chatFixtureSettled,
					controller: { state: "stopped" },
				}}
				onResumeAgent={resume}
				onOpenShell={openShell}
			/>,
		);

		expect(screen.getByRole("alert")).toHaveTextContent("The agent controller stopped");
		expect(screen.getByText("The controller is not connected")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Resume agent" }));
		await user.click(screen.getByRole("button", { name: "Open shell" }));
		expect(resume).toHaveBeenCalledOnce();
		expect(openShell).toHaveBeenCalledOnce();
	});

	it("shows a reconnect in progress while the daemon is still checking the session", () => {
		const stopped = { ...chatFixtureSettled, controller: { state: "stopped" as const } };
		const view = render(
			<ChatWorkspace
				snapshot={stopped}
				session={{ ...chatSession, statusReadiness: "checking" }}
				onResumeAgent={vi.fn()}
				onOpenShell={vi.fn()}
			/>,
		);

		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(screen.getByText("Tallpine restarted. History is kept.")).toBeInTheDocument();
		// The banner title and the composer placeholder both say so.
		expect(screen.getAllByText("Reconnecting to the agent…")).toHaveLength(2);
		expect(screen.queryByRole("button", { name: "Resume agent" })).not.toBeInTheDocument();
		expect(screen.queryByText("The controller is not connected")).not.toBeInTheDocument();

		// Once the check fails for good, the real recovery actions come back.
		view.rerender(
			<ChatWorkspace
				snapshot={stopped}
				session={{ ...chatSession, statusReadiness: "unavailable" }}
				onResumeAgent={vi.fn()}
				onOpenShell={vi.fn()}
			/>,
		);
		expect(screen.getByRole("alert")).toHaveTextContent("The agent controller stopped");
		expect(screen.getByRole("button", { name: "Resume agent" })).toBeInTheDocument();
	});

	it("keeps a sleeping conversation usable without exposing wake controls", async () => {
		const user = userEvent.setup();
		const resume = vi.fn();
		const send = vi.fn();
		const sessionId = "hibernated-chat-test";
		writeChatComposerText(sessionId, "Continue");
		const snapshot = { ...chatFixtureSettled, sessionId, controller: { state: "hibernated" as const } };
		const view = render(
			<ChatWorkspace
				snapshot={snapshot}
				onResumeAgent={resume}
				onSend={send}
			/>,
		);

		expect(screen.queryByText("Agent hibernated")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Wake agent" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Reconnect" })).not.toBeInTheDocument();
		expect(screen.getAllByText(/Check the worktree state/).length).toBeGreaterThan(0);
		const composer = screen.getByRole("combobox", { name: "Message the agent" });
		expect(composer).toHaveAttribute("contenteditable", "true");
		await waitFor(() => expect(composer).toHaveTextContent("Continue"));
		expect(resume).not.toHaveBeenCalled();

		await typeInLexicalEditor(composer, " this work");
		expect(resume).not.toHaveBeenCalled();
		view.rerender(<ChatWorkspace snapshot={{ ...snapshot, controller: { state: "stopped" } }} onResumeAgent={resume} onSend={send} resumingAgent resumeError="resume agent error" />);
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(screen.queryByText("The agent controller stopped")).not.toBeInTheDocument();
		expect(screen.queryByText("resume agent error")).not.toBeInTheDocument();
		expect(composer).toHaveAttribute("contenteditable", "true");
		await user.click(screen.getByRole("button", { name: "Send message" }));
		await waitFor(() => expect(send).toHaveBeenCalledOnce());
		expect(send.mock.calls[0]?.[0]).toBe("Continue this work");
		expect(resume).not.toHaveBeenCalled();

		view.rerender(<ChatWorkspace snapshot={{ ...snapshot, controller: { state: "stopped" } }} onResumeAgent={resume} />);
		expect(screen.getByRole("alert")).toHaveTextContent("The agent controller stopped");
		expect(screen.getByRole("button", { name: "Resume agent" })).toBeInTheDocument();
	});

	it("does not race view activation when hibernation arrives after a draft edit", async () => {
		const resume = vi.fn(async () => undefined);
		const snapshot = { ...chatFixtureSettled, sessionId: "hibernate-race", controller: { state: "ready" as const } };
		const view = render(<ChatWorkspace snapshot={snapshot} onResumeAgent={resume} />);
		await typeInLexicalEditor(screen.getByRole("combobox", { name: "Message the agent" }), "Continue");
		expect(resume).not.toHaveBeenCalled();

		view.rerender(<ChatWorkspace snapshot={{ ...snapshot, controller: { state: "hibernated" } }} onResumeAgent={resume} />);
		await typeInLexicalEditor(screen.getByRole("combobox", { name: "Message the agent" }), " again");
		expect(resume).not.toHaveBeenCalled();
		expect(screen.queryByRole("button", { name: "Wake agent" })).not.toBeInTheDocument();
	});

	// An asynchronous spawn puts the session on screen before its agent exists.
	// The opening brief reads as sent, the setup checklist sits where the reply
	// will appear, and the composer stays open: what the user types is queued.
	it("shows a starting session's checklist under its opening brief and keeps it typeable", () => {
		render(
			<ChatWorkspace
				snapshot={startingSnapshot("queued")}
				session={{ ...chatSession, branch: "ao/mer-1", provisionState: "provisioning", provisionSteps: startingSteps("running") }}
				onResumeAgent={vi.fn()}
			/>,
		);

		const startup = screen.getByTestId("session-startup");
		expect(screen.getByTestId("live-working-label")).toHaveTextContent(/^Setting up session$/);
		expect(screen.getByTestId("live-working-label").previousElementSibling?.querySelector("svg")).toBeInTheDocument();
		// It takes the Working row's slot inside the brief's turn, so the handoff moves nothing.
		expect(startup.closest("[data-chat-scroll-anchor]")).toContainElement(screen.getByText("Fix clicking attachments"));
		const steps = within(startup).getAllByRole("listitem");
		expect(steps.map((step) => [step.dataset.step, step.dataset.status])).toEqual([
			["fetch", "done"],
			["worktree", "done"],
			["agent", "running"],
		]);
		expect(steps[1]).toHaveTextContent("Workspace created");
		expect(steps[1]).not.toHaveTextContent("ao/mer-1");
		expect(steps[2]).toHaveTextContent("Getting Codex ready");
		expect(screen.getByText("Fix clicking attachments")).toBeInTheDocument();
		expect(screen.queryByText("Queued · sends when the agent finishes")).not.toBeInTheDocument();
		expect(screen.queryByTestId("queued-message-dock")).not.toBeInTheDocument();
		expect(screen.getByText("Codex is starting. Messages send in order")).toBeInTheDocument();
		expect(screen.queryByText(/^Working for /)).not.toBeInTheDocument();
		expect(screen.queryByText("The agent controller stopped")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Resume agent" })).not.toBeInTheDocument();
		expect(screen.getByTestId("chat-conversation-panel")).not.toHaveAttribute("inert");
	});

	it("localizes the startup composer queue hint", async () => {
		await appI18n.changeLanguage("zh-CN");
		render(
			<ChatWorkspace
				snapshot={startingSnapshot("queued")}
				session={{ ...chatSession, provisionState: "provisioning", provisionSteps: startingSteps("running") }}
				onResumeAgent={vi.fn()}
			/>,
		);
		expect(screen.getByText("Codex 正在启动. 消息将按顺序发送")).toBeInTheDocument();
	});

	// The checklist keeps the working slot until the agent is up and its first
	// turn is live, then leaves nothing behind.
	it("hands a clean start over to the working line while preserving its spinner and collapsing the checklist", async () => {
		const view = render(
			<ChatWorkspace
				snapshot={startingSnapshot("queued")}
				session={{ ...chatSession, provisionState: "provisioning", provisionSteps: startingSteps("done") }}
			/>,
		);
		expect(screen.getByTestId("session-startup")).toBeInTheDocument();
		const label = screen.getByTestId("live-working-label");
		const spinner = label.previousElementSibling?.querySelector("svg");
		view.rerender(
			<ChatWorkspace
				snapshot={startingSnapshot("queued")}
				session={{ ...chatSession, provisionState: "ready", provisionSteps: startingSteps("done") }}
			/>,
		);
		expect(screen.getByTestId("live-working-label")).toBe(label);
		expect(label).toHaveTextContent(/^Setting up session$/);
		expect(label.previousElementSibling?.querySelector("svg")).toBe(spinner);

		view.rerender(
			<ChatWorkspace
				snapshot={startingSnapshot("running")}
				session={{ ...chatSession, provisionState: "provisioning", provisionSteps: startingSteps("done") }}
			/>,
		);
		expect(screen.getByTestId("live-working-label")).toBe(label);
		expect(label.previousElementSibling?.querySelector("svg")).toBe(spinner);
		await waitFor(() => expect(screen.queryByTestId("session-startup")).not.toBeInTheDocument());
		expect(screen.getByText(/^Working for /)).toBeInTheDocument();

		view.rerender(
			<ChatWorkspace
				snapshot={startingSnapshot("running")}
				session={{ ...chatSession, provisionState: "ready", provisionSteps: startingSteps("done") }}
			/>,
		);
		expect(screen.queryByTestId("session-startup")).not.toBeInTheDocument();
		expect(screen.getByText("Fix clicking attachments")).toBeInTheDocument();
		expect(label.previousElementSibling?.querySelector("svg")).toBe(spinner);
	});

	it("keeps follow-up messages queued when the opening brief starts running", () => {
		const snapshot = startingSnapshot("running");
		snapshot.turns.push({ id: "follow-up", state: "queued", requestedAt: "2026-08-15T00:00:08Z" });
		snapshot.items.push({ ...humanMessage("And add a regression check"), id: "follow-up-message", turnId: "follow-up", sequence: 2 });
		render(
			<ChatWorkspace
				snapshot={snapshot}
				session={{ ...chatSession, provisionState: "provisioning", provisionSteps: startingSteps("running") }}
			/>,
		);
		expect(screen.getByTestId("queued-message-dock")).toHaveTextContent("And add a regression check");
		expect(screen.getByTestId("session-startup").closest("[data-chat-scroll-anchor]")).toContainElement(screen.getByText("Fix clicking attachments"));
	});

	it("waits for every startup step before handing over to Working", () => {
		render(
			<ChatWorkspace
				snapshot={startingSnapshot("running")}
				session={{ ...chatSession, branch: "ao/mer-1/root", provisionState: "provisioning", provisionSteps: startingSteps("done").map((step) => step.id === "worktree" ? { ...step, status: "running", endedAt: undefined } : step) }}
			/>,
		);
		expect(screen.getByTestId("live-working-label")).toHaveTextContent(/^Setting up session$/);
		expect(screen.getByTestId("session-startup")).toBeInTheDocument();
		expect(within(screen.getByTestId("session-startup")).getByText("Preparing your workspace")).toHaveClass("chat-working-shimmer");
		expect(screen.queryByText("ao/mer-1/root")).not.toBeInTheDocument();
	});

	it("waits for controller and provisioning readiness before enabling permission changes", () => {
		const onChooseSettings = vi.fn();
		const snapshot = { ...chatFixtureSettled, controller: { state: "connecting" as const } };
		const renderChat = (controller: "connecting" | "ready", provisionState: "provisioning" | "ready") => (
			<ChatWorkspace
				snapshot={{ ...snapshot, controller: { state: controller } }}
				session={{ ...chatSession, provisionState }}
				onChooseSettings={onChooseSettings}
			/>
		);
		const view = render(renderChat("connecting", "provisioning"));
		const approval = () => screen.getByRole("button", { name: "Approval policy for the next turn" });
		expect(approval()).toBeDisabled();
		expect(screen.getByRole("combobox", { name: "Message the agent" })).toBeEnabled();

		view.rerender(renderChat("ready", "provisioning"));
		expect(approval()).toBeDisabled();

		view.rerender(renderChat("ready", "ready"));
		expect(approval()).toBeEnabled();
		expect(onChooseSettings).not.toHaveBeenCalled();
	});

	it("shimmers the entire centered composer until the orchestrator is ready", async () => {
		const view = render(
			<ChatWorkspace
				sessionRole="orchestrator"
				snapshot={{ ...chatFixtureEmpty, controller: { state: "connecting" } }}
				session={{ ...chatSession, kind: "orchestrator", provisionState: "provisioning", provisionSteps: startingSteps("running") }}
			/>,
		);
		expect(screen.getByLabelText("Message the agent").closest("form")).toHaveAttribute("data-starting", "true");
		expect(screen.queryByTestId("orchestrator-startup-status")).not.toBeInTheDocument();
		expect(screen.queryByTestId("session-startup")).not.toBeInTheDocument();
		expect(screen.getByText("What do you want to work on?")).toBeInTheDocument();
		const composer = screen.getByLabelText("Message the agent");
		expect(screen.getByText("Starting your orchestrator")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Send message" })).toBeDisabled();
		view.rerender(<ChatWorkspace sessionRole="orchestrator" snapshot={chatFixtureEmpty} session={{ ...chatSession, kind: "orchestrator", provisionState: "ready" }} />);
		expect(screen.getByLabelText("Message the agent")).toBe(composer);
		expect(composer.closest("form")).not.toHaveAttribute("data-starting");
		expect(await screen.findByText("Ask anything about this project")).toBeInTheDocument();
	});

	it.each([
		["fetch", "Getting the latest code"],
		["worktree", "Preparing your workspace"],
		["setup", "Running your project setup"],
		["agent", "Starting your orchestrator"],
	] as const)("shows the actual %s setup step in the placeholder", (id, message) => {
		render(<ChatWorkspace snapshot={{ ...chatFixtureEmpty, controller: { state: "connecting" } }} sessionRole="orchestrator" startingSteps={[{ id, status: "running" }]} />);
		expect(screen.getByText(message)).toBeInTheDocument();
	});

	it("offers sample artifact requests in an empty artifact session, fills the composer with one, and hides them", async () => {
		const user = userEvent.setup();
		render(<ChatWorkspace snapshot={chatFixtureEmpty} session={{ ...chatSession, workspaceId: STANDALONE_WORKSPACE_ID, requestedOutput: "artifact" }} />);

		expect(screen.getByRole("heading", { name: "What do you want to create?" })).toBeInTheDocument();
		expect(screen.getByText("Describe the page or document you want")).toBeInTheDocument();
		const prompts = within(screen.getByRole("list", { name: "Sample artifact requests" })).getAllByRole("button");
		expect(prompts.map((prompt) => prompt.textContent)).toEqual([
			"Write a one-page plan for a new feature",
			"Build an HTML page comparing three options side by side",
			"Turn my rough notes into a polished Markdown document",
		]);

		await user.click(prompts[1]);
		await waitFor(() => expect(screen.getByLabelText("Message the agent")).toHaveTextContent("Build an HTML page comparing three options side by side"));
		expect(screen.getByRole("button", { name: "Send message" })).toBeEnabled();
		await waitFor(() => expect(screen.queryByRole("list", { name: "Sample artifact requests" })).not.toBeInTheDocument());
	});

	it("offers ordinary sample requests in an empty non-artifact chat and fills the composer with one", async () => {
		const user = userEvent.setup();
		render(<ChatWorkspace snapshot={chatFixtureEmpty} session={chatSession} />);

		expect(screen.getByRole("heading", { name: "What do you want to work on?" })).toBeInTheDocument();
		expect(screen.queryByRole("list", { name: "Sample artifact requests" })).not.toBeInTheDocument();
		const prompts = within(screen.getByRole("list", { name: "Sample requests" })).getAllByRole("button");
		expect(prompts.map((prompt) => prompt.textContent)).toEqual([
			"Review my recent changes for bugs",
			"Add tests for the code I am working on",
			"Walk me through how a request flows through this code",
		]);

		await user.click(prompts[0]);
		await waitFor(() => expect(screen.getByLabelText("Message the agent")).toHaveTextContent("Review my recent changes for bugs"));
		await waitFor(() => expect(screen.queryByRole("list", { name: "Sample requests" })).not.toBeInTheDocument());
	});

	it("offers sample artifact requests to artifact sessions outside Scratchpad too", () => {
		render(<ChatWorkspace snapshot={chatFixtureEmpty} session={{ ...chatSession, requestedOutput: "artifact" }} />);

		expect(screen.getByRole("heading", { name: "What do you want to create?" })).toBeInTheDocument();
		expect(screen.getByRole("list", { name: "Sample artifact requests" })).toBeInTheDocument();
	});

	it("drops the sample requests once the conversation has messages", () => {
		render(<ChatWorkspace snapshot={chatFixture} session={chatSession} />);

		expect(screen.queryByRole("list", { name: "Sample requests" })).not.toBeInTheDocument();
	});

	it("waits for provider settings before showing composer dropdowns", () => {
		const props = {
			snapshot: chatFixtureEmpty,
			onChooseSettings: vi.fn(),
			models: [{ id: "test-model", displayName: "Test model", default: true }],
		};
		const view = render(<ChatWorkspace {...props} settingsReady={false} />);
		expect(screen.queryByRole("group", { name: "Turn settings" })).not.toBeInTheDocument();
		view.rerender(<ChatWorkspace {...props} settingsReady />);
		expect(screen.getByRole("group", { name: "Turn settings" })).toBeInTheDocument();
	});

	it("keys only the switch marker nearest a message with that message", () => {
		const marker = (id: string, sequence: number, targetHarness: string) => ({
			kind: "activity" as const, id, sequence, revision: 1, activityKind: "system" as const, status: "completed" as const,
			summary: "Switched provider", detail: { event: "provider_switch" as const, sourceHarness: "codex", targetHarness },
			createdAt: "2026-08-08T00:00:00Z",
		});
		const errors = vi.spyOn(console, "error").mockImplementation(() => {});
		// A switch whose send failed, then another switch, then the message.
		render(
			<QueryClientProvider client={new QueryClient()}>
				<ChatWorkspace
					snapshot={{
						...chatFixtureEmpty, latestSequence: 3,
						items: [marker("switch-1", 1, "claude-code"), marker("switch-2", 2, "opencode"), { ...humanMessage("hello"), sequence: 3, clientMessageId: "m1" }],
					}}
				/>
			</QueryClientProvider>,
		);
		expect(errors.mock.calls.flat().join(" ")).not.toMatch(/same key/);
		errors.mockRestore();
	});

	it("keeps the Switched line's node when the daemon's line comes before its message", () => {
		const localEchos = [{
			clientMessageId: "m1", text: "hello", createdAt: "2026-08-08T00:00:00Z",
			providerSwitch: { sourceHarness: "codex", targetHarness: "claude-code", afterSequence: 0 },
		}];
		const view = render(
			<QueryClientProvider client={new QueryClient()}>
				<ChatWorkspace snapshot={{ ...chatFixtureEmpty, latestSequence: 0, items: [] }} localEchos={localEchos} />
			</QueryClientProvider>,
		);
		const before = screen.getByRole("separator");
		view.rerender(
			<QueryClientProvider client={new QueryClient()}>
				<ChatWorkspace
					snapshot={{
						...chatFixtureEmpty, latestSequence: 1,
						items: [{
							kind: "activity", id: "switch-1", sequence: 1, revision: 1, activityKind: "system", status: "completed",
							summary: "Switched provider", detail: { event: "provider_switch", sourceHarness: "codex", targetHarness: "claude-code" },
							createdAt: "2026-08-08T00:00:00Z",
						}],
					}}
					localEchos={localEchos}
				/>
			</QueryClientProvider>,
		);
		expect(screen.getByRole("separator")).toBe(before);
	});
});
