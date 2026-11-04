/**
 * The orchestrator chat draws worker traffic as rows. These pin what a reader
 * keeps when it does: a quiet reply still copies and rolls back, a reply that asks
 * something stays a full reply, and a turn with no reply still shows its work.
 */

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { workspaceQueryKey } from "../../hooks/useWorkspaceQuery";
import { chatFixtureEmpty } from "../../lib/chat-fixture";
import type { ConversationActivity, ConversationItem, ConversationMessage, ConversationSnapshot } from "../../types/conversation";
import type { WorkspaceSummary } from "../../types/workspace";
import { TooltipProvider } from "../ui/tooltip";

const at = (minute: number) => `2026-10-22T03:${String(minute).padStart(2, "0")}:00Z`;

function automation(turnId: string, minute: number, text: string): ConversationMessage {
	return {
		kind: "message", id: `${turnId}-report`, turnId, sequence: minute * 10, revision: 0, role: "user", origin: "automation",
		streaming: false, createdAt: at(minute), text,
	};
}

const report = (turnId: string, minute: number, entry: string) =>
	automation(turnId, minute, `Reports since your previous turn:\n\n${entry}`);

function command(turnId: string, minute: number, text: string): ConversationActivity {
	return {
		kind: "activity", id: `${turnId}-command`, turnId, sequence: minute * 10 + 1, revision: 0, activityKind: "command",
		status: "completed", summary: text, detail: { command: text, output: "ok" }, createdAt: at(minute),
	};
}

function reply(turnId: string, minute: number, text: string): ConversationMessage {
	return {
		kind: "message", id: `${turnId}-reply`, turnId, sequence: minute * 10 + 2, revision: 0, role: "assistant", origin: "provider",
		streaming: false, createdAt: at(minute), text,
	};
}

function snapshot(items: ConversationItem[]): ConversationSnapshot {
	const turnIds = [...new Set(items.map((item) => item.turnId as string))];
	return {
		...chatFixtureEmpty,
		latestSequence: Math.max(...items.map((item) => item.sequence)),
		turns: turnIds.map((id) => ({
			id, state: "completed" as const, providerTurnId: `provider-${id}`,
			requestedAt: items.find((item) => item.turnId === id)!.createdAt,
			completedAt: items.filter((item) => item.turnId === id).at(-1)!.createdAt,
		})),
		items,
	};
}

function renderOrchestrator(items: ConversationItem[], onRollback = vi.fn(), workspaces: WorkspaceSummary[] = [], sessionRole: "orchestrator" | "worker" = "orchestrator") {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	queryClient.setQueryData(workspaceQueryKey, workspaces);
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<ChatWorkspace snapshot={snapshot(items)} sessionRole={sessionRole} onRollback={onRollback} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	return onRollback;
}

