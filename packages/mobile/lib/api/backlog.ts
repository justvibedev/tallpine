import type { components } from "@tallpine/shared/api/schema";
import type { ServerConfig } from "../config";
import { API, req } from "./http";

// ---- Backlog (read-only list; "delegate" reuses delegateTask) ---------------

export type BacklogItem = Pick<components["schemas"]["BacklogItemResponse"], "ref" | "title" | "note" | "doneWhen" | "state" | "hold" | "childCount" | "childClosedCount" | "badges" | "plan">;

export async function getBacklog(cfg: ServerConfig, projectId: string): Promise<BacklogItem[]> {
	const res = await req(cfg, `${API}/projects/${encodeURIComponent(projectId)}/backlog?state=open`);
	const data = await res.json();
	return Array.isArray(data?.items) ? data.items : [];
}

// ---- Merge digest (read-only; the same data as `tallpine merges`) ----------

export type MergeEntry = components["schemas"]["MergesMergeEntry"];

export type MergeOverlap = components["schemas"]["MergesMergeOverlap"];

export type MergeDigest = components["schemas"]["ControllersMergeDigestResponse"];

export async function getMergeDigest(cfg: ServerConfig, projectId: string): Promise<MergeDigest> {
	const res = await req(cfg, `${API}/projects/${encodeURIComponent(projectId)}/merges`);
	const data = await res.json();
	return {
		recentlyMerged: Array.isArray(data?.recentlyMerged) ? data.recentlyMerged : [],
		inFlight: Array.isArray(data?.inFlight) ? data.inFlight : [],
		overlaps: Array.isArray(data?.overlaps) ? data.overlaps : [],
	};
}

