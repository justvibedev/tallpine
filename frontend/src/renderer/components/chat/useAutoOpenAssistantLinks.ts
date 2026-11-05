import { useEffect, useRef } from "react";
import { isWebLink, isWorkspaceHtmlLink } from "../../lib/external-link-policy";
import type { ConversationSnapshot } from "../../types/conversation";
import { isOrchestratorSession, type WorkspaceSession } from "../../types/workspace";

const HTTP_LINK_PATTERN = /https?:\/\/[^\s<>()\[\]{}"']+/i;
const autoOpenedLinkSessions = new Set<string>();

interface AssistantLinkState {
	revision: number;
	sequence: number;
	streaming: boolean;
}

interface ConversationLinkBaseline {
	latestSequence: number;
	messages: Map<string, AssistantLinkState>;
	pendingCompleted: Map<string, number>;
}

function cleanExtractedLink(value: string): string {
	return value.replace(/[.,!?;:`\\]+$/, "");
}

function firstBrowserLink(text: string, workspacePaths: string[]): string | undefined {
	const candidates: Array<{ index: number; value: string }> = [];
	const webMatch = HTTP_LINK_PATTERN.exec(text);
	if (webMatch) candidates.push({ index: webMatch.index, value: cleanExtractedLink(webMatch[0]) });
	const markdownLink = /\[[^\]]+\]\(([^)\s]+)\)/.exec(text);
	if (markdownLink?.[1]) candidates.push({ index: markdownLink.index, value: cleanExtractedLink(markdownLink[1]) });
	for (const candidate of candidates.sort((a, b) => a.index - b.index)) {
		if (isWebLink(candidate.value) || isWorkspaceHtmlLink(candidate.value, workspacePaths)) return candidate.value;
	}
	return undefined;
}

/** Only worker chats auto-open links; an orchestrator's Browser shows only pages the user opens. */
export function autoOpensAssistantLinks(session: Pick<WorkspaceSession, "id" | "kind">): boolean {
	return !isOrchestratorSession(session);
}

/**
 * Opens the first browser link of a newly completed assistant reply, once per session.
 * `enabled` is false for orchestrators: their replies cite PR and release links the
 * in-app Browser (no login) shows as a 404, and entering the chat must never pop it open.
 */
export function useAutoOpenAssistantLinks({ snapshot, isLoading, uiSessionId, paths, autoOpenLinkInBrowser, enabled = true }: {
	snapshot: ConversationSnapshot | undefined;
	isLoading: boolean;
	uiSessionId: string;
	paths: string[];
	autoOpenLinkInBrowser: (url: string) => void;
	enabled?: boolean;
}) {
	const conversationLinkBaselines = useRef(new Map<string, ConversationLinkBaseline>());
	useEffect(() => {
		if (!enabled || !snapshot || isLoading) return;
		const previous = conversationLinkBaselines.current.get(uiSessionId);
		const isInitialSnapshot = !previous;
		const latestUserMessage = snapshot.items
			.filter((item) => item.kind === "message" && item.role === "user")
			.at(-1);
		const messages = new Map<string, AssistantLinkState>();
		const pendingCompleted = new Map(previous?.pendingCompleted);
		let latestSequence = Math.max(previous?.latestSequence ?? -1, snapshot.latestSequence);
		for (const item of snapshot.items) {
			latestSequence = Math.max(latestSequence, item.sequence);
			if (item.kind !== "message" || item.role !== "assistant") continue;
			const prior = previous?.messages.get(item.id);
			messages.set(item.id, {
				revision: item.revision,
				sequence: item.sequence,
				streaming: item.streaming,
			});
			if (item.streaming) {
				pendingCompleted.delete(item.id);
				continue;
			}
			const completedCurrentTurnOnMount =
				isInitialSnapshot && latestUserMessage && item.sequence > latestUserMessage.sequence;
			const newlyCompleted = previous
				? prior
					? prior.streaming && item.revision >= prior.revision
					: item.sequence > previous.latestSequence
				: completedCurrentTurnOnMount;
			if (newlyCompleted) pendingCompleted.set(item.id, item.revision);
			else if (pendingCompleted.get(item.id) !== item.revision) pendingCompleted.delete(item.id);
		}
		conversationLinkBaselines.current.set(uiSessionId, { latestSequence, messages, pendingCompleted });
		if (autoOpenedLinkSessions.has(uiSessionId)) return;
		// Do not surprise users by opening links from history when a session is first
		// mounted. The exception is the current turn: a fast agent can finish before
		// the first conversation request resolves, so its response is already present
		// in the initial snapshot and must not be mistaken for old history.
		for (const item of snapshot.items) {
			if (
				item.kind !== "message" ||
				item.role !== "assistant" ||
				item.streaming ||
				pendingCompleted.get(item.id) !== item.revision
			) continue;
			const url = firstBrowserLink(item.text, paths);
			if (url) {
				autoOpenedLinkSessions.add(uiSessionId);
				pendingCompleted.delete(item.id);
				autoOpenLinkInBrowser(url);
				break;
			}
		}
	}, [autoOpenLinkInBrowser, enabled, isLoading, paths, snapshot, uiSessionId]);
}
