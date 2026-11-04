/**
 * Live conversation data for a Chat session.
 *
 * The daemon serves the snapshot already ordered by sequence, so this hook maps
 * the wire shape onto the view model and does not re-sort, re-derive turn state,
 * or decide which approvals are actionable. Those are daemon decisions.
 *
 * Live changes arrive through the daemon event channel. Older history is fetched
 * in bounded pages only when the reader asks for it.
 */

import { type InfiniteData, type QueryClient, infiniteQueryOptions, useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useState } from "react";
import type { components } from "../../../api/schema";
import { apiErrorCode, apiErrorMessage } from "../../lib/api-client";
import { clientForSessionHost } from "../../lib/host-clients";
import { sessionUiKey } from "../../lib/hosts";
import { subscribeWorkspaceFileChanges } from "../../lib/workspace-file-events";
import { workspaceQueryKeyForHost } from "../useWorkspaceQuery";
import type { ConversationSnapshot, ChatConfigOption, ChatConfigOptionValue, ChatModel, ChatSkill } from "../../types/conversation";
import { toSnapshot, mergeConversationPages } from "./conversation-reducer";
import { WireSnapshot, switchOutcomeUnknown, ProviderSwitchFailed } from "./conversation-types";
import { conversationQueryKey, clearConversationProviderCatalogs, invalidateConversationProviderCatalogs, conversationModelsQueryKey, conversationConfigOptionsQueryKey, conversationProviderCatalogEpoch, conversationSkillsQueryKey } from "./conversation-state";

export const CONVERSATION_PAGE_SIZE = 200;
export const remoteConversationRefreshErrorKey = (sessionId: string, hostId: string) => ["remote-conversation-refresh-error", hostId, sessionId] as const;
export const CONFIG_OPTIONS_POLL_INTERVAL_MS = 5_000;
export const CONFIG_OPTION_WRITE_TIMEOUT_MS = 60_000;
export const SKILLS_POLL_INTERVAL_MS = 60_000;

/**
 * Answers that will never change on a retry. SESSION_MODE_MISMATCH is permanent
 * while the session's committed controller is TUI; the others describe a session or
 * controller state the client should explain rather than poll at.
 */
export const PERMANENT_CODES = new Set([
	"SESSION_MODE_MISMATCH",
	"SESSION_NOT_FOUND",
	"SESSION_MODE_UNSUPPORTED",
	"CHAT_AUTH_REQUIRED",
]);

export interface ConversationQueryResult {
	snapshot?: ConversationSnapshot;
	isLoading: boolean;
	/** Set when the session exists but has no chat conversation to show. */
	unavailable?: { code: string; message: string };
	error?: string;
	refreshError?: string;
	hasOlder: boolean;
	isLoadingOlder: boolean;
	loadOlder: () => void;
}

export async function fetchConversationPage(sessionId: string, hostId?: string, beforeSequence?: number): Promise<ConversationSnapshot> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/conversation", {
		params: { path: { sessionId }, query: { beforeSequence, limit: CONVERSATION_PAGE_SIZE } },
	});
	if (error) throw error;
	return toSnapshot(data as WireSnapshot);
}

