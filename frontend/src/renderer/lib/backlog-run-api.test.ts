import { beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "./api-client";
import { controlBacklogRun, getLatestBacklogRun, startBacklogRun } from "./backlog-run-api";

vi.mock("./api-client", () => ({
	apiClient: { GET: vi.fn(), POST: vi.fn() },
	apiErrorMessage: (error: unknown) => (error as { message?: string }).message ?? "error",
}));

const post = vi.mocked(apiClient.POST) as unknown as ReturnType<typeof vi.fn>;
const get = vi.mocked(apiClient.GET) as unknown as ReturnType<typeof vi.fn>;
const run = { ref: "R1", items: [] };

beforeEach(() => {
	post.mockReset().mockResolvedValue({ data: { run } });
	get.mockReset();
});

describe("backlog run api", () => {
	it("builds the start body for each selection", async () => {
		await startBacklogRun("p1", "pr", { kind: "all" });
		await startBacklogRun("p1", "local", { kind: "refs", refs: ["B1", "B2"] });
		await startBacklogRun("p1", "pr-merge", { kind: "parent", ref: "B30" });
		expect(post.mock.calls.map((call) => call[1].body)).toEqual([
			{ scope: "pr", selection: "all" },
			{ scope: "local", selection: "refs", refs: ["B1", "B2"] },
			{ scope: "pr-merge", selection: "parent", refs: ["B30"] },
		]);
		expect(post.mock.calls[0][0]).toBe("/api/v1/projects/{projectId}/backlog-runs");
	});

	it("posts each control to its own route", async () => {
		await controlBacklogRun("p1", "R1", "pause");
		await controlBacklogRun("p1", "R1", "resume");
		await controlBacklogRun("p1", "R1", "stop");
		expect(post.mock.calls.map((call) => call[0])).toEqual([
			"/api/v1/projects/{projectId}/backlog-runs/{runRef}/pause",
			"/api/v1/projects/{projectId}/backlog-runs/{runRef}/resume",
			"/api/v1/projects/{projectId}/backlog-runs/{runRef}/stop",
		]);
	});

	it("reads the newest run with its plan, and nothing when there is none", async () => {
		get.mockResolvedValueOnce({ data: { runs: [{ ref: "R3" }] } }).mockResolvedValueOnce({ data: { run } });
		expect(await getLatestBacklogRun("p1")).toBe(run);
		expect(get.mock.calls[1][1].params.path).toEqual({ projectId: "p1", runRef: "R3" });
		get.mockReset().mockResolvedValueOnce({ data: { runs: [] } });
		expect(await getLatestBacklogRun("p1")).toBeUndefined();
	});

	it("surfaces the daemon's message on failure", async () => {
		post.mockResolvedValue({ error: { message: "The project already has an open run" } });
		await expect(startBacklogRun("p1", "pr", { kind: "all" })).rejects.toThrow("already has an open run");
	});
});
