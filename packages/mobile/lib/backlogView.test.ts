import { describe, expect, it } from "vitest";
import { backlogCanDelegate, backlogDelegateRequest, backlogProgress } from "./backlogView";

describe("backlogDelegateRequest", () => {
	it("sends the ref so the daemon links and validates the item", () => {
		expect(backlogDelegateRequest("p1", { ref: "B12", title: "Fix it" })).toEqual({ projectId: "p1", brief: "B12: Fix it", mode: "chat", backlogRef: "B12" });
	});
});

describe("backlogCanDelegate", () => {
	it("is only for open, unheld items", () => {
		expect(backlogCanDelegate({ state: "open", hold: "", childCount: 0 })).toBe(true);
		expect(backlogCanDelegate({ state: "open", hold: "", childCount: 2 })).toBe(false);
		expect(backlogCanDelegate({ state: "open", hold: "waiting on legal", childCount: 0 })).toBe(false);
		expect(backlogCanDelegate({ state: "done", hold: "", childCount: 0 })).toBe(false);
	});
});

describe("backlogProgress", () => {
	it("shows child progress only for parents", () => {
		expect(backlogProgress({ childCount: 3, childClosedCount: 1 })).toBe("1/3 done");
		expect(backlogProgress({ childCount: 0, childClosedCount: 0 })).toBeUndefined();
	});
});
