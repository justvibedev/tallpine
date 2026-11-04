import { chatFixture, chatFixtureEmpty } from "../../lib/chat-fixture";
import type { ConversationMessage, ConversationSnapshot } from "../../types/conversation";
import { type WorkspaceSession } from "../../types/workspace";

export function poll(snapshot: ConversationSnapshot): ConversationSnapshot {
	return structuredClone(snapshot);
}


export function idleSnapshot(snapshot: ConversationSnapshot = chatFixture): ConversationSnapshot {
	return {
		...snapshot,
		controller: { state: "ready" },
		items: snapshot.items.filter(
			(item) =>
				!(
					item.kind === "activity" &&
					item.activityKind === "approval" &&
					item.status === "pending"
				),
		),
		turns: snapshot.turns.map((turn) =>
			turn.state === "running"
				? {
						...turn,
						state: "completed" as const,
						completedAt: turn.requestedAt,
					}
				: turn,
		),
	};
}

/** jsdom has no layout, so the scroller's geometry has to be stated. */

export function stubGeometry(
	node: HTMLElement,
	{
		scrollHeight,
		clientHeight,
		scrollTop,
	}: {
		scrollHeight: number;
		clientHeight: number;
		scrollTop: number;
	},
) {
	Object.defineProperty(node, "scrollHeight", {
		configurable: true,
		value: scrollHeight,
	});
	Object.defineProperty(node, "clientHeight", {
		configurable: true,
		value: clientHeight,
	});
	Object.defineProperty(node, "scrollTop", {
		configurable: true,
		writable: true,
		value: scrollTop,
	});
}


export function humanMessage(text: string): ConversationMessage {
	return {
		kind: "message",
		id: "message-with-attachment",
		sequence: 1,
		revision: 1,
		role: "user",
		origin: "human",
		text,
		streaming: false,
		createdAt: "2026-08-08T00:00:00Z",
	};
}

/** A session whose only turn is the opening brief. */

export function startingSnapshot(turnState: "queued" | "running"): ConversationSnapshot {
	return {
		...chatFixtureEmpty,
		controller: { state: turnState === "running" ? "busy" : "connecting" },
		items: [{ ...humanMessage("Fix clicking attachments"), id: "brief", turnId: "brief-turn" }],
		turns: [{
			id: "brief-turn",
			state: turnState,
			requestedAt: "2026-08-15T00:00:00Z",
			...(turnState === "running" ? { startedAt: "2026-08-15T00:00:07Z" } : {}),
		}],
		latestSequence: 1,
	};
}

/** Fetch and worktree done; the agent step in the given state. */

export function startingSteps(agent: "running" | "done"): NonNullable<WorkspaceSession["provisionSteps"]> {
	return [
		{ id: "fetch", status: "done", startedAt: "2026-08-15T00:00:00Z", endedAt: "2026-08-15T00:00:01.2Z" },
		{ id: "worktree", status: "done", startedAt: "2026-08-15T00:00:01.2Z", endedAt: "2026-08-15T00:00:03.6Z" },
		agent === "done"
			? { id: "agent", status: "done", startedAt: "2026-08-15T00:00:03.6Z", endedAt: "2026-08-15T00:00:06Z" }
			: { id: "agent", status: "running", startedAt: "2026-08-15T00:00:03.6Z" },
	];
}


export const chatSession = {
	id: chatFixture.sessionId,
	workspaceId: "project-1",
	workspaceName: "tallpine",
	title: "Reviewer chat",
	provider: "codex",
	kind: "worker",
	mode: "chat",
	status: "working",
	updatedAt: "2026-08-15T00:00:00Z",
	activity: { state: "active", lastActivityAt: "2026-08-15T00:00:00Z" },
	prs: [],
} satisfies WorkspaceSession;
