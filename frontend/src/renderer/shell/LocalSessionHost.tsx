import { CatchBoundary, useMatch } from "@tanstack/react-router";
import { Suspense } from "react";
import { LazyChunkErrorPanel } from "../components/LazyChunkBoundary";
import { SessionView } from "./lazyChunks";

export function captureSessionViewCrash(_error: Error) {
}

/**
 * Project sessions and scratchpads are separate routes, so rendering
 * SessionView from each route remounted the whole session surface (chat,
 * terminals, inspector) on every switch between them. Both routes render
 * nothing; this host keeps one SessionView mounted at a stable position and
 * only swaps its props. Remote host routes stay keyed per session.
 */
export function LocalSessionHost() {
	const scratchpadSessionId = useMatch({
		from: "/_shell/sessions/$sessionId",
		shouldThrow: false,
		select: (match) => match.params.sessionId,
	});
	const projectSession = useMatch({
		from: "/_shell/projects/$projectId_/sessions/$sessionId",
		shouldThrow: false,
		select: (match) => match.params,
	});
	const sessionId = projectSession?.sessionId ?? scratchpadSessionId;
	if (!sessionId) return null;
	return (
		<CatchBoundary errorComponent={LazyChunkErrorPanel} getResetKey={() => sessionId} onCatch={captureSessionViewCrash}>
			<Suspense fallback={null}>
				<SessionView projectId={projectSession?.projectId} sessionId={sessionId} />
			</Suspense>
		</CatchBoundary>
	);
}
