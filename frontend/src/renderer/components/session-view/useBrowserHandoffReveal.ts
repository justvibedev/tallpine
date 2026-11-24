import { useEffect, useRef } from "react";
import { useBrowserHandoff } from "../../hooks/useBrowserHandoff";

/**
 * Opens the Browser panel once per browser handoff (B858): the agent is
 * waiting on the person, so the page and its banner come to the front. A
 * later close by the person is not overridden.
 */
export function useBrowserHandoffReveal(sessionId: string | undefined, enabled: boolean, reveal: () => void): void {
	const handoff = useBrowserHandoff(sessionId);
	const revealedId = useRef<string | null>(null);
	useEffect(() => {
		if (!enabled || !handoff || revealedId.current === handoff.id) return;
		revealedId.current = handoff.id;
		reveal();
	}, [enabled, handoff, reveal]);
}
