import { useRouter } from "expo-router";
import { useCallback, useState } from "react";
import { Alert } from "react-native";
import { mobileReachablePreviewURL, type DashboardSession } from "../api";
import { openGitHub } from "../openGitHub";
import { getWorkspacePaths, openSessionShell } from "./api";
import { chatSheetRoute } from "./chatSheetRegistry";
import { conversationActionError } from "./conversationErrors";
import { userFacingError } from "../connectionError";
import { sessionLink } from "./automationMessages";
import { readSession } from "./sessionLookup";
import { MobileChatSession } from "./ChatSessionScreen";
import { MobileConversation } from "./useConversation";
import { ServerConfig } from "../config";
import { dismissKeyboardBeforeSheet } from "./dismissKeyboardBeforeSheet";

export function useChatOpeners({
	config,
	conversation,
	filePaths,
	filePathsTruncated,
	filePathsRequest,
	session,
	setFilePaths,
	setFilePathsTruncated,
	keyboardVisible,
	router,
	openingShell,
	setOpeningShell,
	setMenuOpen,
	currentHostId,
	sessions,
}: {
	config: ServerConfig | null;
	conversation: MobileConversation;
	filePaths: string[];
	filePathsTruncated: boolean;
	filePathsRequest: React.RefObject<Promise<{ paths: string[]; truncated: boolean; }> | null>;
	session: MobileChatSession;
	setFilePaths: React.Dispatch<React.SetStateAction<string[]>>;
	setFilePathsTruncated: React.Dispatch<React.SetStateAction<boolean>>;
	keyboardVisible: boolean;
	router: ReturnType<typeof useRouter>;
	openingShell: boolean;
	setOpeningShell: React.Dispatch<React.SetStateAction<boolean>>;
	setMenuOpen: React.Dispatch<React.SetStateAction<boolean>>;
	currentHostId: string | undefined;
	sessions: DashboardSession[];
}) {

	const loadWorkspaceFiles = useCallback(async () => {
		if (!config || !conversation.snapshot) return { paths: filePaths, truncated: filePathsTruncated };
		if (filePathsRequest.current) return filePathsRequest.current;
		const request = getWorkspacePaths(config, session.id)
			.then((result) => {
				setFilePaths(result.paths);
				setFilePathsTruncated(result.truncated);
				return result;
			})
			.catch(() => ({ paths: filePaths, truncated: filePathsTruncated }))
			.finally(() => { filePathsRequest.current = null; });
		filePathsRequest.current = request;
		return request;
	}, [config, conversation.snapshot, filePaths, filePathsTruncated, session.id]);

	const openTurnSettings = useCallback(async () => {
		const current = conversation.snapshot;
		if (!current) return;
		await dismissKeyboardBeforeSheet(keyboardVisible);
		let catalog = { models: conversation.models, configOptions: conversation.configOptions };
		let catalogError = conversation.actionErrors.settings ?? conversation.actionErrors.config;
		try {
			catalog = await conversation.loadTurnOptions();
		} catch (cause) {
			catalogError = conversationActionError(cause);
		}
		router.push(chatSheetRoute({
			kind: "turn-settings",
			snapshot: current,
			models: catalog.models,
			options: catalog.configOptions,
			disabled: current.controller.state === "stopped" || conversation.pendingActions.includes("settings") || conversation.pendingActions.includes("config"),
			error: catalogError,
			onSettings: conversation.chooseSettings,
			onOption: conversation.setConfigOption,
			onRefresh: () => conversation.loadTurnOptions({ refresh: true }),
		}));
	}, [conversation, keyboardVisible, router]);

	const openShell = useCallback(async () => {
		if (!config || openingShell) return;
		setOpeningShell(true);
		try {
			const shell = await openSessionShell(config, session.id, session.projectId);
			setMenuOpen(false);
			router.push({ pathname: "/shell/[handleId]", params: { handleId: shell.handleId, projectId: session.projectId, sessionId: session.id, title: shell.title, hostId: config.hostId } });
		} catch (cause) {
			Alert.alert("Couldn't open shell", userFacingError(cause));
		} finally { setOpeningShell(false); }
	}, [config, openingShell, router, session.id, session.projectId]);

	// Links open in-app, like the desktop Browser inspector. A localhost link from
	// the agent's machine is mapped onto the Tallpine host first (the phone's own
	// localhost is never the dev server the agent named); openGitHub then applies
	// the package's rule for a web page - GitHub app when it has a screen for it,
	// the in-app browser otherwise.
	// An `ao://sessions/…` link opens that session's chat on this conversation's machine.
	const openLink = useCallback((url: string) => {
		const target = sessionLink(url);
		if (target) {
			router.push({ pathname: "/session/[id]", params: { id: target.sessionId, projectId: target.projectId, hostId: currentHostId } });
			return;
		}
		void openGitHub(mobileReachablePreviewURL(url, config?.host ?? "")?.href ?? url);
	}, [config?.host, currentHostId, router]);
	// A session the board leaves out (archived, say) is read once for its name.
	const [readSessionNames, setReadSessionNames] = useState<Readonly<Record<string, string>>>({});
	const nameOfSession = useCallback((sessionId: string) => sessions.find((candidate) => candidate.id === sessionId)?.displayName || readSessionNames[sessionId] || undefined, [readSessionNames, sessions]);
	const wantSessionName = useCallback((sessionId: string) => {
		if (!config) return;
		void readSession(config, sessionId).then((read) => {
			const name = read?.displayName;
			if (name) setReadSessionNames((current) => current[sessionId] === name ? current : { ...current, [sessionId]: name });
		});
	}, [config]);

	return { loadWorkspaceFiles, openTurnSettings, openShell, openLink, nameOfSession, wantSessionName };
}
