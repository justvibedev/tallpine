import { describe, expect, it } from "vitest";
import { activityRuns, deliveryCopy, elapsed, fileChanges, formatTokens, lastLine, printable, reviewPaths, truncationNote } from "./format";

const act = (sequence: number, turnId: string, activityKind: string, extra: object = {}) =>
	({ kind: "activity", id: `a${sequence}`, sequence, turnId, activityKind, ...extra }) as never;

describe("timeline format helpers", () => {
	it("groups consecutive mechanics of one turn into a run and keeps prose/approvals single", () => {
		const rows = activityRuns([
			act(1, "t1", "command"),
			act(2, "t1", "mcp_tool"),
			act(3, "t1", "approval"),
			act(4, "t1", "command"),
			act(5, "t2", "command"),
			{ kind: "message", id: "m", sequence: 6, turnId: "t2" } as never,
		]);
		expect(rows.map((r) => [r.kind, r.key, r.items.length])).toEqual([
			["activities", "run-1", 2],
			["single", "activity:a3", 1],
			["activities", "run-4", 1],
			["activities", "run-5", 1],
			["single", "message:m", 1],
		]);
	});

	it("never folds file_change, reasoning, error or event rows into a run", () => {
		for (const kind of ["file_change", "reasoning", "error"]) expect(activityRuns([act(1, "t", kind)])[0].kind).toBe("single");
		expect(activityRuns([act(1, "t", "command", { detail: { event: "x" } })])[0].kind).toBe("single");
	});

	it("formats delivery, tokens, elapsed, last line and printable values", () => {
		expect(deliveryCopy("queued")).toMatch(/^Queued/);
		expect(deliveryCopy("failed")).toBe("Not sent");
		expect(deliveryCopy("sent")).toBeUndefined();
		expect(formatTokens(999)).toBe("999");
		expect(formatTokens(1500)).toBe("1.5k");
		expect(formatTokens(12_000)).toBe("12k");
		expect(elapsed(undefined, "2026-01-01T00:00:01Z")).toBeUndefined();
		expect(lastLine("a\nb\n")).toBe("b");
		expect(printable(undefined)).toBe("");
		expect(printable({ a: 1 })).toBe('{\n  "a": 1\n}');
	});

	it("parses file changes, review paths and truncation notes defensively", () => {
		expect(fileChanges("nope")).toEqual([]);
		expect(fileChanges([{ path: "a.ts", status: "weird" }, { nope: 1 }])[0]).toMatchObject({ path: "a.ts", status: "modified" });
		expect(reviewPaths(["x", { path: "y" }, 3])).toEqual(["x", "y"]);
		expect(truncationNote({ truncated: false })).toBeUndefined();
		expect(truncationNote({ truncated: true })).toMatch(/was larger than/);
	});
});
