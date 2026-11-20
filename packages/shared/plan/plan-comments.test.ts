import { describe, expect, it } from "vitest";
import { commentBody, pendingComments, planCommentState } from "./plan-comments";

describe("plan comments", () => {
	it("tells a plan with no pending comment from one the planner is answering or one held on approval", () => {
		expect(planCommentState({})).toBe("none");
		expect(planCommentState({ comments: [{ outcome: "accepted", approve: true }] })).toBe("none");
		expect(planCommentState({ comments: [{ outcome: "pending" }] })).toBe("considering");
		expect(planCommentState({ comments: [{ outcome: "revised", approve: true }, { outcome: "pending", approve: true }] })).toBe("held");
		expect(pendingComments({ comments: [{ outcome: "pending" }, { outcome: "accepted" }] })).toHaveLength(1);
	});

	it("sends a comment only when it has text", () => {
		expect(commentBody("  ")).toEqual({});
		expect(commentBody(undefined)).toEqual({});
		expect(commentBody(" keep it ")).toEqual({ comment: "keep it" });
	});
});