/** Refresh only the live page; refetching an infinite query downloads all older history. */
export async function refreshRemoteConversation(queryClient: QueryClient, sessionId: string, hostId: string): Promise<void> {
	let latest: ConversationSnapshot;
	try {
		latest = await fetchConversationPage(sessionId, hostId);
	} catch (error) {
		queryClient.setQueryData(remoteConversationRefreshErrorKey(sessionId, hostId), true);
		throw error;
	}
	queryClient.setQueryData(remoteConversationRefreshErrorKey(sessionId, hostId), false);
	queryClient.setQueryData<InfiniteData<ConversationSnapshot>>(conversationQueryKey(sessionId, hostId), (previous) => {
		if (previous?.pages.length && previous.pages[0].conversationId === latest.conversationId && previous.pages[0].activeBranchId === latest.activeBranchId && latest.latestSequence < previous.pages[0].latestSequence) return previous;
		if (!previous?.pages.length || !latest.hasMoreBefore || previous.pages[0].conversationId !== latest.conversationId || previous.pages[0].activeBranchId !== latest.activeBranchId) return { pages: [latest], pageParams: [undefined] };
		const priorLive = previous.pages[0];
		const slidOut = latest.oldestSequence > priorLive.oldestSequence
			? priorLive.items.filter((item) => item.sequence < latest.oldestSequence)
			: [];
		const slidTurnIds = new Set(slidOut.map((item) => item.turnId));
		const live = slidOut.length
			? mergeConversationPages([latest, { ...priorLive, items: slidOut, turns: priorLive.turns.filter((turn) => slidTurnIds.has(turn.id)) }]) ?? latest
			: latest;
		return { ...previous, pages: [live, ...previous.pages.slice(1)] };
	});
}

export function conversationQueryOptions(sessionId: string, hostId?: string) {
	return infiniteQueryOptions({
		queryKey: conversationQueryKey(sessionId, hostId),
		initialPageParam: undefined as number | undefined,
		queryFn: ({ pageParam }) => fetchConversationPage(sessionId, hostId, pageParam),
		getNextPageParam: (page) => (page.hasMoreBefore ? page.oldestSequence : undefined),
		// A mode mismatch is authoritative for this committed controller epoch, so
		// retrying the same request cannot help and would leave the surface loading
		// instead of explaining why there is no conversation. Only genuinely
		// transient failures are retried.
		retry: (attempt, error) => {
			const code = apiErrorCode(error);
			if (code && PERMANENT_CODES.has(code)) return false;
			return attempt < 2;
		},
	});
}

export function selectConversationPages(data: InfiniteData<ConversationSnapshot>) {
	return mergeConversationPages(data.pages);
}

export function useConversation(sessionId: string | undefined, hostId?: string): ConversationQueryResult {
	const queryClient = useQueryClient();
	const refreshError = useQuery({
		queryKey: remoteConversationRefreshErrorKey(sessionId ?? "", hostId ?? ""),
		queryFn: async () => false,
		initialData: false,
		enabled: false,
	}).data;
	const query = useInfiniteQuery({
		...conversationQueryOptions(sessionId ?? "", hostId),
		enabled: Boolean(sessionId),
		select: selectConversationPages,
	});
	useEffect(() => {
		if (!hostId || !sessionId) return;
		const timer = window.setInterval(() => {
			void refreshRemoteConversation(queryClient, sessionId, hostId).catch(() => {});
		}, 2_000);
		return () => window.clearInterval(timer);
	}, [hostId, queryClient, sessionId]);

	if (query.error && (!hostId || !query.data)) {
		const code = apiErrorCode(query.error);
		// The session is currently owned by Terminal UI (or its Chat controller is
		// absent). A deliberate interface switch can change that later, but this
		// request cannot, so explain it rather than retrying.
		if (code === "SESSION_MODE_MISMATCH" || code === "CHAT_CONTROLLER_NOT_READY") {
			return {
				isLoading: false,
				unavailable: { code, message: apiErrorMessage(query.error) },
				hasOlder: false,
				isLoadingOlder: false,
				loadOlder: () => {},
			};
		}
		return {
			isLoading: false,
			error: apiErrorMessage(query.error),
			hasOlder: false,
			isLoadingOlder: false,
			loadOlder: () => {},
		};
	}

	return {
		snapshot: query.data,
		refreshError: hostId && (refreshError || query.error) ? "Could not load this remote conversation." : undefined,
		isLoading: query.isLoading,
		hasOlder: query.hasNextPage,
		isLoadingOlder: query.isFetchingNextPage,
		loadOlder: () => {
			void query.fetchNextPage();
		},
	};
}

