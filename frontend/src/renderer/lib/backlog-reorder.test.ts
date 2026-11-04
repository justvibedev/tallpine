import { describe, expect, it } from "vitest";
import { backlogItem as item } from "../test/backlog-fixtures";
import { applyMove, reorderTarget, stepTarget } from "./backlog-reorder";

const list = [item({ ref: "B1" }), item({ ref: "B2" }), item({ ref: "B3" })];

describe("reorderTarget", () => {
	it("lands after the item now at the destination when moving down", () => {
		expect(reorderTarget(list, 0, 2)).toEqual({ after: "B3" });
	});
	it("lands before the item now at the destination when moving up", () => {
		expect(reorderTarget(list, 2, 0)).toEqual({ before: "B1" });
	});
	it("ignores a drop in place or out of range", () => {
		expect(reorderTarget(list, 1, 1)).toBeUndefined();
		expect(reorderTarget(list, 0, 5)).toBeUndefined();
		expect(reorderTarget(list, -1, 0)).toBeUndefined();
	});
});

describe("stepTarget", () => {
	it("moves one place with the keyboard and stops at the ends", () => {
		expect(stepTarget(list, "B2", "down")).toEqual({ after: "B3" });
		expect(stepTarget(list, "B2", "up")).toEqual({ before: "B1" });
		expect(stepTarget(list, "B1", "up")).toBeUndefined();
		expect(stepTarget(list, "B3", "down")).toBeUndefined();
		expect(stepTarget(list, "B9", "down")).toBeUndefined();
	});
});

describe("applyMove", () => {
	it("returns the list as it reads after the move", () => {
		expect(applyMove(["a", "b", "c"], 0, 2)).toEqual(["b", "c", "a"]);
	});
});
