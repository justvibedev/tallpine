import type { QueryClient, QueryKey } from "@tanstack/react-query";
import { conversationQueryRoot } from "../../hooks/conversation";

export type Invalidator = {
	invalidate: (queryKey: QueryKey) => void;
	clear: () => void;
};

// Do not repeatedly cancel a slow fetch under continuous CDC traffic. A
// key receives at most one in-flight refresh and one queued catch-up.
export function createInvalidator(queryClient: QueryClient, isDisposed: () => boolean): Invalidator {
	const refreshes = new Map<string, { dirty: boolean }>();
	const invalidate = (queryKey: QueryKey) => {
		if (isDisposed()) return;
		const key = JSON.stringify(queryKey);
		const running = refreshes.get(key);
		if (running) {
			running.dirty = true;
			return;
		}
		// A fetch from polling/mounting may already predate this event. Wait
		// for it, then refresh once so joining its promise cannot lose the event.
		const state = { dirty: queryClient.isFetching({ queryKey, type: "active" }) > 0 };
		// An inactive Chat hover prefetch cannot refresh itself after this event.
		// Cancel it without interrupting unrelated imperative queries.
		if (queryKey[0] === conversationQueryRoot[0] && queryClient.isFetching({ queryKey, type: "inactive" }) > 0) {
			void queryClient.cancelQueries({ queryKey, type: "inactive" });
		}
		refreshes.set(key, state);
		const settled = () => {
			refreshes.delete(key);
			if (state.dirty && !isDisposed()) invalidate(queryKey);
		};
		void queryClient.invalidateQueries({ queryKey }, { cancelRefetch: false }).then(settled, settled);
	};
	return { invalidate, clear: () => refreshes.clear() };
}
