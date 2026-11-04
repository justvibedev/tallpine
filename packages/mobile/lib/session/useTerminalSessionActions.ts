import { useCallback, type MutableRefObject } from "react";
import { Alert, Platform } from "react-native";
import { ApiError, killSession, killSessionReviewer, sendMessage } from "../api";
import { closeShellTerminal } from "../chat/api";
import { userFacingError } from "../connectionError";
import { haptics } from "../haptics";
import { ARCHIVE_SESSION_CONFIRM } from "../worker-action-model";
import { REROUTED_NOTICE, routeForSend, terminalPayload, TERMINAL_MODE_NOTICE, TERMINAL_UNAVAILABLE_NOTICE, type SendTarget } from "./sendRoute";
import type { MuxClient, MuxStatus } from "../mux";
import type { ServerConfig } from "../config";

/** Send, archive/close and restore actions of the terminal screen. */
export function useTerminalSessionActions({
	activeConfig, id, terminalHandleId, projectId, sessionId, reviewerPane, shellOnly, leave, restore,
	msg, setMsg, sendTarget, setSendTarget, setSending, setRestoring, status, setBanner, setNotFound,
	muxRef, openedRef, lastDimsRef,
}: {
	activeConfig: ServerConfig | null | undefined;
	id: string;
	terminalHandleId: string;
	projectId: string | undefined;
	sessionId: string | undefined;
	reviewerPane: boolean;
	shellOnly: boolean;
	leave: () => void;
	restore: (id: string) => Promise<unknown>;
	msg: string;
	setMsg: (m: string) => void;
	sendTarget: SendTarget;
	setSendTarget: (t: SendTarget) => void;
	setSending: (b: boolean) => void;
	setRestoring: (b: boolean) => void;
	status: MuxStatus;
	setBanner: (b: string | null) => void;
	setNotFound: (b: boolean) => void;
	muxRef: MutableRefObject<MuxClient | null>;
	openedRef: MutableRefObject<boolean>;
	lastDimsRef: MutableRefObject<{ cols: number; rows: number } | null>;
}) {
	// Send the composed text to the selected route. The agent route can still
	// auto-engage the terminal route when the daemon reports a blocked prompt.
	//
	// Tallpine's /send is the right route for a message: the daemon hands it to the
	// harness and submits it. But it sanitises control characters and refuses
	// outright while a session is paused on a permission prompt — answering 409
	// SESSION_AWAITING_DECISION with the advice "answer it in the session
	// terminal first". So on exactly that code we do what it says and write the
	// line to the PTY instead. `shouldRetryOnTerminal` keeps that narrow: a
	// terminated or exited session has no PTY, and rerouting there would swallow
	// the user's text and report success.
	//
	// The reroute is announced in the banner rather than done silently, and the
	// text stays in the field on any failure we did NOT handle, so nothing typed
	// is ever lost.
	const sendPrompt = useCallback(async () => {
		const text = msg.trim();
		if (!text) return;
		if (routeForSend(sendTarget) === "terminal") {
			if (muxRef.current && status === "open") {
				muxRef.current.sendInput(terminalHandleId, terminalPayload(text), projectId);
				haptics.success();
				setMsg("");
				setBanner(TERMINAL_MODE_NOTICE);
			} else {
				haptics.error();
				setBanner(TERMINAL_UNAVAILABLE_NOTICE);
			}
			return;
		}
		setSending(true);
		try {
			if (!activeConfig) throw new Error("No active computer connection");
			await sendMessage(activeConfig, id, text);
			haptics.success();
			setMsg("");
		} catch (e) {
			const failure = e instanceof ApiError ? e : null;
			// Only reroute onto a mux we actually hold open — otherwise the write is
			// a no-op and we would clear the field having sent nothing.
			if (routeForSend(sendTarget, failure) === "terminal" && muxRef.current && status === "open") {
				muxRef.current.sendInput(terminalHandleId, terminalPayload(text), projectId);
				haptics.success();
				setMsg("");
				setSendTarget("terminal");
				setBanner(REROUTED_NOTICE);
			} else {
				haptics.error();
				setBanner(`Message not sent. ${userFacingError(e)}`);
			}
		} finally {
			setSending(false);
		}
	}, [msg, sendTarget, activeConfig, id, terminalHandleId, projectId, status]);

	const confirmKill = useCallback(() => {
		const doKill = async () => {
			try {
				if (!activeConfig) throw new Error("No active computer connection");
				if (reviewerPane) await killSessionReviewer(activeConfig, String(sessionId));
				else if (shellOnly) await closeShellTerminal(activeConfig, id);
				else await killSession(activeConfig, id);
				haptics.success();
				leave();
			} catch (e) {
				haptics.error();
				setBanner(`${reviewerPane ? "Couldn't stop the reviewer" : shellOnly ? "Couldn't close the shell" : "Couldn't archive the session"}. ${userFacingError(e)}`);
			}
		};
		if (Platform.OS === "web") {
			doKill();
			return;
		}
		if (reviewerPane) {
			haptics.warning();
			Alert.alert("Stop reviewer session?", "This closes the persistent reviewer and cancels any review it is currently running. Review history is preserved.", [
				{ text: "Keep reviewer", style: "cancel" },
				{ text: "Stop reviewer", style: "destructive", onPress: doKill },
			]);
			return;
		}
		haptics.warning();
		Alert.alert(shellOnly ? "Close shell?" : ARCHIVE_SESSION_CONFIRM.title, shellOnly ? "This stops the worktree shell." : ARCHIVE_SESSION_CONFIRM.message, [
			{ text: shellOnly ? "Cancel" : ARCHIVE_SESSION_CONFIRM.cancel, style: "cancel" },
			{ text: shellOnly ? "Close" : ARCHIVE_SESSION_CONFIRM.confirm, style: "destructive", onPress: doKill },
		]);
	}, [activeConfig, id, leave, sessionId, reviewerPane, shellOnly]);

	// Restore a terminated session: the daemon re-attaches its worktree agent and
	// its PTY comes back, so we re-open the terminal once restore succeeds.
	const onRestore = useCallback(async () => {
		setRestoring(true);
		try {
			await restore(id);
			setBanner(null);
			setNotFound(false);
			openedRef.current = false;
			// Give the daemon a moment to bring the PTY up, then re-attach.
			setTimeout(() => {
				if (openedRef.current) return;
				openedRef.current = true;
				muxRef.current?.openTerminal(terminalHandleId, projectId);
				const d = lastDimsRef.current;
				if (d) muxRef.current?.resize(terminalHandleId, d.cols, d.rows, projectId);
			}, 1200);
		} catch (e) {
			setBanner(`Couldn't restore the session. ${userFacingError(e)}`);
		} finally {
			setRestoring(false);
		}
	}, [restore, id, terminalHandleId, projectId]);

	return { sendPrompt, confirmKill, onRestore };
}
