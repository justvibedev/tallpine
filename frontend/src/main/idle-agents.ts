interface LiveSession {
	id?: string;
	isTerminated?: boolean;
	activity?: { state?: string };
}

interface SessionReviews {
	reviewerActivityState?: string;
	runs?: { status?: string }[];
}

/**
 * True while any live session is mid-turn or has a review pass running.
 * Review state is read from each session's own review runs: the display status
 * is a single ranked phrase, so a worker waiting on its human shows "Blocked"
 * while its review runs. Anything unreadable counts as busy.
 */
export async function agentsBusy(port: number | null, request: typeof fetch = fetch): Promise<boolean> {
	if (!port) return true;
	const base = `http://127.0.0.1:${port}/api/v1/sessions`;
	try {
		const response = await request(`${base}?active=true`, { signal: AbortSignal.timeout(5_000) });
		if (!response.ok) return true;
		const body = (await response.json()) as { sessions?: unknown };
		if (!Array.isArray(body.sessions)) return true;
		const live = (body.sessions as LiveSession[]).filter((s) => !s.isTerminated);
		if (live.some((s) => s.activity?.state === "active")) return true;
		for (const session of live) {
			if (!session.id) return true;
			const reviews = await request(`${base}/${encodeURIComponent(session.id)}/reviews`, { signal: AbortSignal.timeout(5_000) });
			if (!reviews.ok) return true;
			const state = (await reviews.json()) as SessionReviews;
			if (state.reviewerActivityState === "active" || state.runs?.some((run) => run.status === "running")) return true;
		}
		return false;
	} catch {
		return true;
	}
}
