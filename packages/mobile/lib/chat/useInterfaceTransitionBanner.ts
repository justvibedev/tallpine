import { useCallback, useRef, useState } from "react";
import { mobileInterfaceTransitionIsCancellable, mobileInterfaceTransitionRecoveryMessage } from "../session/useInterfaceTransition";
import { InterfaceTransitionRecheck } from "../session/useInterfaceTransition";
import { SessionInterfaceTransition } from "./api";
import { SessionInterfaceTransitionStatus } from "./api";
import { interfacePhaseLabel } from "./ChatSessionScreen";

export function useInterfaceTransitionBanner({
	interfaceSwitch,
}: {
	interfaceSwitch: { status: SessionInterfaceTransitionStatus | undefined; transition: SessionInterfaceTransition | undefined; loading: boolean; starting: boolean; cancelling: boolean; acknowledgingNotice: boolean; error: string | undefined; acknowledgeNoticeError: string | undefined; fetchFailed: boolean; start: (targetMode: "chat" | "tui", policy: "drain" | "interrupt") => Promise<void>; cancel: () => Promise<void>; acknowledgeNotice: (transitionId: string) => Promise<void>; refresh: () => Promise<InterfaceTransitionRecheck | undefined>; };
}) {

	// The poll keeps retrying on its own at up to 8s; this is for the user who can
	// see the network is back and does not want to wait for the tick. Nothing else
	// on this screen re-asks the hook, so without it a transition whose poll had
	// failed could only be checked by leaving and coming back. The ref is the
	// guard (state updates are async); the state drives the label.
	const [recheckingTransition, setRecheckingTransition] = useState(false);
	const recheckingTransitionRef = useRef(false);
	const retryInterfaceCheck = useCallback(async () => {
		if (recheckingTransitionRef.current) return;
		recheckingTransitionRef.current = true;
		setRecheckingTransition(true);
		try {
			await interfaceSwitch.refresh();
		} finally {
			recheckingTransitionRef.current = false;
			setRecheckingTransition(false);
		}
	}, [interfaceSwitch]);
	const interfaceRecoveryMessage = mobileInterfaceTransitionRecoveryMessage(interfaceSwitch.transition);
	const interfaceTransitionPhaseText = interfaceRecoveryMessage || `Switching to Terminal UI · ${interfacePhaseLabel(interfaceSwitch.transition?.phase)}`;
	const interfaceTransitionBanner = {
		text: interfaceSwitch.fetchFailed
			? `${interfaceTransitionPhaseText}. Couldn't check on it${interfaceSwitch.error ? `: ${interfaceSwitch.error}` : ""}`
			: interfaceTransitionPhaseText,
		// Cancel stays put while a check is failing: the phase the hook holds is
		// still cancellable and the cancel is its own request, so a user who wants
		// out should not have to prove the link first. Retry rides alongside it,
		// which is also how the terminal card lays the two out.
		action: mobileInterfaceTransitionIsCancellable(interfaceSwitch.transition) ? (interfaceSwitch.cancelling ? "Cancelling…" : "Cancel") : undefined,
		onPress: interfaceSwitch.cancelling ? undefined : () => void interfaceSwitch.cancel().catch(() => {}),
		secondary: interfaceSwitch.fetchFailed || interfaceRecoveryMessage ? (recheckingTransition ? "Retrying…" : "Retry") : undefined,
		onSecondary: recheckingTransition ? undefined : () => void retryInterfaceCheck(),
	};

	return { interfaceTransitionBanner };
}
