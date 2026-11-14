import { describe, expect, it } from "vitest";
import { mapSession } from "./api/normalize";
import { askLaneOf } from "./orchestratorView";

describe("askLaneOf", () => {
	it("reads the lane of a lane orchestrator that arrived as a mapped DashboardSession", () => {
		const wire = { id: "o1", projectId: "p1", kind: "orchestrator", lane: "B7", status: "working" } as never;
		expect(askLaneOf(mapSession(wire))).toBe("B7");
	});
});