/** Drops what was cached from a session's outgoing agent once it switched. */
export async function refreshAfterProviderSwitch(queryClient: QueryClient, sessionId: string, hostId?: string) {
	// Catalogs are keyed by Tallpine session id, so the outgoing agent's go before the new one can show them.
	clearConversationProviderCatalogs(queryClient, sessionId, hostId);
	await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
	await queryClient.invalidateQueries({ queryKey: conversationQueryKey(sessionId, hostId) });
	invalidateConversationProviderCatalogs(queryClient, sessionId, hostId);
}

/** Switches the session's agent, then drops what was cached from the outgoing one. */
export function useProviderSwitchCommand(sessionId: string, hostId?: string) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async (request: Omit<components["schemas"]["ProviderSwitchRequest"], "idempotencyKey">) => {
			const { data, error, response } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/provider-switch", {
				params: { path: { sessionId } },
				body: { ...request, idempotencyKey: crypto.randomUUID() },
			});
			if (error) throw switchOutcomeUnknown(response.status, error) ? error : new ProviderSwitchFailed(error);
			return data;
		},
		onSuccess: () => refreshAfterProviderSwitch(queryClient, sessionId, hostId),
		// With no clear answer the daemon may have switched, so what it runs is read back.
		onError: (error) => {
			if (!(error instanceof ProviderSwitchFailed)) void refreshAfterProviderSwitch(queryClient, sessionId, hostId).catch(() => {});
		},
	});
}
/**
 * The models the provider offers for this session.
 *
 * Fetched from the live conversation rather than a table in Tallpine, and only while a
 * session is open: the catalog depends on the account's entitlements, which the
 * provider knows and Tallpine does not.
 */
export function useConversationModels(sessionId: string | undefined, enabled: boolean, hostId?: string) {
	const query = useQuery({
		queryKey: conversationModelsQueryKey(sessionId ?? "", hostId),
		enabled: Boolean(sessionId) && enabled,
		// The catalog changes on the scale of provider releases, not turns.
		staleTime: 5 * 60 * 1000,
		retry: false,
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET(
				"/api/v1/sessions/{sessionId}/conversation/models",
				{
					params: { path: { sessionId: sessionId as string } },
				},
			);
			if (error) throw error;
			return (data?.models ?? []) as ChatModel[];
		},
	});
	return {
		// An empty list is a real answer: this agent offers no choice, so the picker
		// hides itself rather than showing an error the user cannot act on.
		models: query.data ?? [],
		isLoading: query.isLoading,
		/**
		 * The provider has answered, with models or an error. Until then a chat can
		 * show the saved catalog; afterwards the live answer decides, so an agent
		 * that cannot list or switch models never offers saved choices.
		 */
		loaded: query.isFetched,
	};
}

/**
 * Provider-owned controls advertised for this live session.
 *
 * Unlike Tallpine's durable turn settings, these are an ACP catalog whose values and
 * available choices may change after any selection (choosing a model can replace
 * the effort choices, for example). The daemon therefore returns the complete
 * catalog after every mutation and that response replaces the cache atomically.
 */
