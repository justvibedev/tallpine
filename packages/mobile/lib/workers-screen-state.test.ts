import { describe, expect, it } from "vitest";
import { workersScreenState } from "./workers-screen-state";

const base = { configured: true, hostCount: 1, loading: false, hasSessions: false, hostSelected: false, loadingExpired: false };

describe("Workers screen state", () => {
	it("fresh install with no computer shows the pairing prompt", () => {
		expect(workersScreenState({ ...base, configured: false, hostCount: 0 })).toBe("unpaired");
	});
	it("paired and loading shows a labelled loader, not a blank board", () => {
		expect(workersScreenState({ ...base, loading: true })).toBe("loading");
	});
	it("an offline or revoked computer that never answers stalls into an error with actions", () => {
		expect(workersScreenState({ ...base, loading: true, loadingExpired: true })).toBe("stalled");
	});
	it("keeps the last good board while a refresh is loading", () => {
		expect(workersScreenState({ ...base, loading: true, hasSessions: true, loadingExpired: true })).toBe("board");
	});
	it("a selected computer renders its own offline state through the board", () => {
		expect(workersScreenState({ ...base, loading: true, hostSelected: true })).toBe("board");
	});
	it("a loaded empty board stays a board (empty state lives in its list)", () => {
		expect(workersScreenState(base)).toBe("board");
	});
});
