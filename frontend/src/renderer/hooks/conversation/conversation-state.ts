/** Query keys, local echoes, dispatch tracking and provider catalog epochs. */

import { type InfiniteData, type QueryClient } from "@tanstack/react-query";
import { sessionUiKey } from "../../lib/hosts";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";
import type { ConversationSnapshot } from "../../types/conversation";
import { ConversationSwitchDisplay } from "./conversation-types";

export const conversationQueryRoot = ["conversation"] as const;

export function conversationQueryKey(sessionId: string, hostId?: string) {
	return hostId ? (["remote-conversation", hostId, sessionId] as const) : ([...conversationQueryRoot, sessionId] as const);
}

export function conversationModelsQueryKey(sessionId: string, hostId?: string) {
	return hostId ? (["remote-conversation-models", hostId, sessionId] as const) : (["conversation-models", sessionId] as const);
}

export function conversationConfigOptionsQueryKey(sessionId: string, hostId?: string) {
	return hostId ? (["remote-conversation-config-options", hostId, sessionId] as const) : (["conversation-config-options", sessionId] as const);
}

export const conversationDispatchTrackingQueryKey = ["conversation-dispatch-tracking"] as const;
export const conversationLocalEchosQueryKey = ["conversation-local-echos"] as const;
export type ConversationDispatchOperation = "edit" | "retry" | "send";
export interface ConversationDispatchDescriptor {
	operation: ConversationDispatchOperation;
	requestId: string;
	sourceTurnId?: string;
}
export type ConversationDispatchTracking =
	| (ConversationDispatchDescriptor & { state: "pending" })
	| (ConversationDispatchDescriptor & { state: "accepted"; turnId: string });
export type ConversationDispatchTrackingBySession = Record<string, ConversationDispatchTracking>;

/**
 * Renderer-only acknowledgement of a human send. It is deliberately separate
 * from the durable snapshot: CDC identifies a conversation, not one exact new
 * item, so treating it as a partial server update would make ordering unsafe.
 */
export type ConversationLocalEcho = {
	clientMessageId: string;
	text: string;
	/** A continue after a stop, drawn as a marker while it is sent. */
	continuation?: boolean;
	createdAt: string;
	/** A hibernated send is acknowledged locally while the provider wakes. */
	backgroundWake?: boolean;
	/** A turn was already active when this was sent, so it belongs in the queue dock, not the chat. */
	queued?: boolean;
	/** Excerpts are rendered inside the optimistic user message bubble. */
	excerpts?: (Pick<ChatDraftExcerptReference, "text" | "messageId" | "revision"> & { conversationId?: string })[];
	/** Filled after the daemon accepts the send, then used for exact reconciliation. */
	turnId?: string;
	/** Sent with an agent switch: the transcript shows the "Switched" line above it at once. */
	providerSwitch?: ConversationSwitchDisplay & {
		targetHarness: string;
		/** The newest sequence seen when it was sent; the daemon's own line comes after it. */
		afterSequence: number;
	};
};
export type ConversationLocalEchosBySession = Record<string, ConversationLocalEcho[]>;

export function newestCachedSequence(queryClient: QueryClient, sessionId: string, hostId?: string): number {
	const cached = queryClient.getQueryData<InfiniteData<ConversationSnapshot>>(conversationQueryKey(sessionId, hostId));
	return Math.max(0, ...(cached?.pages.map((page) => page.latestSequence) ?? []));
}

export function switchEcho(
	display: ConversationSwitchDisplay | undefined,
	targetHarness: string | undefined,
	afterSequence: number,
): ConversationLocalEcho["providerSwitch"] {
	return display && targetHarness ? { ...display, targetHarness, afterSequence } : undefined;
}

export function addConversationLocalEcho(
	queryClient: QueryClient,
	targetSessionId: string,
	echo: ConversationLocalEcho,
): void {
	// An echo shown while an agent switch ran is replaced by its send's own.
	queryClient.setQueryData<ConversationLocalEchosBySession>(conversationLocalEchosQueryKey, (current = {}) => ({
		...current,
		[targetSessionId]: [
			...(current[targetSessionId] ?? []).filter((existing) => existing.clientMessageId !== echo.clientMessageId),
			echo,
		],
	}));
}

export function acceptConversationLocalEcho(
	queryClient: QueryClient,
	targetSessionId: string,
	clientMessageId: string,
	turnId: string,
): void {
	queryClient.setQueryData<ConversationLocalEchosBySession>(conversationLocalEchosQueryKey, (current = {}) => {
		const echoes = current[targetSessionId];
		if (!echoes) return current;
		let changed = false;
		const nextEchoes = echoes.map((echo) => {
			if (echo.clientMessageId !== clientMessageId || echo.turnId === turnId) return echo;
			changed = true;
			return { ...echo, turnId };
		});
		return changed ? { ...current, [targetSessionId]: nextEchoes } : current;
	});
}

