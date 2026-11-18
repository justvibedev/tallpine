import { describe, expect, it } from "vitest";
import type {
	ConversationActivity,
	ConversationItem,
	ConversationMessage,
	ConversationSnapshot,
	ConversationTurn,
} from "../../types/conversation";
import { groupByTurn } from "./timeline-groups";

const T = (clock: string) => `2026-11-17T21:${clock}Z`;

function message(sequence: number, turnId: string | undefined, role: "user" | "assistant", clock: string, origin: ConversationMessage["origin"] = role === "user" ? "human" : "provider"): ConversationMessage {
	return { kind: "message", id: `m${sequence}`, turnId, sequence, revision: 0, role, origin, text: `message ${sequence}`, streaming: false, createdAt: T(clock) };
}

function activity(sequence: number, turnId: string | undefined, clock: string, overrides: Partial<ConversationActivity> = {}): ConversationActivity {
	return { kind: "activity", id: `a${sequence}`, turnId, sequence, revision: 0, activityKind: "command", status: "completed", summary: `command ${sequence}`, createdAt: T(clock), ...overrides };
}

function action(sequence: number, clock: string, status: ConversationActivity["status"] = "completed"): ConversationActivity {
	return activity(sequence, undefined, clock, { activityKind: "ao_action", status, summary: "Spawned an agent", detail: { action: "session.spawned" } });
}

function snapshot(items: ConversationItem[], turns: ConversationTurn[]): ConversationSnapshot {
	return {
		conversationId: "c1",
		sessionId: "agent-orchestrator-1288",
		harness: "claude-code",
		mode: "chat",
		controller: { state: "ready" },
		turns,
		items,
		latestSequence: items.at(-1)?.sequence ?? 0,
		oldestSequence: items[0]?.sequence ?? 1,
		hasMoreBefore: false,
		settings: {},
	};
}

const groupIds = (groups: ReturnType<typeof groupByTurn>) =>
	groups.map((group) => [group.turnId ?? "loose", group.items.map((item) => item.sequence)]);

// The rows of orchestrator agent-orchestrator-1288 around 21:12Z on 2026-11-17
// (B812), when the owner took the screenshot: turn "prev" was answering while the
// owner queued "carrier"'s message and then a second one ("follower") that the
// daemon batched into "carrier" when it started. "carrier" then spawned agents
// (actions the daemon records with no turn) and auto-compacted mid-turn, while a
// local-CI relay ("relay") waited in the queue.
const turns: ConversationTurn[] = [
	{ id: "prev", state: "completed", requestedAt: T("10:37"), startedAt: T("10:37"), completedAt: T("11:25") },
	{ id: "carrier", state: "running", requestedAt: T("11:01"), startedAt: T("11:25") },
	{ id: "follower", state: "completed", requestedAt: T("11:21"), completedAt: T("11:25"), deliveredInTurnId: "carrier" },
	{ id: "relay", state: "queued", requestedAt: T("11:30") },
];
const items: ConversationItem[] = [
	message(41558, "prev", "user", "10:37", "automation"),
	message(41559, "prev", "assistant", "10:37"),
	message(41568, "carrier", "user", "11:01"),
	message(41569, "prev", "assistant", "11:07"),
	activity(41570, "prev", "11:07"),
	activity(41571, undefined, "11:13", { activityKind: "ao_action", summary: "Terminated an agent", detail: { action: "session.terminated" } }),
	message(41574, "follower", "user", "11:21"),
	message(41575, "prev", "assistant", "11:24"),
	message(41577, "relay", "user", "11:30", "automation"),
	message(41578, "carrier", "assistant", "11:31"),
	activity(41583, "carrier", "11:54"),
	action(41584, "11:57", "failed"),
	message(41585, "carrier", "assistant", "11:59"),
	activity(41586, "carrier", "11:59"),
	action(41587, "12:07", "failed"),
	message(41588, "carrier", "assistant", "12:08"),
	action(41590, "12:18"),
	activity(41591, "carrier", "12:19", { activityKind: "reasoning", status: "running", summary: "Compact conversation" }),
];

describe("groupByTurn across a batched prompt and a mid-turn compaction (B812)", () => {
	it("renders the batched owner message above the reply that answers it", () => {
		const groups = groupByTurn(snapshot(items, turns), false);
		expect(groupIds(groups)).toEqual([
			["prev", [41558, 41559, 41569, 41570, 41571, 41575]],
			["carrier", [41568, 41574, 41578, 41583, 41584, 41585, 41586, 41587, 41588, 41590, 41591]],
			["relay", [41577]],
		]);
		// The batched turn has no block of its own, so no empty "Worked" outcome either.
		expect(groups.find((group) => group.turnId === "follower")).toBeUndefined();
		expect(groups.find((group) => group.turnId === "carrier")?.live).toBe(true);
	});

	it("keeps a queued follower in its own block until a turn takes it", () => {
		const queued = turns.map((turn) => (turn.id === "follower" ? { id: "follower", state: "queued" as const, requestedAt: T("11:21") } : turn));
		const groups = groupByTurn(snapshot(items, queued), false);
		expect(groupIds(groups).map(([turnId]) => turnId)).toEqual(["prev", "carrier", "follower", "relay"]);
	});

	it("leaves a Tallpine action loose when no turn was running", () => {
		const idle = [
			message(1, "t1", "user", "00:00"),
			message(2, "t1", "assistant", "00:05"),
			action(3, "05:00"),
		];
		const groups = groupByTurn(
			snapshot(idle, [{ id: "t1", state: "completed", requestedAt: T("00:00"), startedAt: T("00:00"), completedAt: T("00:10") }]),
			false,
		);
		expect(groupIds(groups)).toEqual([
			["t1", [1, 2]],
			["loose", [3]],
		]);
	});

	it("does not merge two loose items that another turn's rows separate", () => {
		const relays = [
			message(1, "t1", "user", "00:00"),
			message(2, undefined, "user", "00:01", "automation"),
			message(3, "t1", "assistant", "00:02"),
			message(4, undefined, "user", "00:03", "automation"),
			message(5, undefined, "user", "00:04", "automation"),
		];
		const groups = groupByTurn(
			snapshot(relays, [{ id: "t1", state: "completed", requestedAt: T("00:00"), startedAt: T("00:00"), completedAt: T("00:02") }]),
			false,
		);
		expect(groupIds(groups)).toEqual([
			["t1", [1, 3]],
			["loose", [2]],
			["loose", [4, 5]],
		]);
	});
});
