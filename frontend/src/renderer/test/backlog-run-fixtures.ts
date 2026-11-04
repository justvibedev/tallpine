import { vi } from "vitest";
import type { BacklogRun, BacklogRunItem } from "../lib/backlog-run-api";

/** Mock of lib/backlog-run-api's network functions; the keys and types stay real. */
export const runApi = {
	getLatestBacklogRun: vi.fn(),
	startBacklogRun: vi.fn(),
	controlBacklogRun: vi.fn(),
	getBacklogItemUsage: vi.fn(),
};

/** What useSettings reports for the daemon's TALLPINE_BACKLOG_RUN gate. */
export const runSwitch = { enabled: true };

export function runItem(overrides: Partial<BacklogRunItem> = {}): BacklogRunItem {
	return {
		ref: "B1",
		itemId: "item-b1",
		title: "First",
		lane: 0,
		position: 0,
		model: "sonnet",
		status: "queued",
		displayStatus: "queued",
		attempts: 0,
		updatedAt: "2026-10-28T10:00:00Z",
		usage: { costComplete: true },
		...overrides,
	};
}

export function backlogRun(overrides: Partial<BacklogRun> = {}): BacklogRun {
	return {
		id: "run-1",
		ref: "R1",
		projectId: "proj-1",
		scope: "pr",
		selection: "all",
		refs: [],
		status: "running",
		requestedBy: "human",
		createdAt: "2026-10-28T10:00:00Z",
		updatedAt: "2026-10-28T10:00:00Z",
		items: [],
		counts: {},
		usage: { costComplete: true },
		...overrides,
	};
}

export function resetRunApi(run: BacklogRun | undefined) {
	for (const fn of Object.values(runApi)) fn.mockReset();
	runSwitch.enabled = true;
	runApi.getLatestBacklogRun.mockResolvedValue(run);
	runApi.startBacklogRun.mockResolvedValue(run ?? backlogRun());
	runApi.controlBacklogRun.mockResolvedValue(run ?? backlogRun());
	runApi.getBacklogItemUsage.mockResolvedValue({ costComplete: true });
}
