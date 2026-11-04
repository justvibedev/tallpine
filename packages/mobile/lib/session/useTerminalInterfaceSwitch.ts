import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert } from "react-native";
import { PRODUCT_NAME } from "../brand";
import { userFacingError } from "../connectionError";
import { haptics } from "../haptics";
import { terminalInterfaceFailureRecovery } from "./terminalInterfaceRecovery";
import {
	interfaceSwitchAlert,
	type InterfaceSwitchRecheck,
	mobileInterfaceTransitionIsActive,
	mobileInterfaceTransitionRecoveryMessage,
	useInterfaceTransition,
} from "./useInterfaceTransition";

/** Chat-interface switch state and actions for the terminal screen. */
export function useTerminalInterfaceSwitch({ activeConfig, shellOnly, sessionId, refresh, known, setBanner }: {
	activeConfig: Parameters<typeof useInterfaceTransition>[0];
	shellOnly: boolean;
	sessionId: string;
	refresh: Parameters<typeof useInterfaceTransition>[2];
	known: { status?: string | null; activity?: string | null } | null | undefined;
	setBanner: (b: string | null) => void;
}) {
	const interfaceSwitch = useInterfaceTransition(activeConfig, shellOnly ? "" : sessionId, refresh);
	// Owned by the screen rather than the hook: the background poll calls the same
	// `refresh()`, so a hook-wide busy flag would let a poll tick disable the
	// user's own button and swallow the very tap the recheck exists to serve.
	// The ref is the guard (state updates are async); the state drives the spinner.
	const [rechecking, setRechecking] = useState(false);
	const recheckingRef = useRef(false);
	// A recheck can be in flight for up to REQUEST_TIMEOUT_MS, long enough for the
	// user to leave. Alerting from a screen they already navigated away from would
	// interrupt whatever they went to instead.
	const mountedRef = useRef(true);
	useEffect(() => {
		mountedRef.current = true;
		return () => {
			mountedRef.current = false;
		};
	}, []);
	// The status the hook holds right now, readable after an await. The tap's
	// closure is from the render it started in, which is the pre-tap status.
	const interfaceStatusRef = useRef(interfaceSwitch.status);
	interfaceStatusRef.current = interfaceSwitch.status;
	const interfaceTransitionActive = mobileInterfaceTransitionIsActive(interfaceSwitch.transition);
	const interfaceRecoveryMessage = mobileInterfaceTransitionRecoveryMessage(interfaceSwitch.transition);
	const interfaceTransitionNotice =
		!interfaceTransitionActive &&
		!interfaceSwitch.transition?.noticeAcknowledgedAt &&
		(interfaceSwitch.transition?.phase === "failed" ||
			interfaceSwitch.transition?.phase === "recovery_required")
			? interfaceSwitch.transition
			: undefined;
	const interfaceTransitionRecovered =
		interfaceTransitionNotice?.phase === "recovery_required" &&
		interfaceTransitionNotice.errorCode === "DAEMON_RESTARTED";
	const interfaceTransitionNoticeText =
		interfaceTransitionNotice?.errorDetail ||
		(interfaceTransitionRecovered
			? `${PRODUCT_NAME} restored the session in its last committed interface.`
			: interfaceTransitionNotice?.phase === "recovery_required"
				? "The interface switch needs recovery before more work is sent."
				: "The interface switch failed; Terminal UI remains available.");
	const interfaceFailureRecovery = useMemo(
		() => terminalInterfaceFailureRecovery(interfaceSwitch.transition),
		[interfaceSwitch.transition?.errorCode],
	);
	const interfaceBusy = Boolean(
		known &&
		(known.status === "working" ||
			known.status === "needs_input" ||
			known.activity === "active" ||
			known.activity === "waiting_input" ||
			known.activity === "blocked"),
	);

	const startInterfaceSwitch = useCallback(
		async (policy: "drain" | "interrupt") => {
			try {
				await interfaceSwitch.start("chat", policy);
				setBanner(null);
			} catch (cause) {
				setBanner(`Couldn't switch interface. ${userFacingError(cause)}`);
			}
		},
		[interfaceSwitch],
	);

	const requestInterfaceSwitch = useCallback(async () => {
		// A second tap would start a second recheck against a link already slow
		// enough for the first one to be visible. `starting` and an active
		// transition are guarded too, so the button blocks exactly what it
		// announces as busy — otherwise a tap during the start POST fires a
		// duplicate the daemon answers with 409.
		if (recheckingRef.current || interfaceSwitch.starting || interfaceTransitionActive) return;
		haptics.tap();
		let status = interfaceSwitch.status;
		let recheck: InterfaceSwitchRecheck = { outcome: "answered" };
		if (!status?.supported) {
			// A tap can land between two polls, or before the first answer: ask
			// once more before telling the user it is unavailable.
			recheckingRef.current = true;
			setRechecking(true);
			try {
				const result = await interfaceSwitch.refresh();
				if (!mountedRef.current) return;
				if (!result) recheck = { outcome: "not-attempted" };
				// A superseded answer is not a verdict, same rule as the hook's own
				// loop: the readiness tick can overtake a slow tap request, and acting
				// on the tap's payload would alert "Couldn't reach Tallpine" (or "Not ready
				// yet" off an older body) while the button had just enabled itself.
				// The newer request owns the answer; use whatever the hook adopted.
				else if (result.stale) status = interfaceStatusRef.current;
				else if (result.ok) status = result.status;
				else recheck = { outcome: "failed", error: result.error, status: result.status };
			} finally {
				recheckingRef.current = false;
				if (mountedRef.current) setRechecking(false);
			}
		}
		if (!status?.supported) {
			const { title, message } = interfaceSwitchAlert(status, interfaceSwitch.error, recheck);
			Alert.alert(title, message);
			return;
		}
		// The guard at the top saw the pre-tap render. The fresh answer can carry
		// a transition someone else started while the recheck was in flight (the
		// desktop button, say); starting another gets a 409 and a "Switch failed"
		// banner for a switch that is in fact proceeding. The card takes over and
		// explains it — but after a visible spinner, returning in silence reads
		// as a tap that was dropped, so confirm the outcome the user asked for
		// actually happened rather than leaving the card to arrive unannounced.
		if (mobileInterfaceTransitionIsActive(status.transition)) {
			haptics.success();
			return;
		}
		if (!interfaceBusy) {
			void startInterfaceSwitch("drain");
			return;
		}
		Alert.alert(
			"Switch to Chat?",
			known?.activity === "waiting_input" || known?.activity === "blocked"
				? "This turn is waiting for your input. Finish waits for your answer; stop cancels it and switches now."
				: `Keep the same ${PRODUCT_NAME} session, worktree, and native agent conversation.`,
			[
				{ text: "Keep Terminal UI", style: "cancel" },
				{ text: "Finish, then switch", onPress: () => void startInterfaceSwitch("drain") },
				{ text: "Stop and switch", style: "destructive", onPress: () => void startInterfaceSwitch("interrupt") },
			],
		);
	}, [interfaceBusy, interfaceSwitch, interfaceTransitionActive, known?.activity, startInterfaceSwitch]);

	// The poll keeps retrying on its own at up to 8s; this is for the user who can
	// see the network is back and does not want to wait for the tick. The header
	// button is disabled for the whole transition, so this is the only tap that
	// re-asks while one is live. Same guard as the header recheck: it is the same
	// request, and two of them against a slow link is what the guard prevents.
	const retryInterfaceCheck = useCallback(async () => {
		if (recheckingRef.current) return;
		haptics.tap();
		recheckingRef.current = true;
		setRechecking(true);
		try {
			await interfaceSwitch.refresh();
		} finally {
			recheckingRef.current = false;
			if (mountedRef.current) setRechecking(false);
		}
	}, [interfaceSwitch]);

	const requestInterfaceFailureRecovery = useCallback(() => {
		if (!interfaceFailureRecovery) return;
		interfaceFailureRecovery.confirm((policy) => void startInterfaceSwitch(policy));
	}, [interfaceFailureRecovery, startInterfaceSwitch]);

	return {
		interfaceSwitch, rechecking, interfaceTransitionActive, interfaceRecoveryMessage,
		interfaceTransitionNotice, interfaceTransitionNoticeText, interfaceFailureRecovery,
		interfaceBusy, requestInterfaceSwitch, retryInterfaceCheck, requestInterfaceFailureRecovery,
	};
}