describe("orchestrator worker updates", () => {
	it("draws a report as a row and an answer that asks nothing as one line that keeps copy and rollback", async () => {
		const onRollback = renderOrchestrator([
			report("t1", 1, "[done] [Check regressions](tallpine://sessions/proj/proj-288)\nOne strong candidate: #7043\nScreened 58 PRs."),
			command("t1", 1, "gh pr merge 7054"),
			reply("t1", 2, "Merged [#7054](https://github.com/o/r/pull/7054)."),
		]);

		const row = screen.getByTestId("worker-update");
		expect(row).toHaveTextContent("Check regressions");
		expect(row).toHaveTextContent("One strong candidate: #7043");
		expect(screen.queryByText(/Reports since your previous turn/)).not.toBeInTheDocument();

		const answer = screen.getByTestId("orchestrator-reply");
		expect(answer).toHaveTextContent("Merged #7054.");
		expect(screen.queryByRole("button", { name: /^Worked for/ })).not.toBeInTheDocument();
		expect(within(answer).getByRole("button", { name: "Copy message as markdown" })).toBeInTheDocument();

		const work = within(answer).getByRole("button", { name: "Show the work behind this reply" });
		await userEvent.click(work);
		expect(work).toHaveAttribute("aria-expanded", "true");

		await userEvent.click(within(answer).getByRole("button", { name: "Roll back to here" }));
		await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Roll back" }));
		expect(onRollback).toHaveBeenCalledWith("t1");
	});

	it("draws a report that lists an artifact as a card with the file under it, not as raw text", async () => {
		renderOrchestrator([
			report("t1", 1, "[done] [Write plan](tallpine://sessions/proj/proj-9)\nPlan is ready.\nartifact: plans/q3.md"),
			reply("t1", 2, "Read it."),
		]);

		const row = screen.getByTestId("worker-update");
		expect(row).toHaveTextContent("Write plan");
		expect(row).toHaveTextContent("q3.md");
		// Open, the body keeps the report's words but not the artifact line the card lifted out.
		await userEvent.click(within(row).getByRole("button", { expanded: false }));
		expect(row).toHaveTextContent("Plan is ready.");
		expect(row).not.toHaveTextContent("artifact:");
	});

	it("draws a report with no artifact as a card without a file line", () => {
		renderOrchestrator([
			report("t1", 1, "[done] [Write plan](tallpine://sessions/proj/proj-9)\nPlan is ready."),
			reply("t1", 2, "Read it."),
		]);

		const row = screen.getByTestId("worker-update");
		expect(row).toHaveTextContent("Plan is ready");
		expect(row).not.toHaveTextContent(".md");
	});

	it("keeps an answer that asks the user something as the full reply", () => {
		renderOrchestrator([
			report("t1", 1, "[needs_input] [iOS pages](tallpine://sessions/proj/proj-286)\nTunnel or loopback?"),
			command("t1", 1, "tallpine session logs proj-286"),
			reply("t1", 2, "Checked its logs.\n\nShould it load over the tunnel?"),
		]);

		expect(screen.queryByTestId("orchestrator-reply")).not.toBeInTheDocument();
		expect(screen.getByText("Checked its logs.")).toBeInTheDocument();
		expect(screen.getByText("Should it load over the tunnel?")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /^Worked for/ })).toBeInTheDocument();
	});

	it("labels a needs-input report neutrally while it waits on the orchestrator, and as needing the human once escalated", () => {
		const workspaceWith = (needsInputAudience: "orchestrator" | "human") => [{
			id: "proj", name: "proj", path: "/tmp/proj",
			sessions: [{ id: "proj-286", workspaceId: "proj", title: "iOS pages", provider: "codex", kind: "worker", status: "needs_input", needsInputAudience, prs: [] }],
		}] as unknown as WorkspaceSummary[];
		const items = [
			report("t1", 1, "[needs_input] [iOS pages](tallpine://sessions/proj/proj-286)\nTunnel or loopback?"),
			reply("t1", 2, "On it."),
		];

		renderOrchestrator(items, vi.fn(), workspaceWith("orchestrator"));
		expect(screen.getByTestId("worker-update")).toHaveTextContent("waiting on orchestrator");
		expect(screen.getByTestId("worker-update")).not.toHaveTextContent("needs you");
		cleanup();

		renderOrchestrator(items, vi.fn(), workspaceWith("human"));
		expect(screen.getByTestId("worker-update")).toHaveTextContent("needs you");
		cleanup();

		const answered = workspaceWith("human");
		delete (answered[0]!.sessions[0] as { needsInputAudience?: string }).needsInputAudience;
		renderOrchestrator(items, vi.fn(), answered);
		expect(screen.getByTestId("worker-update")).toHaveTextContent("answered");
	});

	it("draws an older worker message that names its sender only in its prefix as a row titled by the session", () => {
		const workspaces = [{
			id: "proj", name: "proj", path: "/tmp/proj",
			sessions: [{ id: "proj-187", workspaceId: "proj", title: "PR 5051 rebase", provider: "codex", kind: "worker", status: "idle", prs: [] }],
		}] as unknown as WorkspaceSummary[];
		renderOrchestrator([
			automation("t1", 1, "[from proj-187] Rebased #5051; disk full during validation, retrying."),
			reply("t1", 2, "Noted."),
		], vi.fn(), workspaces);

		const row = screen.getByTestId("worker-update");
		expect(row).toHaveTextContent("PR 5051 rebase");
		expect(row).toHaveTextContent("Rebased #5051");
		expect(row).not.toHaveTextContent("[from");
		expect(within(row).getByRole("link", { name: "PR 5051 rebase" })).toHaveAttribute("href", "tallpine://sessions/proj/proj-187");
	});

	it("draws Tallpine's task-naming prompt as the name it produced", () => {
		renderOrchestrator([
			automation("t1", 1, "Tallpine TASK TITLE UPDATE\nChoose a concise task title from the brief and run:\nao session rename proj-150 \"<title>\""),
			command("t1", 1, 'tallpine session rename proj-150 "Chat Link Browser"'),
			reply("t1", 2, "Renamed the worker task to **Chat Link Browser**."),
		]);

		expect(screen.getByText("Chat Link Browser", { selector: "span" }).closest("p")).toHaveTextContent("New task named Chat Link Browser");
		expect(screen.queryByText(/Tallpine TASK TITLE UPDATE/)).not.toBeInTheDocument();
	});

	it("still shows the work of a turn that answered with tools only", () => {
		renderOrchestrator([
			report("t1", 1, "[done] [Menus](tallpine://sessions/proj/proj-285)\nMenus render above the browser"),
			command("t1", 1, "gh pr merge 7054"),
		]);

		expect(screen.getByTestId("worker-update")).toHaveTextContent("Menus");
		expect(screen.queryByTestId("orchestrator-reply")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: /^Worked for/ })).toBeInTheDocument();
	});
});

