import { useRouter } from "expo-router";
import { useEffect } from "react";
import { Alert } from "react-native";
import { archiveSession } from "../archive-session";
import { haptics } from "../haptics";
import { chatSheetRoute, type ConversationActionsEntry } from "./chatSheetRegistry";
import { conversationActionUnsupported } from "./conversationErrors";
import { conversationMarkers } from "./timelineModel";
import { turnSettingsModelLabel } from "./turnSettingsModel";
import { can } from "./types";
import { useOpenPage } from "../pageNavigation";
import { reviewRouteForSession } from "../reviewView";
import { backOr } from "../backNavigation";
import { ARCHIVE_SESSION_CONFIRM } from "../worker-action-model";
import { type FilesNavigation } from "../files/navigation";
import { InterfaceTransitionRecheck } from "../session/useInterfaceTransition";
import { MobileChatSession } from "./ChatSessionScreen";
import { MobileConversation } from "./useConversation";
import { SessionInterfaceTransition } from "./api";
import { SessionInterfaceTransitionStatus } from "./api";
import { dismissKeyboardBeforeSheet } from "./dismissKeyboardBeforeSheet";

export function useConversationActionsMenu({
	conversation,
	menuOpen,
	setMenuOpen,
	session,
	actionsListeners,
	actionsEntryRef,
	sessionName,
	interfaceSwitch,
	interfaceTransitionActive,
	filesNavigation,
	router,
	setJumpToSequence,
	openShell,
	currentHostId,
	openPage,
	openTurnSettings,
	requestInterfaceSwitch,
	renameWorker,
	setWorkerPinned,
	kill,
	keyboardVisible,
	openingShell,
	title,
}: {
	conversation: MobileConversation;
	menuOpen: boolean;
	setMenuOpen: React.Dispatch<React.SetStateAction<boolean>>;
	session: MobileChatSession;
	actionsListeners: React.RefObject<Set<(entry: ConversationActionsEntry) => void>>;
	actionsEntryRef: React.RefObject<ConversationActionsEntry | undefined>;
	sessionName: string;
	interfaceSwitch: { status: SessionInterfaceTransitionStatus | undefined; transition: SessionInterfaceTransition | undefined; loading: boolean; starting: boolean; cancelling: boolean; acknowledgingNotice: boolean; error: string | undefined; acknowledgeNoticeError: string | undefined; fetchFailed: boolean; start: (targetMode: "chat" | "tui", policy: "drain" | "interrupt") => Promise<void>; cancel: () => Promise<void>; acknowledgeNotice: (transitionId: string) => Promise<void>; refresh: () => Promise<InterfaceTransitionRecheck | undefined>; };
	interfaceTransitionActive: boolean;
	filesNavigation: FilesNavigation;
	router: ReturnType<typeof useRouter>;
	setJumpToSequence: React.Dispatch<React.SetStateAction<number | undefined>>;
	openShell: () => Promise<void>;
	currentHostId: string | undefined;
	openPage: (href: Parameters<ReturnType<typeof useOpenPage>>[0]) => void;
	openTurnSettings: () => Promise<void>;
	requestInterfaceSwitch: () => void;
	renameWorker: (id: string, displayName: string, hostId?: string | undefined) => Promise<void>;
	setWorkerPinned: (id: string, pinned: boolean, hostId?: string | undefined) => Promise<void>;
	kill: (id: string, hostId?: string | undefined) => Promise<void>;
	keyboardVisible: boolean;
	openingShell: boolean;
	title: string;
}) {

	useEffect(() => {
		const current = conversation.snapshot;
		if (!menuOpen || !current) return;
		setMenuOpen(false);
		const entry: ConversationActionsEntry = {
			kind: "conversation-actions",
			sessionId: session.id,
			snapshot: current,
			subscribeEntry: (listener) => {
				actionsListeners.current.add(listener);
				listener(actionsEntryRef.current ?? entry);
				return () => { actionsListeners.current.delete(listener); };
			},
			sessionTitle: sessionName,
			modelLabel: turnSettingsModelLabel(current, conversation.models, conversation.configOptions),
			openingShell,
			compacting: conversation.pendingActions.includes("compact"),
			mcpReloading: conversation.pendingActions.includes("mcp"),
			refreshing: conversation.refreshing,
			compactSupported: can(current, "compaction") && !conversationActionUnsupported("compact", conversation.actionCodes.compact),
			mcpReloadSupported: can(current, "mcp_reload") && !conversationActionUnsupported("mcp", conversation.actionCodes.mcp),
			interfaceSupported: Boolean(interfaceSwitch.status?.supported),
			interfaceReason: interfaceSwitch.status?.reason || interfaceSwitch.error,
			interfaceSwitching: interfaceTransitionActive || interfaceSwitch.starting,
			// Orchestrators are not deleted from here; the board owns their lifecycle.
			canDelete: !("projectName" in session),
			// The daemon refuses to rename the project orchestrator.
			canRename: !("projectName" in session),
			canPin: !("projectName" in session),
			pinned: "projectName" in session ? false : Boolean(session.isPinned),
			onFiles: filesNavigation.openChanges,
			onMap: () => router.push(chatSheetRoute({ kind: "conversation-map", markers: conversationMarkers(actionsEntryRef.current?.snapshot ?? current), onSelect: setJumpToSequence })),
			onOpenShell: () => void openShell(),
			onPreview: () => router.push({ pathname: "/preview/[id]", params: { id: session.id, title, hostId: currentHostId } }),
			onPullRequests: () => {
				const route = !("projectName" in session) && (session.prs?.length ?? (session.pr ? 1 : 0)) <= 1
					? reviewRouteForSession(session, currentHostId)
					: undefined;
				if (route) openPage(route);
				else router.push({ pathname: "/(tabs)/prs", params: { hostId: currentHostId, projectId: session.projectId } });
			},
			artifactCount: "projectName" in session ? undefined : session.artifacts?.length,
			onArtifacts: () => router.push({ pathname: "/artifacts/[sessionId]", params: { sessionId: session.id, title, hostId: currentHostId } }),
			onSettings: () => void openTurnSettings(),
			onSwitchInterface: requestInterfaceSwitch,
			onCompact: () => void conversation.compact().catch(() => {}),
			onReload: () => void conversation.reloadMcp().catch(() => {}),
			onRename: () => router.push(chatSheetRoute({
				kind: "conversation-rename",
				initialTitle: sessionName,
				onRename: (next) => renameWorker(session.id, next),
			})),
			onTogglePin: () => {
				if ("projectName" in session) return;
				void setWorkerPinned(session.id, !session.isPinned).catch(() => {});
			},
			onRefresh: () => void conversation.refresh(),
			onDelete: () => {
				haptics.warning();
				Alert.alert(
					ARCHIVE_SESSION_CONFIRM.title,
					ARCHIVE_SESSION_CONFIRM.message,
					[
						{ text: ARCHIVE_SESSION_CONFIRM.cancel, style: "cancel" },
						// Leave first: the session this screen is showing is about to stop
						// existing, and the board is where its row disappears from.
						{ text: ARCHIVE_SESSION_CONFIRM.confirm, style: "destructive", onPress: () => { backOr(router); void archiveSession(() => kill(session.id), Alert.alert); } },
					],
				);
			},
		};
		actionsEntryRef.current = entry;
		void dismissKeyboardBeforeSheet(keyboardVisible).then(() => router.push(chatSheetRoute(actionsEntryRef.current ?? entry)));
	}, [currentHostId, conversation, filesNavigation, interfaceSwitch, interfaceTransitionActive, keyboardVisible, menuOpen, openPage, openShell, openTurnSettings, openingShell, requestInterfaceSwitch, router, session, sessionName, setWorkerPinned, title]);

}
