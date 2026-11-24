import { useEffect, useState } from "react";
import type { BrowserHandoffState } from "../../shared/browser-handoff";
import { tallpineBridge } from "../lib/bridge";

/** The session's running browser handoff (B858), or null; follows main's changes. */
export function useBrowserHandoff(sessionId: string | undefined): BrowserHandoffState | null {
	const [state, setState] = useState<BrowserHandoffState | null>(null);
	useEffect(() => {
		setState(null);
		if (!sessionId) return;
		let live = true;
		// A change that arrives first is newer than the initial read.
		let changed = false;
		const off = tallpineBridge.browser.onHandoffChanged((change) => {
			if (change.sessionId !== sessionId) return;
			changed = true;
			setState(change.state);
		});
		void tallpineBridge.browser
			.handoffState(sessionId)
			.then((initial) => {
				if (live && !changed) setState(initial);
			})
			.catch(() => undefined);
		return () => {
			live = false;
			off();
		};
	}, [sessionId]);
	return state;
}
