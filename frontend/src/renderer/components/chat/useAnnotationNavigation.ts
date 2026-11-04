import { useCallback, useEffect, useRef, useState, type MutableRefObject } from "react";
import { annotationBody, annotationTextMatches } from "../../lib/chat-annotation-navigation";
import { type ConversationSnapshot } from "../../types/conversation";
import { RefObject } from "react";

/**
 * State and entry point for jumping to a quoted annotation in the transcript.
 */
export function useAnnotationNavigation({
	onForeignAnnotation,
	snapshot,
	releaseFollow,
	scrollContent,
	annotationNavigationRef,
}: {
	onForeignAnnotation: ((annotation: { text: string; messageId?: string; revision?: number; conversationId?: string; }) => void) | undefined;
	snapshot: ConversationSnapshot;
	releaseFollow: () => void;
	scrollContent: RefObject<HTMLDivElement | null>;
	annotationNavigationRef: MutableRefObject<((annotation: { text: string; messageId?: string; revision?: number; }) => void) | null>;
}) {
	const [annotationTarget, setAnnotationTarget] = useState<{ text: string; messageId?: string; revision?: number }>();

	const [annotationNavigationError, setAnnotationNavigationError] = useState<string>();

	const annotationHighlightCleanup = useRef<(() => void) | undefined>(undefined);

	const annotationHighlightTimer = useRef<number | undefined>(undefined);

	const annotationRetryCount = useRef(0);

	// Group count when navigation last asked for older history. A finished load
	// that adds nothing (a failed page or a source outside this branch) must end
	// the search instead of requesting the same page again.
	const annotationOlderRequestGroups = useRef<number | undefined>(undefined);

	const selectAnnotation = useCallback((annotation: { text: string; messageId?: string; revision?: number; conversationId?: string }) => {
		if (onForeignAnnotation && annotation.conversationId && annotation.conversationId !== snapshot.conversationId) {
			// The source lives in another conversation (a side chat quoting the main
			// chat); its host navigates there instead of searching this transcript.
			onForeignAnnotation(annotation);
			return;
		}
		releaseFollow();
		setAnnotationNavigationError(undefined);
		annotationRetryCount.current = 0;
		annotationOlderRequestGroups.current = undefined;
		if (!annotation.messageId) {
			const source = Array.from(scrollContent.current?.querySelectorAll<HTMLElement>("[data-chat-message-id]") ?? [])
				.find((element) => annotationTextMatches(annotationBody(element), annotation.text));
			if (source) {
				setAnnotationTarget({ ...annotation, messageId: source.dataset.chatMessageId });
				return;
			}
			setAnnotationNavigationError("This annotation's source is unavailable. Reselect the text to create a new reference.");
			return;
		}
		setAnnotationTarget({ ...annotation });
	}, [onForeignAnnotation, releaseFollow, snapshot.conversationId]);

	useEffect(() => {
		annotationNavigationRef.current = selectAnnotation;
		return () => { annotationNavigationRef.current = null; };
	}, [annotationNavigationRef, selectAnnotation]);

	useEffect(() => () => {
		window.clearTimeout(annotationHighlightTimer.current);
		annotationHighlightCleanup.current?.();
	}, []);

	return {
		annotationTarget,
		setAnnotationTarget,
		annotationNavigationError,
		setAnnotationNavigationError,
		annotationHighlightCleanup,
		annotationHighlightTimer,
		annotationRetryCount,
		annotationOlderRequestGroups,
		selectAnnotation,
	};
}
