import { describe, expect, it } from "vitest";
import type { ConversationMessage } from "../../types/conversation";
import { latestReplyUnanswered } from "./orchestratorNeedsYou";

const msg = (o: Partial<ConversationMessage>): ConversationMessage =>
	({ kind: "message", id: `m${o.sequence}`, sequence: 1, revision: 0, role: "assistant", origin: "agent", text: "ok", streaming: false, createdAt: "2026-11-04T10:00:00Z", ...o }) as ConversationMessage;
const owner = (sequence: number) => msg({ sequence, role: "user", origin: "human", text: "go" });

describe("latestReplyUnanswered", () => {
	it("is true for a marked reply nobody has answered", () => {
		expect(latestReplyUnanswered([owner(1), msg({ sequence: 2, text: "**Needs you:** merge it" })])).toBe(true);
	});
	it("clears once the owner writes after it", () => {
		expect(latestReplyUnanswered([msg({ sequence: 1, text: "**Needs you:** merge it" }), owner(2)])).toBe(false);
	});
	it("ignores a continue and worker traffic as answers", () => {
		const items = [msg({ sequence: 1, text: "**Needs you:** merge" }), msg({ sequence: 2, role: "user", origin: "human", continuation: true }), msg({ sequence: 3, role: "user", origin: "automation", text: "[done]" })];
		expect(latestReplyUnanswered(items)).toBe(true);
	});
	it("is false when the newest reply is unmarked, even if an older one was marked", () => {
		expect(latestReplyUnanswered([msg({ sequence: 1, text: "**Needs you:** x" }), msg({ sequence: 2, text: "all done" })])).toBe(false);
	});
	it("is false for an empty timeline", () => {
		expect(latestReplyUnanswered([])).toBe(false);
	});
});
