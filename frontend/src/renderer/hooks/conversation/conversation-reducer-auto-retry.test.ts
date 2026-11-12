import { describe, expect, it } from "vitest";
import { toSnapshot } from "./conversation-reducer";

describe("toSnapshot automatic network retry", () => {
	it("maps a pending automatic network retry from the daemon", () => {
		const autoRetry = { attempt: 2, maxAttempts: 5, nextAt: "2026-11-12T10:00:00Z", offline: true };
		const snapshot = toSnapshot({
			conversationId: "conv-1", sessionId: "tallpine-1", mode: "chat", controller: "ready", latestSequence: 1,
			continueTurnId: "",
			branchedFromEarlierMessage: false, hasMoreBefore: false, nativeForkAvailableAfterSequence: 0,
			settings: {}, messages: [], activities: [],
			turns: [{ id: "dropped", requestedAt: "2026-11-12T09:59:00Z", state: "failed", transientError: true, autoRetry }],
		});
		expect(snapshot.turns[0]?.autoRetry).toEqual(autoRetry);
		expect(snapshot.turns[0]?.transientError).toBe(true);
	});

	it("leaves both absent for an ordinary failure", () => {
		const snapshot = toSnapshot({
			conversationId: "conv-1", sessionId: "tallpine-1", mode: "chat", controller: "ready", latestSequence: 1,
			continueTurnId: "",
			branchedFromEarlierMessage: false, hasMoreBefore: false, nativeForkAvailableAfterSequence: 0,
			settings: {}, messages: [], activities: [],
			turns: [{ id: "failed", requestedAt: "2026-11-12T09:59:00Z", state: "failed", errorMessage: "boom" }],
		});
		expect(snapshot.turns[0]?.autoRetry).toBeUndefined();
		expect(snapshot.turns[0]?.transientError).toBeUndefined();
	});
});
