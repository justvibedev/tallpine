import { describe, expect, it } from "vitest";
import { ALL_WORKER_LANES, DEFAULT_WORKER_LANE, effectiveWorkerLane, filterWorkersByLane, workerLaneChips } from "./worker-lane-filter";

const workers = [{ id: "a", workerLane: "B12" }, { id: "b", workerLane: "B3" }, { id: "c" }, { id: "d", workerLane: "B12" }];

describe("worker lane filter", () => {
	it("offers one chip per lane, natural-sorted, plus Default for un-laned workers", () => {
		expect(workerLaneChips(workers)).toEqual([
			{ id: DEFAULT_WORKER_LANE, label: "Default" },
			{ id: "B3", label: "B3" },
			{ id: "B12", label: "B12" },
		]);
	});
	it("offers nothing when no worker is laned", () => {
		expect(workerLaneChips([{}, {}])).toEqual([]);
	});
	it("filters by lane, by default lane, and keeps all", () => {
		expect(filterWorkersByLane(workers, "B12").map((w) => w.id)).toEqual(["a", "d"]);
		expect(filterWorkersByLane(workers, DEFAULT_WORKER_LANE).map((w) => w.id)).toEqual(["c"]);
		expect(filterWorkersByLane(workers, ALL_WORKER_LANES)).toHaveLength(4);
	});
	it("falls back to All when the lane has no chip any more", () => {
		const chips = workerLaneChips(workers);
		expect(effectiveWorkerLane(chips, "B3")).toBe("B3");
		expect(effectiveWorkerLane(chips, "B99")).toBe(ALL_WORKER_LANES);
	});
	it("hides a lane whose workers are all ended, with no lone Default chip", () => {
		expect(workerLaneChips([{ workerLane: "B108", isTerminated: true }, { workerLane: "B108", isTerminated: true }, {}])).toEqual([]);
	});
	it("shows a lane that has an active worker", () => {
		expect(workerLaneChips([{ workerLane: "B108", isTerminated: true }, { workerLane: "B12" }, {}]).map((c) => c.id)).toEqual([DEFAULT_WORKER_LANE, "B12"]);
	});
});
