import { describe, expect, it } from "vitest";
import type { OrchestratorLink } from "./api";
import { canEndLane, canHandoff, endLaneBody, parseLaneRef } from "./laneActions";

const link = (over: Partial<OrchestratorLink> = {}): OrchestratorLink => ({ id: "o", projectId: "p", projectName: "P", mode: "chat", ...over });

describe("laneActions", () => {
	it("parses a backlog ref and rejects anything else", () => {
		expect(parseLaneRef(" b12 ")).toBe("B12");
		for (const bad of ["", "12", "B", "B1x", "feature"]) expect(parseLaneRef(bad)).toBeNull();
	});
	it("ends only lane orchestrators and hands off only live ones", () => {
		expect(canEndLane(link())).toBe(false);
		expect(canEndLane(link({ lane: "B7" }))).toBe(true);
		expect(canHandoff(link())).toBe(true);
		expect(canHandoff(link({ hasRuntime: false, isTerminal: true }))).toBe(false);
		expect(canHandoff(null)).toBe(false);
	});
	it("states how many workers re-home", () => {
		expect(endLaneBody("B7", 1)).toContain("1 live worker moves to the default orchestrator and is told");
		expect(endLaneBody("B7", 0)).toContain("0 live workers move");
		expect(endLaneBody("B7", 3)).toContain("3 live workers move");
	});
});
