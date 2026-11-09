import { toKanbanColumn } from "@tallpine/product-ui";
import type { WorkspaceSession } from "../../types/workspace";

export const GIB = 1024 ** 3;

export const FROZEN_NOW = new Date("2026-09-18T12:00:00.000Z");
export const LONG_AGO = new Date(FROZEN_NOW.getTime() - 2 * 60 * 60 * 1000).toISOString();

export function session(id: string, title: string, activityState = "idle", lastActivityAt = LONG_AGO): WorkspaceSession {
	return {
		id,
		title,
		workspaceId: "p1",
		workspaceName: "radic",
		provider: "claude-code",
		status: "idle",
		kanbanColumn: toKanbanColumn(undefined, "idle"),
		updatedAt: "2026-09-18T00:00:00Z",
		activity: { state: activityState as "idle", lastActivityAt },
		prs: [],
	};
}

export function reading(
	sessionId: string,
	rssBytes: number,
	processCount: number,
	processes: { pid: number; ppid: number; rssBytes: number; cpuPercent: number; command: string }[] = [],
	cpuPercent = 0,
) {
	return { sessionId, rssBytes, processCount, cpuPercent, sampledAt: "2026-09-18T00:00:00Z", processes };
}

/** A 32 GB host with the given amount free and PSI stall percentage. `load1`
 * is -1 on a platform with no load average (Windows), never otherwise negative. */
function host(availableGiB: number, pressureRaw = 0, cpuPercent = 0, load1 = 0.5) {
	return {
		cpuPercent,
		cpuMeasured: true,
		totalBytes: 32 * GIB,
		availableBytes: availableGiB * GIB,
		swapTotalBytes: 8 * GIB,
		swapUsedBytes: 0,
		swapBytesPerSec: 0,
		cpuCount: 8,
		load1,
		pressureRaw,
		pressureSource: "psi",
	};
}

/** Tallpine holding `tallpineGiB` on a host with `availableGiB` free at the given pressure. */
export function appReading(availableGiB: number, pressureRaw = 0, tallpineGiB = 2, hostCPUPercent = 0, tallpineCPUPercent = 12, load1 = 0.5) {
	return {
		isError: false,
		data: {
			app: {
				rssBytes: tallpineGiB * GIB,
				processCount: 20,
				cpuPercent: tallpineCPUPercent,
				cpuMeasured: true,
				own: reading("ao", 300 * 1024 ** 2, 3, [{ pid: 7, ppid: 1, rssBytes: 300 * 1024 ** 2, cpuPercent: 1, command: "tallpine daemon" }]),
			},
			system: host(availableGiB, pressureRaw, hostCPUPercent, load1),
			liveCount: 2,
		},
	};
}
