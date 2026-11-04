import type { QueryClient, QueryKey } from "@tanstack/react-query";
import { refreshWorkspaceSession, workspaceQueryKey } from "../../hooks/useWorkspaceQuery";
import { sessionScmSummaryQueryKey } from "../../hooks/useSessionScmSummary";
import { conversationQueryKey, conversationQueryRoot } from "../../hooks/conversation";
import { reviewerConversationQueryKey, reviewerConversationQueryRoot } from "../../hooks/useReviewerConversation";
import { sessionReviewsQueryKey } from "../session-reviews";
import { sessionUsageQueryRoot } from "../../hooks/useSessionUsageSummaries";
import { editorHandoffQueryKey, editorHandoffQueryRoot } from "../../hooks/useEditorHandoff";
import { backlogInboxCountsKey, backlogProjectKey, backlogRootKey } from "../backlog-api";
import {
	ID_ONLY_WORKSPACE_REFRESH_MS,
	INVALIDATE_WINDOW_MS,
	SESSION_PATCH_MIN_INTERVAL_MS,
	WORKSPACE_REFETCH_DEBOUNCE_MS,
	WORKSPACE_REFETCH_MAX_WAIT_MS,
} from "./constants";
import type { PendingState } from "./pending-state";

export type RefreshScheduler = {
	scheduleFlush: () => void;
	queueIdOnlySessionPatch: (sessionId: string) => void;
	dispose: () => void;
};

type RefreshSchedulerDeps = {
	queryClient: QueryClient;
	invalidate: (queryKey: QueryKey) => void;
	pending: PendingState;
	isDisposed: () => boolean;
};

// Batches pending invalidations into flushes and paces the workspace list
// refetches (whole-list debounce, per-session patches, id-only throttling).
export function createRefreshScheduler({ queryClient, invalidate, pending, isDisposed }: RefreshSchedulerDeps): RefreshScheduler {
	let refreshTimer: ReturnType<typeof setTimeout> | undefined;
	// An id-only update waits here until the workspace is a full
	// ID_ONLY_WORKSPACE_REFRESH_MS old; any workspace refresh covers it.
	const idOnlyReleasedAt = new Map<string, number>();
	const idOnlyTimers = new Map<string, ReturnType<typeof setTimeout>>();
	const sessionPatches = new Map<string, { running: boolean; dirty: boolean; lastAt: number; timer?: ReturnType<typeof setTimeout> }>();
	let fullRefetchTimer: ReturnType<typeof setTimeout> | undefined;
	let fullRefetchFirstAt: number | undefined;
	// Trailing debounce with a max wait, for whole-list refetches.
	const requestFullWorkspaceRefetch = () => {
		if (isDisposed()) return;
		const now = Date.now();
		fullRefetchFirstAt ??= now;
		if (fullRefetchTimer !== undefined) clearTimeout(fullRefetchTimer);
		const wait = Math.max(0, Math.min(WORKSPACE_REFETCH_DEBOUNCE_MS, fullRefetchFirstAt + WORKSPACE_REFETCH_MAX_WAIT_MS - now));
		fullRefetchTimer = setTimeout(() => {
			fullRefetchTimer = undefined;
			fullRefetchFirstAt = undefined;
			invalidate(workspaceQueryKey);
		}, wait);
	};
	// Re-read one session into the cached list. One read per session in
	// flight; events arriving meanwhile, or sooner than the minimum interval,
	// collapse into one more read.
	const requestSessionPatch = (sessionId: string) => {
		if (isDisposed()) return;
		let state = sessionPatches.get(sessionId);
		if (!state) {
			state = { running: false, dirty: false, lastAt: Number.NEGATIVE_INFINITY };
			sessionPatches.set(sessionId, state);
		}
		const patch = state;
		if (patch.running || patch.timer !== undefined) {
			patch.dirty = true;
			return;
		}
		const run = () => {
			patch.timer = undefined;
			patch.dirty = false;
			patch.running = true;
			void refreshWorkspaceSession(queryClient, sessionId).then(
				(outcome) => {
					if (outcome === "refetch") requestFullWorkspaceRefetch();
				},
				requestFullWorkspaceRefetch,
			).finally(() => {
				patch.running = false;
				patch.lastAt = Date.now();
				if (patch.dirty && !isDisposed()) requestSessionPatch(sessionId);
			});
		};
		const wait = patch.lastAt + SESSION_PATCH_MIN_INTERVAL_MS - Date.now();
		if (wait <= 0) run();
		else patch.timer = setTimeout(run, wait);
	};
	// The scheduled flush body. Extracted so a leading-edge event can run
	// it immediately without waiting out a full window.
	let lastFlushAt = Number.NEGATIVE_INFINITY;
	const flushPending = () => {
		flushConversations(invalidate, pending);
		flushWorkspace(invalidate, pending, requestFullWorkspaceRefetch, requestSessionPatch);
		if (pending.allEventScopedCachesInvalidation) {
			invalidate(["session-reviews"]);
			invalidate(["session-interface-transition"]);
			invalidate(["agent-models"]);
			pending.allEventScopedCachesInvalidation = false;
		}
		flushSessionScoped(invalidate, pending);
		flushBacklog(invalidate, pending);
	};
	// A busy stream must not postpone visible updates until traffic
	// stops, and the first event after a quiet period must not wait out
	// a full window either. Flush on the leading edge when the last
	// flush is at least one window old; otherwise coalesce this and
	// later events into a single trailing flush. invalidate() still
	// dedups the resulting refetches per key, so the leading edge
	// cannot start a refetch storm.
	const scheduleFlush = () => {
		if (refreshTimer !== undefined) return;
		const sinceLastFlush = Date.now() - lastFlushAt;
		if (sinceLastFlush >= INVALIDATE_WINDOW_MS) {
			lastFlushAt = Date.now();
			flushPending();
			return;
		}
		refreshTimer = setTimeout(() => {
			refreshTimer = undefined;
			lastFlushAt = Date.now();
			flushPending();
		}, INVALIDATE_WINDOW_MS - sinceLastFlush);
	};
	// Releases an id-only update's session for the next flush once that
	// session's last release is a full interval old; until then a timer waits.
	const queueIdOnlySessionPatch = (sessionId: string) => {
		if (idOnlyTimers.has(sessionId)) return;
		const wait = (idOnlyReleasedAt.get(sessionId) ?? Number.NEGATIVE_INFINITY) + ID_ONLY_WORKSPACE_REFRESH_MS - Date.now();
		const release = () => {
			idOnlyReleasedAt.set(sessionId, Date.now());
			pending.sessionPatches.add(sessionId);
			pending.workspaceInvalidation = true;
		};
		if (wait <= 0) {
			release();
			return;
		}
		idOnlyTimers.set(sessionId, setTimeout(() => {
			idOnlyTimers.delete(sessionId);
			if (isDisposed()) return;
			release();
			scheduleFlush();
		}, wait));
	};
	const dispose = () => {
		if (refreshTimer !== undefined) clearTimeout(refreshTimer);
		for (const timer of idOnlyTimers.values()) clearTimeout(timer);
		idOnlyTimers.clear();
		if (fullRefetchTimer !== undefined) clearTimeout(fullRefetchTimer);
		for (const patch of sessionPatches.values()) if (patch.timer !== undefined) clearTimeout(patch.timer);
		sessionPatches.clear();
		pending.sessionPatches.clear();
		pending.conversationSessions.clear();
		pending.reviewSessions.clear();
		pending.interfaceTransitionSessions.clear();
		pending.modelCatalogScopes.clear();
		pending.backlogProjects.clear();
	};
	return { scheduleFlush, queueIdOnlySessionPatch, dispose };
}