export function releaseConversationLocalEcho(
	queryClient: QueryClient,
	targetSessionId: string,
	clientMessageId?: string,
	turnId?: string,
): void {
	queryClient.setQueryData<ConversationLocalEchosBySession>(conversationLocalEchosQueryKey, (current = {}) => {
		const echoes = current[targetSessionId];
		if (!echoes) return current;
		const nextEchoes = echoes.filter(
			(echo) =>
				(clientMessageId === undefined || echo.clientMessageId !== clientMessageId) &&
				(turnId === undefined || echo.turnId !== turnId),
		);
		if (nextEchoes.length === echoes.length) return current;
		const next = { ...current };
		if (nextEchoes.length === 0) delete next[targetSessionId];
		else next[targetSessionId] = nextEchoes;
		return next;
	});
}

export function claimConversationDispatch(
	queryClient: QueryClient,
	targetSessionId: string,
	requestId: string,
	operation: ConversationDispatchOperation,
	sourceTurnId?: string,
): boolean {
	const current =
		queryClient.getQueryData<ConversationDispatchTrackingBySession>(
			conversationDispatchTrackingQueryKey,
		) ?? {};
	if (current[targetSessionId]?.state === "pending") return false;
	queryClient.setQueryData<ConversationDispatchTrackingBySession>(
		conversationDispatchTrackingQueryKey,
		{
			...current,
			[targetSessionId]: { operation, requestId, sourceTurnId, state: "pending" },
		},
	);
	return true;
}

export function acceptConversationDispatch(
	queryClient: QueryClient,
	targetSessionId: string,
	requestId: string,
	turnId: string,
): void {
	queryClient.setQueryData<ConversationDispatchTrackingBySession>(
		conversationDispatchTrackingQueryKey,
		(current = {}) => {
			const tracked = current[targetSessionId];
			if (tracked?.requestId !== requestId) return current;
			return {
				...current,
				[targetSessionId]: { ...tracked, state: "accepted", turnId },
			};
		},
	);
}

export function releaseConversationDispatch(
	queryClient: QueryClient,
	targetSessionId: string,
	requestId: string,
): void {
	queryClient.setQueryData<ConversationDispatchTrackingBySession>(
		conversationDispatchTrackingQueryKey,
		(current = {}) => {
			if (current[targetSessionId]?.requestId !== requestId) return current;
			const next = { ...current };
			delete next[targetSessionId];
			return next;
		},
	);
}

export function conversationSkillsQueryKey(sessionId: string, hostId?: string) {
	return hostId ? (["remote-conversation-skills", hostId, sessionId] as const) : (["conversation-skills", sessionId] as const);
}

export const conversationProviderCatalogEpochs = new WeakMap<QueryClient, Map<string, number>>();

export function conversationProviderCatalogEpoch(queryClient: QueryClient, sessionId: string): number {
	return conversationProviderCatalogEpochs.get(queryClient)?.get(sessionId) ?? 0;
}

export function advanceConversationProviderCatalogEpoch(queryClient: QueryClient, sessionId: string) {
	let epochs = conversationProviderCatalogEpochs.get(queryClient);
	if (!epochs) {
		epochs = new Map();
		conversationProviderCatalogEpochs.set(queryClient, epochs);
	}
	epochs.set(sessionId, conversationProviderCatalogEpoch(queryClient, sessionId) + 1);
}

/** Drop provider-owned catalogs so a handoff cannot keep showing the outgoing controller's options. */
export function clearConversationProviderCatalogs(queryClient: QueryClient, sessionId: string, hostId?: string) {
	advanceConversationProviderCatalogEpoch(queryClient, sessionUiKey(sessionId, hostId));
	queryClient.removeQueries({ queryKey: conversationModelsQueryKey(sessionId, hostId) });
	queryClient.removeQueries({ queryKey: conversationConfigOptionsQueryKey(sessionId, hostId) });
	queryClient.removeQueries({ queryKey: conversationSkillsQueryKey(sessionId, hostId) });
}

/** Refetch provider-owned catalogs after the target controller owns the session again. */
export function invalidateConversationProviderCatalogs(queryClient: QueryClient, sessionId: string, hostId?: string) {
	void queryClient.invalidateQueries({ queryKey: conversationModelsQueryKey(sessionId, hostId) });
	void queryClient.invalidateQueries({ queryKey: conversationConfigOptionsQueryKey(sessionId, hostId) });
	void queryClient.invalidateQueries({ queryKey: conversationSkillsQueryKey(sessionId, hostId) });
}
