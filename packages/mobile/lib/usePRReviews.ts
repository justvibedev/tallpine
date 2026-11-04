import { useCallback, useEffect, useRef, useState } from "react";
import { getSessionReviews, type PRReviewState } from "./api";
import { machineIdentity, type ServerConfig } from "./config";
import type { PRSummarySession } from "./usePRSummaries";

// Native review state per PR for the PRs list. One read-only GET
// /sessions/{id}/reviews per session, fetched once per visit and again on
// pull-to-refresh; a failed fetch keeps whatever was already shown.
const BATCH = 6;

function key(config: ServerConfig, sessionId: string): string {
	return JSON.stringify([machineIdentity(config), sessionId, config.password]);
}

export function usePRReviews(sessions: PRSummarySession[]) {
	const cache = useRef<Record<string, PRReviewState[]>>({});
	const [, bump] = useState(0);
	const [generation, setGeneration] = useState(0);
	const fetched = useRef(-1);
	const targetKey = JSON.stringify(sessions.map(({ config, sessionId }) => [key(config, sessionId), config.host, config.httpPort, config.secure]));

	useEffect(() => {
		const force = generation > 0 && fetched.current !== generation;
		fetched.current = generation;
		const seen = new Set<string>();
		const targets = sessions.filter(({ config, sessionId }) => {
			const id = key(config, sessionId);
			if (!sessionId || seen.has(id)) return false;
			seen.add(id);
			return force || cache.current[id] === undefined;
		});
		if (targets.length === 0) return;
		let cancelled = false;
		void (async () => {
			for (let i = 0; i < targets.length; i += BATCH) {
				const chunk = targets.slice(i, i + BATCH);
				const results = await Promise.all(chunk.map(async ({ config, sessionId }) => {
					try {
						return [key(config, sessionId), (await getSessionReviews(config, sessionId)).reviews] as const;
					} catch {
						return [key(config, sessionId), null] as const;
					}
				}));
				if (cancelled) return;
				for (const [id, reviews] of results) {
					if (reviews) cache.current[id] = reviews;
					else cache.current[id] ??= [];
				}
				bump((n) => n + 1);
			}
		})();
		return () => { cancelled = true; };
		// `sessions` is covered by `targetKey`; the cache is a ref by design.
	}, [targetKey, generation]);

	const reload = useCallback(() => setGeneration((g) => g + 1), []);
	const reviewFor = useCallback(
		(config: ServerConfig, sessionId: string, prUrl: string, prNumber: number) =>
			cache.current[key(config, sessionId)]?.find((r) => r.prUrl === prUrl) ??
			cache.current[key(config, sessionId)]?.find((r) => r.prNumber === prNumber),
		[],
	);
	return { reviewFor, reload };
}