describe("spawned agent rows", () => {
	const spawn = (status: string, report?: string) => {
		const workspaces = [{
			id: "proj", name: "proj", path: "/tmp/proj",
			sessions: [{ id: "proj-29", workspaceId: "proj", title: "Erase stale titles", provider: "codex", kind: "worker", status, prs: [] }],
		}] as unknown as WorkspaceSummary[];
		const spawned = command("t1", 1, 'tallpine spawn "Erase stale titles"');
		(spawned.detail as { output: string }).output = 'spawned session proj-29 "Erase stale titles" (working)';
		renderOrchestrator([...(report ? [automation("t0", 0, `Reports since your previous turn:\n\n${report}`)] : []), spawned, reply("t1", 2, "Spawned it.")], vi.fn(), workspaces);
		return screen.getAllByText("Erase stale titles").at(-1)!.closest("div.pl-5") as HTMLElement;
	};

	it("shows Terminated with no spinner once the worker session is terminated", () => {
		const line = spawn("terminated");
		expect(line).toHaveTextContent("Terminated");
		expect(line.querySelector("svg.animate-spin, [data-spinner]")).toBeNull();
	});

	it("shows Terminated, not Needs input, when the last report asked for input but the worker is gone", () => {
		const line = spawn("terminated", "[needs_input] [Erase stale titles](tallpine://sessions/proj/proj-29)\nWhich locale?");
		expect(line).toHaveTextContent("Terminated");
		expect(line).not.toHaveTextContent("Needs input");
	});

	it("shows Ended, not a spinner, when the killed worker left the session list after review_approved", () => {
		const workspaces = [{ id: "proj", name: "proj", path: "/tmp/proj", sessions: [] }] as unknown as WorkspaceSummary[];
		const spawned = command("t1", 1, 'tallpine spawn "Erase stale titles"');
		(spawned.detail as { output: string }).output = 'spawned session proj-29 "Erase stale titles" (working)';
		renderOrchestrator([automation("t0", 0, "Reports since your previous turn:\n\n[auto] review_approved [Erase stale titles](tallpine://sessions/proj/proj-29)\nApproved"), spawned, reply("t1", 2, "Spawned it.")], vi.fn(), workspaces);
		const line = screen.getAllByText("proj-29").at(-1)!.closest("div.pl-5") as HTMLElement;
		expect(line).toHaveTextContent("Ended");
		expect(line.querySelector(".animate-spin, [data-spinner]")).toBeNull();
	});

	it("keeps the spinner for a worker spawned moments ago that is not in the session list yet", () => {
		const workspaces = [{ id: "proj", name: "proj", path: "/tmp/proj", sessions: [] }] as unknown as WorkspaceSummary[];
		const spawned = command("t1", 1, 'tallpine spawn "Erase stale titles"');
		(spawned.detail as { output: string }).output = 'spawned session proj-29 "Erase stale titles" (working)';
		(spawned as { createdAt: string }).createdAt = new Date().toISOString();
		renderOrchestrator([spawned, reply("t1", 2, "Spawned it.")], vi.fn(), workspaces);
		const line = screen.getAllByText("proj-29").at(-1)!.closest("div.pl-5") as HTMLElement;
		expect(line).not.toHaveTextContent("Ended");
	});

	it("shows Idle with no spinner once the worker went idle without a report", () => {
		expect(spawn("idle")).toHaveTextContent("Idle");
	});

	it("keeps the working state while the worker is working", () => {
		expect(spawn("working")).not.toHaveTextContent(/Idle|Terminated/);
	});
});