export function useConversationConfigOptions(sessionId: string | undefined, enabled: boolean, hostId?: string) {
	const queryClient = useQueryClient();
	const queryKey = conversationConfigOptionsQueryKey(sessionId ?? "", hostId);
	// Set for as long as a selection is being written. Cancelling in-flight reads
	// only closes half the race — without also holding the poll, the interval can
	// start a fresh read mid-write whose pre-change catalog lands after the
	// mutation's own result and reverts the picker the user just used.
	const [writing, setWriting] = useState(false);
	const query = useQuery({
		queryKey,
		enabled: Boolean(sessionId) && enabled,
		retry: false,
		// ACP can push a replacement catalog when one option changes. Until the
		// renderer consumes daemon change events, a light poll keeps those updates
		// visible without coupling them to conversation history polling.
		refetchInterval: writing ? false : CONFIG_OPTIONS_POLL_INTERVAL_MS,
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET(
				"/api/v1/sessions/{sessionId}/conversation/config-options",
				{
					params: { path: { sessionId: sessionId as string } },
				},
			);
			if (error) throw error;
			return (data?.options ?? []) as ChatConfigOption[];
		},
	});
	const mutation = useMutation({
		// Held across the whole write, paired with the cancel below: `onMutate`
		// runs before the request and `onSettled` after the result is committed,
		// so no poll can start or land inside that window.
		// The picked value shows at once; the agent's (possibly slow) reply brings what depends on it.
		onMutate: async ({ optionId, value }) => {
			setWriting(true);
			await queryClient.cancelQueries({ queryKey });
			const previous = queryClient.getQueryData<ChatConfigOption[]>(queryKey);
			queryClient.setQueryData<ChatConfigOption[]>(queryKey, previous?.map((option) => option.id !== optionId ? option
				: "value" in value ? { ...option, currentValue: value.value } : { ...option, currentBoolean: value.enabled }));
			return { previous, controllerEpoch: conversationProviderCatalogEpoch(queryClient, sessionUiKey(sessionId as string, hostId)) };
		},
		onError: (_error, _variables, context) => {
			// A switch since the pick replaced the list; the old agent's must not come back.
			if (!context?.previous || context.controllerEpoch !== conversationProviderCatalogEpoch(queryClient, sessionUiKey(sessionId as string, hostId))) return;
			queryClient.setQueryData(queryKey, context.previous);
		},
		onSettled: () => setWriting(false),
		mutationFn: async ({
			optionId,
			value,
		}: {
			optionId: string;
			value: ChatConfigOptionValue;
		}) => {
			const controllerEpoch = conversationProviderCatalogEpoch(queryClient, sessionUiKey(sessionId as string, hostId));
			// A read already in flight when the user picked would otherwise land
			// after this mutation's setQueryData and put the pre-change catalog
			// back, reverting the picker to the old value until the next poll.
			await queryClient.cancelQueries({ queryKey });
			const { data, error } = await clientForSessionHost(hostId).PATCH(
				"/api/v1/sessions/{sessionId}/conversation/config-options/{configId}",
				{
					params: {
						path: {
							sessionId: sessionId as string,
							configId: optionId,
						},
					},
					body: value,
					// An agent that never answers must not hold the settings locked.
					signal: AbortSignal.timeout(CONFIG_OPTION_WRITE_TIMEOUT_MS),
				},
			);
			if (error) throw error;
			return {
				controllerEpoch,
				options: (data?.options ?? []) as ChatConfigOption[],
			};
		},
		onSuccess: ({ controllerEpoch, options }) => {
			if (controllerEpoch !== conversationProviderCatalogEpoch(queryClient, sessionUiKey(sessionId as string, hostId))) {
				return;
			}
			queryClient.setQueryData(queryKey, options);
		},
	});

	return {
		options: query.data ?? [],
		loaded: query.isSuccess,
		setOption: async (optionId: string, value: ChatConfigOptionValue) =>
			(await mutation.mutateAsync({ optionId, value })).options,
		pending: mutation.isPending,
		error: mutation.error || query.error ? apiErrorMessage(mutation.error ?? query.error) : undefined,
	};
}

/**
 * The named skills this session's provider will accept.
 *
 * Read from the live conversation for the same reason the model catalog is: skills
 * come from the user's own agent config and from the repo's own files, so a list Tallpine
 * held would offer commands that no longer exist and hide ones just written.
 *
 * An empty list is a real answer and the composer depends on being able to tell it
 * from a failure — with no skills, `/` has to stay an ordinary character rather than
 * opening an empty menu.
 */
