import { describe, expect, it, vi } from "vitest";
import { orchestratorLinks } from "./api";
import { laneLabel, laneSpawnTarget } from "./laneSelection";
import { canHandoff } from "./laneActions";
import { orchestratorProjectSections } from "./orchestratorView";


const o = (id: string, over: Record<string, unknown> = {}) => ({ id, projectId: "p", kind: "orchestrator", ...over });
// The daemon marks the current orchestrator of each (project, lane); see service/session markCurrentOrchestrators.
const cur = (id: string, over: Record<string, unknown> = {}) => o(id, { currentOrchestrator: true, ...over });
const names = new Map([["p", "Proj"]]);

describe("orchestratorLinks", () => {
	it("keeps a live default and a live lane orchestrator of one project, default first", () => {
		const out = orchestratorLinks([cur("lane", { lane: "B7" }), cur("def")] as never, names);
		expect(out.map((l) => [l.id, l.lane])).toEqual([["def", undefined], ["lane", "B7"]]);
	});
	it("collapses respawns of the default orchestrator, preferring live, then newest", () => {
		expect(orchestratorLinks([o("old", { isTerminated: true }), cur("live")] as never, names).map((l) => l.id)).toEqual(["live"]);
		expect(orchestratorLinks([o("d1", { isTerminated: true }), cur("d2", { isTerminated: true })] as never, names).map((l) => l.id)).toEqual(["d2"]);
	});
	it("hides a terminated lane orchestrator and keeps a live one", () => {
		const out = orchestratorLinks([cur("def"), o("a", { lane: "B1", isTerminated: true }), cur("b", { lane: "B2" }), o("c", { lane: "B2", isTerminated: true })] as never, names);
		expect(out.map((l) => l.id)).toEqual(["def", "b"]);
	});
	it("shows every project orchestrator when an older daemon marks none", () => {
		const out = orchestratorLinks([o("old", { isTerminated: true }), o("live"), o("l", { lane: "B1" }), o("x", { lane: "B2", isTerminated: true })] as never, names);
		expect(out.map((l) => l.id).sort()).toEqual(["l", "live", "old"]);
	});
	it("a project whose only live orchestrator is the default has one link, so no lane rows", () => {
		const links = orchestratorLinks([cur("def"), o("b108", { lane: "B108", isTerminated: true })] as never, names);
		expect(links.map((l) => l.id)).toEqual(["def"]);
		const [section] = orchestratorProjectSections([{ id: "p", name: "Proj" } as never], [], links).flatMap((x) => x.data);
		expect(section.lanes).toHaveLength(1);
	});
});

describe("lane requests", () => {
	it("sends the lane when launching and the parent when spawning", async () => {
		const { launchOrchestrator, spawnSession } = await import("./api");
		const fetchMock = vi.fn(async () => new Response(JSON.stringify({ orchestrator: { id: "o" }, session: { id: "w" } }), { status: 200 }));
		vi.stubGlobal("fetch", fetchMock);
		const cfg = { host: "t.test", secure: true, httpPort: "3011", muxPort: "3011", password: "x" };
		await launchOrchestrator(cfg, "p", false, "chat", "B7");
		await spawnSession(cfg, { projectId: "p", prompt: "go", parentSessionId: "lane-orch" });
		const bodies = (fetchMock.mock.calls as unknown as [string, { body: string }][]).map((c) => JSON.parse(c[1].body));
		expect(bodies[0]).toMatchObject({ projectId: "p", lane: "B7" });
		expect(bodies[1]).toMatchObject({ projectId: "p", parentSessionId: "lane-orch" });
		vi.unstubAllGlobals();
	});
});

describe("end lane and handoff requests", () => {
	it("dry-runs the end, and posts a handoff to the orchestrator", async () => {
		const { endLane, handoffOrchestrator } = await import("./api");
		const fetchMock = vi.fn(async (url: string) =>
			new Response(JSON.stringify(String(url).endsWith("/end") ? { orchestrator: "def", workers: ["w1", "w2"] } : { orchestrator: { id: "next" } }), { status: 200 }),
		);
		vi.stubGlobal("fetch", fetchMock);
		const cfg = { host: "t.test", secure: true, httpPort: "3011", muxPort: "3011", password: "x" };
		const dry = await endLane(cfg, "lane-orch", { dryRun: true });
		const next = await handoffOrchestrator(cfg, "lane-orch");
		const calls = fetchMock.mock.calls as unknown as [string, { body: string }][];
		expect(calls[0][0]).toMatch(/\/api\/v1\/orchestrators\/end$/);
		expect(JSON.parse(calls[0][1].body)).toEqual({ sessionId: "lane-orch", dryRun: true });
		expect(dry.workers).toEqual(["w1", "w2"]);
		expect(calls[1][0]).toMatch(/\/orchestrators\/lane-orch\/handoff$/);
		expect(next).toBe("next");
		vi.unstubAllGlobals();
	});
});

describe("fresh orchestrator request", () => {
	it("posts clean:true so the daemon replaces the orchestrator with an empty one", async () => {
		const { launchOrchestrator } = await import("./api");
		const fetchMock = vi.fn(async () => new Response(JSON.stringify({ orchestrator: { id: "fresh" } }), { status: 200 }));
		vi.stubGlobal("fetch", fetchMock);
		const cfg = { host: "t.test", secure: true, httpPort: "3011", muxPort: "3011", password: "x" };
		const link = await launchOrchestrator(cfg, "p", true);
		const calls = fetchMock.mock.calls as unknown as [string, { body: string }][];
		expect(JSON.parse(calls[0][1].body)).toMatchObject({ projectId: "p", clean: true });
		expect(link.id).toBe("fresh");
		vi.unstubAllGlobals();
	});
	it("keeps every live default-lane orchestrator reachable (B191), oldest as main, extras labelled", () => {
		const links = orchestratorLinks([o("p-1", { isTerminated: true }), cur("p-233"), o("p-453"), cur("p-lane", { lane: "B7" })] as never, names);
		expect(links.map((l) => l.id)).toEqual(["p-233", "p-453", "p-lane"]);
		expect(links.map((l) => laneLabel(l))).toEqual(["Default", "Orchestrator 453", "B7"]);
		const [row] = orchestratorProjectSections([{ id: "p", name: "Proj" } as never], [], links, new Map([["p", "p-453"]])).flatMap((x) => x.data);
		expect(row.lanes).toHaveLength(3);
		expect(row.link?.id).toBe("p-453");
		expect(row.action).toBe("open");
	});
});

describe("extra default-lane orchestrator actions (B191)", () => {
	const links = orchestratorLinks([cur("p-233"), o("p-453")] as never, names);
	it("spawns workers under the selected extra, and hides Start fresh for it", () => {
		expect(laneSpawnTarget(links, "p", "p-453")).toEqual({ kind: "lane", id: "p-453" });
		expect(laneSpawnTarget(links, "p", "p-233")).toEqual({ kind: "default" });
		expect(canHandoff(links[1])).toBe(false);
		expect(canHandoff(links[0])).toBe(true);
	});
});
