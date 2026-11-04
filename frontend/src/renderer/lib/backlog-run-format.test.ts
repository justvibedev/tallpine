import { describe, expect, it } from "vitest";
import { formatRunCost, formatRunElapsed, runLanes } from "./backlog-run-format";
import { runItem } from "../test/backlog-run-fixtures";

describe("backlog run formatting", () => {
	it("formats cost in dollars and marks a partial total", () => {
		expect(formatRunCost(1_250_000_000, true)).toBe("$1.25");
		expect(formatRunCost(1_250_000_000, false)).toBe("$1.25+");
		expect(formatRunCost(1_000, true)).toBe("<$0.01");
		expect(formatRunCost(undefined, true)).toBeUndefined();
	});

	it("formats elapsed wall-clock time", () => {
		expect(formatRunElapsed(20)).toBe("<1m");
		expect(formatRunElapsed(7 * 60)).toBe("7m");
		expect(formatRunElapsed(2 * 3600 + 5 * 60)).toBe("2h 5m");
		expect(formatRunElapsed(null)).toBeUndefined();
	});

	it("groups the plan by lane in order", () => {
		const lanes = runLanes([runItem({ ref: "B3", lane: 1, position: 0 }), runItem({ ref: "B2", lane: 0, position: 1 }), runItem({ ref: "B1", lane: 0, position: 0 })]);
		expect(lanes.map((lane) => lane.map((item) => item.ref))).toEqual([["B1", "B2"], ["B3"]]);
	});
});
