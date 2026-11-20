// The setup module registers vi.mock calls, so it must be imported before ChatWorkspace.
import { cleanupChatWorkspaceTest, resetChatWorkspaceMocks } from "./ChatWorkspace.test.setup";
import { screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { chatFixtureSettled } from "../../lib/chat-fixture";
import { chatDraftScopeKey } from "../../lib/chat-drafts";
import { renderChat as render } from "../../test/render-chat";
import type { ConversationActivity, ConversationItem, ConversationMessage, ConversationSnapshot } from "../../types/conversation";
import { chatLastSeen, lastSeenStorageKey, ownerReplies } from "./chat-last-seen";
import type { TimelineGroup } from "./timeline-groups";

const createdAt = "2026-11-20T10:00:00Z";
const message = (sequence: number, role: "user" | "assistant", text: string, turnId = "turn-a"): ConversationMessage =>
	({ kind: "message", id: `m${sequence}`, turnId, sequence, revision: 0, role, origin: role === "user" ? "human" : undefined, text, streaming: false, createdAt } as ConversationMessage);
const tool = (sequence: number, turnId = "turn-a"): ConversationActivity =>
	({ kind: "activity", id: `a${sequence}`, turnId, sequence, revision: 0, activityKind: "command", status: "completed", summary: "ls", createdAt } as ConversationActivity);

// One settled owner turn: the agent narrated three times between tool calls, then answered.
// The narration folds under "Worked for"; only the final answer is drawn in the open.
const narratedTurn: ConversationItem[] = [
	message(1, "user", "Check the build"),
	message(2, "assistant", "Looking at the build first."),
	tool(3),
	message(4, "assistant", "Now the tests."),
	tool(5),
	message(6, "assistant", "One more check."),
	tool(7),
	message(8, "assistant", "The build is green."),
];

function settledSnapshot(sessionId: string): ConversationSnapshot {
	return {
		...chatFixtureSettled,
		sessionId,
		latestSequence: 8,
		turns: [{ id: "turn-a", state: "completed", requestedAt: createdAt, startedAt: createdAt, completedAt: createdAt }],
		items: narratedTurn,
	};
}

beforeEach(resetChatWorkspaceMocks);
afterEach(async () => {
	window.localStorage.removeItem(lastSeenStorageKey);
	await cleanupChatWorkspaceTest();
});

describe("new replies pill (B845)", () => {
	it("counts only the replies the thread draws, not narration folded under Worked for", async () => {
		const sessionId = "unread-folded";
		// The reader last saw the conversation before this turn's replies arrived.
		chatLastSeen.set(chatDraftScopeKey(sessionId), 1);
		render(<ChatWorkspace snapshot={settledSnapshot(sessionId)} />);

		expect(await screen.findByText("The build is green.")).toBeVisible();
		expect(screen.queryByText("Now the tests.")).toBeNull();
		await waitFor(() => expect(screen.getByRole("button", { name: /new repl/ })).toHaveTextContent("1 new reply"));
		expect(screen.queryByRole("button", { name: /4 new replies/ })).toBeNull();
	});
});

describe("ownerReplies", () => {
	const group = (items: ConversationItem[], extra: Partial<TimelineGroup> = {}): TimelineGroup =>
		({ key: "t", anchor: items[0]!.sequence, items, ...extra });

	it("counts every reply of a running turn, since all of them are drawn", () => {
		expect(ownerReplies([group(narratedTurn, { live: true })]).map((reply) => reply.sequence)).toEqual([2, 4, 6, 8]);
	});

	it("counts only the final reply of a settled turn", () => {
		expect(ownerReplies([group(narratedTurn, { outcome: { state: "completed" } as TimelineGroup["outcome"] })])).toEqual([{ sequence: 8, index: 0 }]);
	});

	it("keeps a substantial early answer in the orchestrator chat, where the thread draws it", () => {
		const essay = message(4, "assistant", "x".repeat(400));
		const items = [...narratedTurn.slice(0, 3), essay, ...narratedTurn.slice(4)];
		const settled = group(items, { outcome: { state: "completed" } as TimelineGroup["outcome"] });
		expect(ownerReplies([settled], true).map((reply) => reply.sequence)).toEqual([4, 8]);
		expect(ownerReplies([settled], false).map((reply) => reply.sequence)).toEqual([8]);
	});
});
