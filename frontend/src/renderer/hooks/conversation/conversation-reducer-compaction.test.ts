import { describe, expect, it } from "vitest";
import { toSnapshot } from "./conversation-reducer";

const wire = (settings: Record<string, unknown>) => toSnapshot({
	conversationId: "conv-1", sessionId: "tallpine-1", mode: "chat", controller: "ready", latestSequence: 1,
	continueTurnId: "",
	branchedFromEarlierMessage: false, hasMoreBefore: false, nativeForkAvailableAfterSequence: 0,
	settings, messages: [], activities: [], turns: [],
});

describe("toSnapshot compaction readout", () => {
	it("maps the daemon's launch baseline and trigger (B735)", () => {
		const { settings } = wire({ compactThreshold: 151_000, autoCompactBaseline: 51_000, autoCompactTrigger: 118_000 });
		expect(settings).toMatchObject({ compactThreshold: 151_000, autoCompactBaseline: 51_000, autoCompactTrigger: 118_000 });
	});

	it("leaves them unset when the daemon reports none", () => {
		const { settings } = wire({});
		expect(settings.autoCompactBaseline).toBeUndefined();
		expect(settings.autoCompactTrigger).toBeUndefined();
	});
});
