import { describe, expect, it } from "vitest";
import { boardSessionNeedsAttention } from "./board-attention";

describe("boardSessionNeedsAttention", () => {
	it.each([undefined, "Blocked"])("ignores %s cards waiting on the orchestrator", (displayStatus) => {
		expect(
			boardSessionNeedsAttention({ displayStatus, needsInputAudience: "orchestrator", status: "needs_input" }),
		).toBe(false);
	});

	it("flags human-owned needs_input and CI failures", () => {
		expect(boardSessionNeedsAttention({ needsInputAudience: "human", status: "needs_input" })).toBe(true);
		expect(boardSessionNeedsAttention({ displayStatus: "CI failing", status: "idle" })).toBe(true);
	});
});