describe("agent messages in a worker chat", () => {
	const workspaces = [{
		id: "proj", name: "proj", path: "/tmp/proj",
		sessions: [
			{ id: "proj-1", workspaceId: "proj", title: "Orchestrator", provider: "codex", kind: "orchestrator", status: "working", prs: [] },
			{ id: "proj-7", workspaceId: "proj", title: "Sibling worker", provider: "codex", kind: "worker", status: "idle", prs: [] },
		],
	}] as unknown as WorkspaceSummary[];
	const sent = (text: string, sender: string): ConversationMessage => ({
		...automation("t1", 1, `[from ${sender}] ${text}`), senderSessionId: sender, senderProjectId: "proj",
	});

	it("draws the orchestrator's message under its name, linked to its session", () => {
		renderOrchestrator([sent("Keep the draft local until Dhruv approves.", "proj-1"), reply("t1", 2, "Kept it local.")], vi.fn(), workspaces, "worker");

		const message = screen.getByTestId("agent-message");
		expect(message).toHaveTextContent("Keep the draft local until Dhruv approves.");
		expect(message).not.toHaveTextContent("[from");
		expect(within(message).getByRole("link", { name: "Orchestrator" })).toHaveAttribute("href", "tallpine://sessions/proj/proj-1");
	});

	it("names a sibling worker by its task", () => {
		renderOrchestrator([sent("Rebased on main.", "proj-7"), reply("t1", 2, "Noted.")], vi.fn(), workspaces, "worker");

		const message = screen.getByTestId("agent-message");
		expect(message).toHaveTextContent("Rebased on main.");
		expect(within(message).getByRole("link", { name: "Sibling worker" })).toHaveAttribute("href", "tallpine://sessions/proj/proj-7");
	});

	it("leaves a message whose sender this chat cannot find as it was", () => {
		renderOrchestrator([sent("Rebased on main.", "proj-404"), reply("t1", 2, "Noted.")], vi.fn(), workspaces, "worker");

		expect(screen.queryByTestId("agent-message")).not.toBeInTheDocument();
		expect(screen.getByText("Rebased on main.")).toBeInTheDocument();
	});
});

describe("Tallpine's automation messages in the timeline", () => {
	it("draws a GitHub merge-conflict message as a PR row, not the bordered automation card", () => {
		const url = "https://github.com/o/r/pull/6974";
		renderOrchestrator([
			automation("t1", 1, `There are merge conflicts on PR #6974 ("feat: link this computer"). Rebase onto the base branch and resolve them.\nPR: ${url}`),
			reply("t1", 2, "On it."),
		], vi.fn(), [], "worker");

		const event = screen.getByTestId("pull-request-event");
		expect(event).toHaveTextContent("#6974");
		expect(within(event).getByRole("link", { name: "#6974" })).toHaveAttribute("href", url);
	});
});

describe("messages an outside tool relayed", () => {
	const ask = "Catch me up on what is done, what is blocked and what is next.";

	it("draws an automation message under the tool's name, without its prefix or tag", () => {
		renderOrchestrator([automation("t1", 1, `[via Grok Bot #191450] ${ask}`), reply("t1", 2, "Two PRs merged.")]);

		const message = screen.getByTestId("agent-message");
		expect(message).toHaveTextContent("Grok Bot");
		expect(message).toHaveTextContent(ask);
		expect(message).not.toHaveTextContent("[via");
		expect(message).not.toHaveTextContent("191450");
		expect(screen.queryByText("automation")).not.toBeInTheDocument();
		// The orchestrator's answer stays a full reply: the tool asked, not a worker.
		expect(screen.queryByTestId("orchestrator-reply")).not.toBeInTheDocument();
	});

	it("draws one Tallpine stored as the human's the same way, instead of as the user's bubble", () => {
		renderOrchestrator([{ ...automation("t1", 1, `[via Grok Bot] ${ask}`), origin: "human" }, reply("t1", 2, "Two PRs merged.")]);

		expect(screen.getByTestId("agent-message")).toHaveTextContent(`Grok Bot${ask}`);
	});

	it("draws a steer it sent into a running turn the same way", () => {
		renderOrchestrator([
			{ ...automation("t1", 0, "List the open PRs."), origin: "human" },
			command("t1", 1, "gh pr list"),
			{
				kind: "activity", id: "t1-steer", turnId: "t1", sequence: 12, revision: 0, activityKind: "system", status: "completed",
				summary: "[via Grok Bot #200733] Stop that.", detail: { event: "steer", text: "[via Grok Bot #200733] Stop that." }, createdAt: at(1),
			},
			reply("t1", 2, "Stopped."),
		]);

		expect(screen.getByTestId("agent-message")).toHaveTextContent("Grok BotStop that.");
	});

	it("keeps a message an Tallpine session sent as that session's, whatever its text says", () => {
		renderOrchestrator([{ ...automation("t1", 1, `[via Grok Bot] ${ask}`), senderSessionId: "proj-404" }, reply("t1", 2, "Ok.")], vi.fn(), [], "worker");

		expect(screen.queryByTestId("agent-message")).not.toBeInTheDocument();
	});
});