type Invalidate = (queryKey: QueryKey) => void;

function flushConversations(invalidate: Invalidate, pending: PendingState) {
	if (!pending.allConversationsInvalidation) return;
	invalidate(conversationQueryRoot);
	invalidate(reviewerConversationQueryRoot);
	pending.allConversationsInvalidation = false;
}

function flushWorkspace(
	invalidate: Invalidate,
	pending: PendingState,
	requestFullWorkspaceRefetch: () => void,
	requestSessionPatch: (sessionId: string) => void,
) {
	if (!pending.workspaceInvalidation) return;
	if (pending.immediateWorkspaceRefetch) {
		pending.immediateWorkspaceRefetch = false;
		invalidate(workspaceQueryKey);
	}
	if (pending.fullWorkspaceRefetchRequested) {
		pending.fullWorkspaceRefetchRequested = false;
		requestFullWorkspaceRefetch();
	}
	for (const sessionId of pending.sessionPatches) requestSessionPatch(sessionId);
	pending.sessionPatches.clear();
	invalidate(sessionScmSummaryQueryKey());
	invalidate(sessionUsageQueryRoot);
	pending.workspaceInvalidation = false;
}

function flushSessionScoped(invalidate: Invalidate, pending: PendingState) {
	if (pending.allEditorHandoffsInvalidation) {
		invalidate(editorHandoffQueryRoot);
		pending.allEditorHandoffsInvalidation = false;
		pending.editorHandoffSessions.clear();
	} else {
		for (const sessionId of pending.editorHandoffSessions) {
			invalidate(editorHandoffQueryKey(sessionId));
		}
		pending.editorHandoffSessions.clear();
	}
	for (const sessionId of pending.conversationSessions) {
		invalidate(conversationQueryKey(sessionId));
	}
	pending.conversationSessions.clear();
	for (const sessionId of pending.reviewSessions) {
		invalidate(sessionReviewsQueryKey(sessionId));
	}
	pending.reviewSessions.clear();
	for (const reviewId of pending.reviewerConversations) {
		invalidate(reviewerConversationQueryKey(reviewId));
	}
	pending.reviewerConversations.clear();
	for (const sessionId of pending.interfaceTransitionSessions) {
		invalidate(["session-interface-transition", sessionId]);
	}
	pending.interfaceTransitionSessions.clear();
	for (const scope of pending.modelCatalogScopes.values()) {
		// Catalogs are device-wide: pickers key them by agent, not by the
		// project the event names, so refresh every view of this agent
		// (pickers and Settings) together.
		invalidate(["agent-models", scope.agentId]);
	}
	pending.modelCatalogScopes.clear();
}

function flushBacklog(invalidate: Invalidate, pending: PendingState) {
	if (pending.allBacklogInvalidation) {
		invalidate(backlogRootKey);
		pending.allBacklogInvalidation = false;
	} else if (pending.backlogProjects.size > 0) {
		invalidate(backlogInboxCountsKey);
		for (const projectId of pending.backlogProjects) invalidate(backlogProjectKey(projectId));
	}
	pending.backlogProjects.clear();
}
