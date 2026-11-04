import { SESSION_PATCH_EVENT_TYPES, STATUS_READINESS_EVENT } from "./constants";
import type { PendingState } from "./pending-state";
import type { RefreshScheduler } from "./refresh-scheduler";

type CdcEnvelope = {
	sessionId?: unknown;
	projectId?: unknown;
	type?: unknown;
	payload?: unknown;
};

type CdcPayload = {
	conversationId?: unknown;
	reviewId?: unknown;
	interfaceTransitionId?: unknown;
	kind?: unknown;
	agentId?: unknown;
	projectId?: unknown;
};

type CdcClassification = {
	conversationOnly: boolean;
	modelCatalogOnly: boolean;
	// Project-level backlog events never change the session list; a later PR adds the Backlog page subscriber that consumes them.
	backlogOnly: boolean;
	idOnlySessionUpdate: boolean;
	patchSessionId: string | undefined;
	// An event naming no session, or one we could not read, needs the list.
	wholeListEvent: boolean;
};

// Records on `pending` what the decoded CDC event touches and says which kind
// of event it was. Throws nothing the caller does not already guard.
function classifyCdcEvent(decoded: CdcEnvelope, pending: PendingState): CdcClassification {
	const result: CdcClassification = {
		conversationOnly: false,
		modelCatalogOnly: false,
		backlogOnly: false,
		idOnlySessionUpdate: false,
		patchSessionId: undefined,
		wholeListEvent: true,
	};
	// The SSE endpoint sends the complete durable CDC event. Routing
	// fields such as sessionId live on that envelope, while trigger-built
	// details such as conversationId live inside its payload. Do not
	// mistake the payload for the entire event: doing so refreshes the
	// sidebar but leaves a Chat timeline frozen on its pre-turn snapshot.
	const payload =
		typeof decoded.payload === "object" && decoded.payload !== null ? (decoded.payload as CdcPayload) : undefined;
	// A review run can start or finish without this window asking for
	// it: an agent ran `tallpine review trigger`, auto-review fired, or a
	// reviewer submitted. Refresh that session's reviews so the
	// inspector shows it instead of a stale verdict.
	if (
		(decoded.type === "review_run_created" || decoded.type === "review_run_updated") &&
		typeof decoded.sessionId === "string" &&
		decoded.sessionId
	) {
		pending.reviewSessions.add(decoded.sessionId);
	}
	if (decoded.type === "backlog_changed") {
		result.backlogOnly = true;
		if (typeof decoded.projectId === "string" && decoded.projectId) pending.backlogProjects.add(decoded.projectId);
		else pending.allBacklogInvalidation = true;
	}
	if (payload?.kind === "model_catalog" && typeof payload.agentId === "string" && typeof payload.projectId === "string") {
		pending.modelCatalogScopes.set(`${payload.agentId}\0${payload.projectId}`, {
			agentId: payload.agentId,
			projectId: payload.projectId,
		});
		result.modelCatalogOnly = true;
	}
	if (
		typeof payload?.reviewId === "string" &&
		payload.reviewId &&
		typeof payload.conversationId === "string" &&
		payload.conversationId
	) {
		pending.reviewerConversations.add(payload.reviewId);
		result.conversationOnly = true;
	} else if (
		typeof decoded.sessionId === "string" &&
		decoded.sessionId &&
		typeof payload?.interfaceTransitionId === "string" &&
		payload.interfaceTransitionId
	) {
		pending.interfaceTransitionSessions.add(decoded.sessionId);
	}
	if (
		typeof decoded.sessionId === "string" &&
		decoded.sessionId &&
		typeof payload?.conversationId === "string" &&
		payload.conversationId
	) {
		pending.conversationSessions.add(decoded.sessionId);
		result.conversationOnly = true;
	}
	result.wholeListEvent = !(
		typeof decoded.sessionId === "string" &&
		decoded.sessionId &&
		typeof decoded.type === "string" &&
		SESSION_PATCH_EVENT_TYPES.has(decoded.type)
	);
	if (!result.wholeListEvent) result.patchSessionId = decoded.sessionId as string;
	if (
		decoded.type === "session_updated" &&
		typeof decoded.sessionId === "string" &&
		decoded.sessionId &&
		typeof payload?.conversationId !== "string" &&
		typeof payload?.interfaceTransitionId !== "string"
	) {
		// Async chat startup changes the session's provisionState, but
		// does not emit a conversationId. Refresh the open conversation
		// so its controller state can leave "connecting".
		pending.conversationSessions.add(decoded.sessionId);
		pending.editorHandoffSessions.add(decoded.sessionId);
		// Only the workspace waits for an id-only update: the session's
		// own caches above still refresh at once, because such an update
		// can be what tells an open chat that its start-up finished.
		result.idOnlySessionUpdate =
			payload !== undefined && Object.keys(payload).every((key) => key === "id" || key === "sessionId");
	}
	return result;
}

type WorkspaceRefresherDeps = {
	pending: PendingState;
	scheduler: RefreshScheduler;
	isDisposed: () => boolean;
};

export function createWorkspaceRefresher({ pending, scheduler, isDisposed }: WorkspaceRefresherDeps) {
	return (event?: Event) => {
		if (isDisposed()) return;
		let classification: CdcClassification = {
			conversationOnly: false,
			modelCatalogOnly: false,
			backlogOnly: false,
			idOnlySessionUpdate: false,
			patchSessionId: undefined,
			wholeListEvent: true,
		};
		if (event === undefined) {
			pending.immediateWorkspaceRefetch = true;
			// A lifecycle refresh -- reconnect, daemon status change, base-URL change --
			// carries no event, so we cannot know which conversations moved. Normally the
			// replay that follows tells us, but when the event log has been truncated the
			// daemon starts us at head and no CDC arrives at all. EventSource cannot read
			// the header reporting that clamp, so refresh every conversation instead of
			// leaving an open chat frozen on its pre-gap snapshot.
			pending.allConversationsInvalidation = true;
			pending.allBacklogInvalidation = true;
			pending.allEditorHandoffsInvalidation = true;
			pending.allEventScopedCachesInvalidation = true;
		}
		if (event && "data" in event) {
			try {
				const decoded = JSON.parse(String((event as MessageEvent).data)) as CdcEnvelope;
				classification = classifyCdcEvent(decoded, pending);
			} catch {
				// A malformed CDC payload still invalidates workspaces; it simply
				// cannot target a conversation cache precisely.
			}
		}
		// One-shot and rare: the list must leave "checking" promptly, not after the debounce.
		if (event?.type === STATUS_READINESS_EVENT) pending.immediateWorkspaceRefetch = true;
		const { conversationOnly, modelCatalogOnly, backlogOnly, idOnlySessionUpdate, patchSessionId } = classification;
		if (event === undefined || pending.immediateWorkspaceRefetch) {
			pending.workspaceInvalidation = true;
		} else if (idOnlySessionUpdate && patchSessionId) {
			scheduler.queueIdOnlySessionPatch(patchSessionId);
		} else if (!conversationOnly && !modelCatalogOnly && !backlogOnly) {
			if (patchSessionId) pending.sessionPatches.add(patchSessionId);
			else pending.fullWorkspaceRefetchRequested = true;
			pending.workspaceInvalidation = true;
		}
		scheduler.scheduleFlush();
	};
}
