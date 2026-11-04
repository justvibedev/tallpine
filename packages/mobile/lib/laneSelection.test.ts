import { describe, expect, it, vi } from "vitest";
import type { OrchestratorLink } from "./api";
import { hostedProjectSections } from "./hostedRows";
import { laneSelectedFor, laneSelectionKey, laneSpawnTarget, pickLane, resolveSpawnTarget } from "./laneSelection";
import { orchestratorProjectSections } from "./orchestratorView";


const link = (id: string, over: Partial<OrchestratorLink> = {}): OrchestratorLink => ({ id, projectId: "p", projectName: "P", mode: "chat", ...over });
const def = link("def");
const laneA = link("a", { lane: "B7" });
const stopped = link("s", { lane: "B8", hasRuntime: false, isTerminal: true });

describe("pickLane", () => {
	it("returns the remembered lane, else the first, never a gone one", () => {
		expect(pickLane([def, laneA], "a")?.id).toBe("a");
		expect(pickLane([def, laneA], "gone")?.id).toBe("def");
		expect(pickLane([def, laneA])?.id).toBe("def");
		expect(pickLane([], "a")).toBeNull();
	});
});

describe("laneSpawnTarget", () => {
	it("parents to a running lane, delegates for default, and flags a stopped lane instead of falling back", () => {
		const all = [def, laneA, stopped, link("x", { projectId: "q", lane: "B1" })];
		expect(laneSpawnTarget(all, "p", "a")).toEqual({ kind: "lane", id: "a" });
		expect(laneSpawnTarget(all, "p", "s")).toEqual({ kind: "stopped", id: "s", lane: "B8" });
		for (const picked of ["def", "x", undefined]) expect(laneSpawnTarget(all, "p", picked)).toEqual({ kind: "default" });
	});
});

describe("resolveSpawnTarget", () => {
	const all = [def, stopped];
	it("parents a retry after Start lane to the returned orchestrator even though the stored selection is the stopped predecessor", () => {
		expect(resolveSpawnTarget(all, "p", "s", { laneParentId: "fresh" })).toEqual({ kind: "lane", id: "fresh" });
		expect(resolveSpawnTarget(all, "p", "s")).toMatchObject({ kind: "stopped" });
	});
	it("sends to the default lane only on an explicit choice", () => {
		expect(resolveSpawnTarget(all, "p", "s", { useDefaultLane: true })).toEqual({ kind: "default" });
	});
});

describe("selection feeds the project rows", () => {
	const project = { id: "p", name: "P" };
	it("makes the picked lane the row's link, keeping every lane", () => {
		const row = orchestratorProjectSections([project], [], [def, laneA], new Map([["p", "a"]])).flatMap((s) => s.data)[0];
		expect(row.link?.id).toBe("a");
		expect(row.lanes.map((l) => l.id)).toEqual(["def", "a"]);
	});
	it("scopes the choice to its computer", () => {
		const selection = new Map([[laneSelectionKey("h1", "p"), "a"]]);
		const host = (hostId: string) => ({ hostId, name: hostId, projects: [project], sessions: [], orchestrators: [def, laneA] });
		const rows = hostedProjectSections([host("h1"), host("h2")], selection).flatMap((s) => s.data);
		expect(rows.map((r) => r.link?.id)).toEqual(["a", "def"]);
		expect(laneSelectedFor(selection, "h1", "p").get("p")).toBe("a");
		expect(laneSelectedFor(selection, "h2", "p").size).toBe(0);
	});
});
