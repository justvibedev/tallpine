import type { components } from "@tallpine/shared/api/schema";
import type { ServerConfig } from "../config";
import { API, req } from "./http";

// ---- Pull request detail ----------------------------------------------------

// The rich per-PR view, from GET /sessions/{id}/pr. The board poll (GET
// /sessions) carries only PR *facts* — number, state, ci, review, mergeability —
// with no title, author, branches or diff stats, which is why the PR list can
// only show what a session already knows. This is the endpoint that has the
// rest, so it is fetched on demand when a PR is opened rather than on every
// 8s poll. Shape mirrors SessionPRSummary in
// backend/internal/httpd/controllers/dto.go.

export type PRFailingCheck = components["schemas"]["SessionPRFailingCheck"];
export type PRConflictFile = components["schemas"]["SessionPRConflictFile"];
export type PRReviewCommentLink = components["schemas"]["SessionPRReviewCommentLink"];

export type PRUnresolvedReviewer = components["schemas"]["SessionPRUnresolvedReviewer"];

export type PRReviewEntry = components["schemas"]["SessionPRReviewEntry"];

/** The daemon always sends headSha; it stays optional here so a payload without one is refused at merge time rather than typed away. */
export type SessionPRSummary = Omit<components["schemas"]["SessionPRSummary"], "headSha"> & { headSha?: string };

/** Squash-merges a session PR the way desktop does, fenced to the head commit on screen. */
export async function mergeSessionPR(cfg: ServerConfig, pr: Pick<SessionPRSummary, "number" | "url" | "headSha">): Promise<void> {
	if (!pr.headSha) throw new Error(`PR #${pr.number} has no head commit yet; refresh and try again.`);
	await req(cfg, `${API}/prs/${encodeURIComponent(String(pr.number))}/merge`, {
		method: "POST",
		body: JSON.stringify({ prUrl: pr.url, expectedHeadSha: pr.headSha }),
	});
}

export async function getSessionPR(cfg: ServerConfig, sessionId: string): Promise<SessionPRSummary[]> {
	const res = await req(cfg, `${API}/sessions/${encodeURIComponent(sessionId)}/pr`);
	const data = await res.json();
	return Array.isArray(data?.prs) ? data.prs : [];
}

