import { describe, expect, it } from "vitest";
import { toSnapshot } from "./conversation-reducer";

describe("toSnapshot batched delivery (B812)", () => {
	it("maps the turn a queued message was batched into", () => {
		const snapshot = toSnapshot({
			conversationId: "conv-1", sessionId: "tallpine-1", mode: "chat", controller: "ready", latestSequence: 1,
			continueTurnId: "",
			branchedFromEarlierMessage: false, hasMoreBefore: false, nativeForkAvailableAfterSequence: 0,
			settings: {}, messages: [], activities: [],
			turns: [
				{ id: "carrier", requestedAt: "2026-11-17T21:11:01Z", state: "running" },
				{ id: "follower", requestedAt: "2026-11-17T21:11:21Z", state: "completed", deliveredInTurnId: "carrier" },
			],
		});
		expect(snapshot.turns[0]?.deliveredInTurnId).toBeUndefined();
		expect(snapshot.turns[1]?.deliveredInTurnId).toBe("carrier");
	});
});
