/* Side-chat drafts, delivery and tab actions, split out of chat/IndependentSideChats.tsx (B298, P56). */

import { useEffect, useRef, useState } from "react";
import type { ChatAnnotationSummaryItem } from "../../components/chat/ChatAnnotationSummary";
import { useSideChatDrafts } from "./useSideChatDrafts";
import { useSideChatLifecycle } from "./useSideChatLifecycle";
import { useSideChatRequests } from "./useSideChatRequests";
import { useSideChatRename } from "./useSideChatRename";
import { useSideChatTimeline } from "./useSideChatTimeline";
import { useSideChatSend } from "./useSideChatSend";
import { useSideChatsData } from "./useSideChatsData";

export interface UseSideChatsOptions {
	sessionId: string;
	stageAttachments: (items: { mimeType: string; data: string }[]) => Promise<string[]>;
	nativeImages: boolean;
	enabled: boolean;
	onNavigate?: (annotation: ChatAnnotationSummaryItem) => void;
	canCreate: boolean;
}

export function useSideChats({
	sessionId,
	stageAttachments,
	nativeImages,
	enabled,
	onNavigate,
	canCreate,
}: UseSideChatsOptions) {
	const sessionRef = useRef(sessionId);
	sessionRef.current = enabled ? sessionId : "";
	const closedSidesRef = useRef(new Set<string>());
	const [error, setError] = useState<string>();
	const {
		sides,
		setSides,
		activeId,
		setActiveId,
		visible,
		setVisible,
		snapshot,
		setSnapshot,
		setSnapshotRefreshKey,
		olderPages,
		loadingOlder,
		loadOlder,
		refreshList,
	} = useSideChatsData({ sessionId, enabled, sessionRef, closedSidesRef, setError });
	const activeIdRef = useRef(activeId);
	activeIdRef.current = activeId;
	const {
		drafts,
		setDrafts,
		draftsRef,
		attachments,
		setAttachments,
		focusKey,
		setFocusKey,
		updateDraft,
		replaceDraft,
		addReference,
		attachFiles,
	} =
		useSideChatDrafts({ sessionId, sessionRef, closedSidesRef, activeId, activeIdRef, stageAttachments, setError });
	const rename = useSideChatRename({ sessionId, sides, setError, refreshList });
	const { renameText, setRenameText, renameId, setRenameId, renameCancelled } = rename;
	const [confirmClose, setConfirmClose] = useState<string>();
	const [closing, setClosing] = useState(false);
	const [closeError, setCloseError] = useState<string>();
	// The side transcript renders through the main chat's Timeline, which owns
	// selection, source navigation, and scroll follow. Same-side annotations from
	// the composer navigate through it.
	const timelineNavigationRef = useRef<((annotation: ChatAnnotationSummaryItem) => void) | null>(null);
	const [pending, setPending] = useState(false);
	const [, setSendingRevision] = useState(0);
	const sendingRef = useRef(new Set<string>());
	const sending = Boolean(activeId && sendingRef.current.has(activeId));
	const [editingTurnId, setEditingTurnId] = useState<string>();
	const [editingText, setEditingText] = useState("");
	const [inputAnswers, setInputAnswers] = useState<Record<string, Record<string, string>>>({});
	useEffect(() => {
		setDrafts({});
		setAttachments({});
		setRenameId(undefined);
		setRenameText(undefined);
		setConfirmClose(undefined);
		setCloseError(undefined);
		setError(undefined);
	}, [sessionId, enabled]);

	const { create, close } = useSideChatLifecycle({
		sessionId, enabled, canCreate, sessionRef, closedSidesRef, setError, setPending,
		sides, setSides, activeId, setActiveId, setVisible, setFocusKey, setSnapshot, setDrafts, setAttachments, addReference,
	});
	const send = useSideChatSend({
		sessionId, nativeImages, stageAttachments, sessionRef, closedSidesRef, sendingRef, setSendingRevision,
		setError, create, updateDraft, draftsRef, setDrafts, attachments, setAttachments,
	});
	const { interrupt, compact, resolveApproval, resolveInput, updateSettings, retry, saveQueuedEdit } = useSideChatRequests({
		sessionId, setError, updateDraft, inputAnswers, editingTurnId, editingText, setEditingTurnId,
	});
	const currentActiveId = sides.some((side) => side.id === activeId && side.sessionId === sessionId)
		? activeId
		: undefined;
	const { timelineDraftScope, conversation, navigateAnnotation } = useSideChatTimeline({ activeId, snapshot, olderPages, onNavigate, timelineNavigationRef });
	const { beginRename, saveRename } = rename;

	return {
		activeId,
		addReference,
		attachFiles,
		attachments,
		beginRename,
		close,
		closeError,
		closedSidesRef,
		closing,
		compact,
		confirmClose,
		conversation,
		create,
		currentActiveId,
		drafts,
		editingText,
		editingTurnId,
		error,
		focusKey,
		inputAnswers,
		interrupt,
		loadOlder,
		loadingOlder,
		navigateAnnotation,
		pending,
		renameCancelled,
		renameId,
		renameText,
		replaceDraft,
		resolveApproval,
		resolveInput,
		retry,
		saveQueuedEdit,
		saveRename,
		send,
		sending,
		sendingRef,
		sessionRef,
		setActiveId,
		setAttachments,
		setCloseError,
		setClosing,
		setConfirmClose,
		setDrafts,
		setEditingText,
		setEditingTurnId,
		setError,
		setFocusKey,
		setInputAnswers,
		setRenameId,
		setRenameText,
		setSnapshotRefreshKey,
		setVisible,
		sides,
		snapshot,
		timelineDraftScope,
		timelineNavigationRef,
		updateDraft,
		updateSettings,
		visible,
	};
}
