import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert } from "react-native";
import { restoreSession, resumeSessionAgent } from "../api";
import { shouldAutoResume } from "./autoResume";
import type { TimelineStartup } from "./ChatTimeline";
import { sessionStartupView, startupVisible } from "../session/sessionStartup";
import { startSerialPoll } from "../session/serialPoll";
import { STARTUP_POLL_MS } from "../session/startupPoll";
import { userFacingError, NOT_PAIRED_ACTION_COPY } from "../connectionError";
import { MobileChatSession } from "./ChatSessionScreen";
import { MobileConversation } from "./useConversation";
import { ServerConfig } from "../config";

export function useChatStartup({
	resuming,
	setResuming,
	setResumeError,
	config,
	terminated,
	session,
	refreshBoard,
	conversation,
	refreshConversation,
}: {
	resuming: boolean;
	setResuming: React.Dispatch<React.SetStateAction<boolean>>;
	setResumeError: React.Dispatch<React.SetStateAction<string | undefined>>;
	config: ServerConfig | null;
	terminated: boolean;
	session: MobileChatSession;
	refreshBoard: () => Promise<void>;
	conversation: MobileConversation;
	refreshConversation: () => Promise<void>;
}) {

	const resume = useCallback(async (quiet = false) => {
		if (resuming) return;
		setResuming(true);
		setResumeError(undefined);
		try {
			if (!config) throw new Error(NOT_PAIRED_ACTION_COPY);
			if (terminated) await restoreSession(config, session.id);
			else await resumeSessionAgent(config, session.id);
			await refreshBoard();
			await conversation.refresh();
		} catch (cause) {
			// An automatic attempt has no alert; the stopped banner carries the reason.
			if (quiet) setResumeError(userFacingError(cause));
			else Alert.alert("Couldn't resume the agent", userFacingError(cause));
		} finally { setResuming(false); }
	}, [config, conversation.refresh, refreshBoard, resuming, session.id, terminated]);

	const startupView = useMemo(
		() => ("projectName" in session ? undefined : sessionStartupView(session, conversation.snapshot)),
		[conversation.snapshot, session],
	);
	const startupShown = startupVisible(startupView, conversation.snapshot);
	// The agent coming up is not a conversation event, so over the direct link
	// nothing tells the screen. With an opening brief the turn's own events cover
	// it; a promptless start stayed on "Connecting to the agent…". Once
	// provisioning settles, re-read the conversation until it reports the agent
	// connected, so one failed read cannot leave the screen waiting.
	const provisionState = "projectName" in session ? undefined : session.provisionState;
	const previousProvisionState = useRef(provisionState);
	const [awaitingAgent, setAwaitingAgent] = useState(false);
	useEffect(() => {
		const before = previousProvisionState.current;
		previousProvisionState.current = provisionState;
		if (before === "provisioning" && provisionState === "ready") setAwaitingAgent(true);
	}, [provisionState]);
	const agentConnecting = conversation.snapshot?.controller.state === "connecting";
	const hasSnapshot = Boolean(conversation.snapshot);
	useEffect(() => {
		if (!awaitingAgent) return;
		if (hasSnapshot && !agentConnecting) {
			setAwaitingAgent(false);
			return;
		}
		// One read at a time: the refresh keeps only its newest request, so a
		// fixed interval faster than the link discards every response.
		return startSerialPoll(refreshConversation, STARTUP_POLL_MS);
	}, [agentConnecting, awaitingAgent, hasSnapshot, refreshConversation]);
	const retryStart = useCallback(() => void resume(), [resume]);
	const timelineStartup = useMemo<TimelineStartup | undefined>(
		() => (startupView ? { view: startupView, visible: startupShown, onRetry: retryStart, retrying: resuming } : undefined),
		[resuming, retryStart, startupShown, startupView],
	);

	// The daemon leaves agents stopped after a restart; opening the session here
	// starts them, as the desktop does. One attempt per opening: a failure or a
	// later exit stays stopped behind the Resume banner, which shows the reason.
	const autoResumeTried = useRef<string | undefined>(undefined);
	useEffect(() => {
		if (autoResumeTried.current === session.id) return;
		if (!shouldAutoResume(session, terminated, Boolean(config))) return;
		autoResumeTried.current = session.id;
		void resume(true);
	}, [config, resume, session, terminated]);

	return { resume, startupView, startupShown, timelineStartup };
}
