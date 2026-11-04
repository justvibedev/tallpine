/* Side-chat transcript wiring for the main chat Timeline, split out of useSideChats (B303). */

import { useCallback, useMemo, type MutableRefObject } from "react";
import type { ChatAnnotationSummaryItem } from "../../components/chat/ChatAnnotationSummary";
import { sideConversationSnapshot } from "../../components/chat/sideConversation";
import type { Snapshot } from "../../lib/side-chats-model";

export function useSideChatTimeline({
	activeId, snapshot, olderPages, onNavigate, timelineNavigationRef,
}: {
	activeId: string | undefined;
	snapshot: Snapshot | undefined;
	olderPages: Snapshot[];
	onNavigate?: (annotation: ChatAnnotationSummaryItem) => void;
	timelineNavigationRef: MutableRefObject<((annotation: ChatAnnotationSummaryItem) => void) | null>;
}) {
	// The main chat's Timeline renders the side transcript, so live status,
	// "Worked for", and scroll follow behave exactly as they do in the main chat.
	// Stable per side: the Timeline's draft effects depend on this identity.
	const timelineDraftScope = useMemo(
		() => (activeId ? { sessionId: `side:${activeId}`, incarnation: activeId } : undefined),
		[activeId],
	);
	const conversation = useMemo(
		() => (snapshot && snapshot.side.id === activeId ? sideConversationSnapshot(snapshot, olderPages) : undefined),
		[snapshot, olderPages, activeId],
	);
	const navigateAnnotation = useCallback(
		(annotation: ChatAnnotationSummaryItem) => {
			if (annotation.conversationId === activeId) timelineNavigationRef.current?.(annotation);
			else onNavigate?.(annotation);
		},
		[activeId, onNavigate],
	);
	return { timelineDraftScope, conversation, navigateAnnotation };
}