export function useConversationSkills(sessionId: string | undefined, enabled: boolean, hostId?: string) {
	const query = useQuery({
		queryKey: conversationSkillsQueryKey(sessionId ?? "", hostId),
		enabled: Boolean(sessionId) && enabled,
		// ACP agents publish this catalog asynchronously and may replace it later.
		// Polling also keeps Codex project skills current without introducing a
		// second renderer event channel solely for ephemeral provider metadata. The
		// catalog can be large and changes rarely, so it intentionally refreshes much
		// less often than conversation state.
		staleTime: SKILLS_POLL_INTERVAL_MS,
		// The catalog is only served once a live controller owns the session; before
		// then the daemon answers 409 CHAT_CONTROLLER_NOT_READY. A cached "ready"
		// snapshot can outlive the controller (a restart or interface switch), so a
		// mounted query keeps `enabled` true and would re-request the catalog on every
		// interval, turning one readiness conflict into a steady 409 stream. Stop the
		// poll while that conflict stands; readiness returning re-enables the query
		// (the controller-gated `enabled`) or invalidates it, which refetches once and
		// resumes polling on success.
		refetchInterval: (query) =>
			apiErrorCode(query.state.error) === "CHAT_CONTROLLER_NOT_READY"
				? false
				: SKILLS_POLL_INTERVAL_MS,
		retry: false,
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET(
				"/api/v1/sessions/{sessionId}/conversation/skills",
				{
					params: { path: { sessionId: sessionId as string } },
				},
			);
			if (error) throw error;
			return (data?.skills ?? []) as ChatSkill[];
		},
	});
	return { skills: query.data ?? [], isLoading: query.isLoading };
}

/**
 * Every path in the session worktree, for @-mention completion.
 *
 * The daemon already serves this list — tracked and untracked, non-ignored — so the
 * whole set is fetched once and filtered in the composer. That keeps a keystroke
 * from costing a round trip, which is what makes the menu feel like part of typing.
 * The endpoint caps itself, and `truncated` is respected rather than presented as a
 * complete list.
 */
export function useWorkspaceFilePaths(sessionId: string | undefined, enabled: boolean, hostId?: string) {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: hostId ? ["remote-workspace-file-paths", hostId, sessionId ?? ""] : ["workspace-file-paths", sessionId ?? ""],
		enabled: Boolean(sessionId) && enabled,
		// The agent edits files as it works, so this goes stale; refetched on demand
		// rather than polled, since a mention menu that is a minute out of date is
		// still useful and polling every session would not be.
		staleTime: 30 * 1000,
		// Local file changes arrive through the workspace event subscription below.
		// Remote sessions use their owning daemon instead of the local event stream.
		refetchInterval: hostId ? 30 * 1000 : false,
		retry: false,
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/files", {
				params: { path: { sessionId: sessionId as string } },
			});
			if (error) throw error;
			return {
				// A deleted path cannot be read, so offering it would insert a
				// reference the agent then fails to resolve.
				paths: (data?.files ?? [])
					.filter((file) => file.status !== "deleted")
					.map((file) => file.path),
				truncated: Boolean(data?.truncated),
			};
		},
	});
	useEffect(() => {
		if (!sessionId || !enabled || hostId) return;
		return subscribeWorkspaceFileChanges(sessionId, queryClient);
	}, [enabled, hostId, queryClient, sessionId]);
	return {
		paths: query.data?.paths ?? [],
		truncated: query.data?.truncated ?? false,
		isLoading: query.isLoading,
	};
}

/**
 * Write staged images into the session worktree.
 *
 * Returns the worktree-relative paths the agent can open. The composer names those
 * paths in the message it sends, which is how an image reaches an agent whose
 * conversation carries text: the same thing spawn does for the opening brief.
 */
export function useStageAttachments(sessionId: string | undefined, hostId?: string) {
	return useCallback(
		async (attachments: { mimeType: string; data: string }[]): Promise<string[]> => {
			if (!sessionId || attachments.length === 0) return [];
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/attachments", {
				params: { path: { sessionId } },
				body: { attachments },
			});
			if (error) throw error;
			return data?.paths ?? [];
		},
		[hostId, sessionId],
	);
}

/* -------------------------------------------------------------------------- */

