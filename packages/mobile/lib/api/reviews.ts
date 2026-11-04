import type { components } from "@tallpine/shared/api/schema";
import type { ServerConfig } from "../config";
import { API, req } from "./http";

// ---- Tallpine review detail ------------------------------------------------------

export type ReviewRun = components["schemas"]["ReviewRun"];

export type PRReviewState = components["schemas"]["PRReviewState"];

export type ReviewerSurface = components["schemas"]["DomainReviewerSurface"];

export type SessionReviews = {
	reviewerHandleId: string;
	reviewerHarness?: string;
	reviewerActivityState?: "active" | "idle" | "waiting_input" | "blocked" | "exited";
	reviewerSurface?: ReviewerSurface;
	reviews: PRReviewState[];
	runs: ReviewRun[];
};

export type ReviewerAgentConfig = components["schemas"]["AgentConfig"];

export async function getSessionReviews(cfg: ServerConfig, sessionId: string): Promise<SessionReviews> {
	const res = await req(cfg, `${API}/sessions/${encodeURIComponent(sessionId)}/reviews`);
	const data = await res.json();
	return {
		reviewerHandleId: typeof data?.reviewerHandleId === "string" ? data.reviewerHandleId : "",
		reviewerHarness: typeof data?.reviewerHarness === "string" ? data.reviewerHarness : undefined,
		reviewerActivityState: data?.reviewerActivityState,
		reviewerSurface: data?.reviewerSurface,
		reviews: Array.isArray(data?.reviews) ? data.reviews : [],
		runs: Array.isArray(data?.runs) ? data.runs : [],
	};
}

export async function triggerSessionReview(cfg: ServerConfig, sessionId: string): Promise<SessionReviews & { created: boolean }> {
	const res = await req(cfg, `${API}/sessions/${encodeURIComponent(sessionId)}/reviews/trigger`, { method: "POST" });
	const data = await res.json();
	return { ...mapSessionReviews(data), created: data?.created === true };
}

export async function cancelSessionReview(cfg: ServerConfig, sessionId: string): Promise<void> {
	await req(cfg, `${API}/sessions/${encodeURIComponent(sessionId)}/reviews/cancel`, { method: "POST" });
}

export async function restoreSessionReviewer(cfg: ServerConfig, sessionId: string): Promise<void> {
	await req(cfg, `${API}/sessions/${encodeURIComponent(sessionId)}/reviews/restore`, { method: "POST" });
}

export async function killSessionReviewer(cfg: ServerConfig, sessionId: string): Promise<SessionReviews> {
	const res = await req(cfg, `${API}/sessions/${encodeURIComponent(sessionId)}/reviews/kill`, { method: "POST" });
	return mapSessionReviews(await res.json());
}

export async function switchSessionReviewer(
	cfg: ServerConfig,
	sessionId: string,
	harness?: string,
	agentConfig?: ReviewerAgentConfig,
): Promise<SessionReviews> {
	const res = await req(cfg, `${API}/sessions/${encodeURIComponent(sessionId)}/reviews/switch`, {
		method: "POST",
		body: JSON.stringify({
			...(harness ? { harness } : {}),
			...(agentConfig ? { agentConfig } : {}),
		}),
	});
	return mapSessionReviews(await res.json());
}

function mapSessionReviews(data: any): SessionReviews {
	return {
		reviewerHandleId: typeof data?.reviewerHandleId === "string" ? data.reviewerHandleId : "",
		reviewerHarness: typeof data?.reviewerHarness === "string" ? data.reviewerHarness : undefined,
		reviewerActivityState: data?.reviewerActivityState,
		reviewerSurface: data?.reviewerSurface,
		reviews: Array.isArray(data?.reviews) ? data.reviews : [],
		runs: Array.isArray(data?.runs) ? data.runs : [],
	};
}

export async function requestSessionRereview(
	cfg: ServerConfig,
	sessionId: string,
	pullRequestUrl: string,
	reviewerId: string,
): Promise<void> {
	await req(cfg, `${API}/sessions/${encodeURIComponent(sessionId)}/reviews/rerequest`, {
		method: "POST",
		body: JSON.stringify({ pullRequestUrl, reviewerId }),
	});
}

export async function resolveSessionReviewComment(
	cfg: ServerConfig,
	sessionId: string,
	pullRequestUrl: string,
	commentUrl: string,
): Promise<void> {
	await req(cfg, `${API}/sessions/${encodeURIComponent(sessionId)}/reviews/comments/resolve`, {
		method: "POST",
		body: JSON.stringify({ pullRequestUrl, commentUrl }),
	});
}

