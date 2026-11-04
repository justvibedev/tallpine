type AppStateLike = {
	currentState: string | null | undefined;
	addEventListener: (type: "change", listener: (state: string) => void) => { remove: () => void };
};

/**
 * Calls `refresh` now and every `ms` while the app is active. Backgrounded, the
 * timer stops; returning to active refreshes once and restarts it. Returns the stopper.
 */
export function startPlanPolling(refresh: () => void, appState: AppStateLike, ms: number): () => void {
	let timer: ReturnType<typeof setInterval> | undefined;
	const start = () => { if (!timer) timer = setInterval(refresh, ms); };
	const stop = () => { if (timer) { clearInterval(timer); timer = undefined; } };
	if (appState.currentState === "active") { refresh(); start(); }
	const sub = appState.addEventListener("change", (state) => {
		if (state === "active") { refresh(); start(); } else stop();
	});
	return () => { stop(); sub.remove(); };
}
