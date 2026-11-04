import { useCallback } from "react";
import { Alert } from "react-native";
import { userFacingError } from "../connectionError";
import { PRODUCT_NAME } from "../brand";
import { InterfaceTransitionRecheck } from "../session/useInterfaceTransition";
import { SessionInterfaceTransition } from "./api";
import { SessionInterfaceTransitionStatus } from "./api";

export function useInterfaceSwitchRequest({
	setMenuOpen,
	interfaceSwitch,
	turnActive,
	turnWaiting,
}: {
	setMenuOpen: React.Dispatch<React.SetStateAction<boolean>>;
	interfaceSwitch: { status: SessionInterfaceTransitionStatus | undefined; transition: SessionInterfaceTransition | undefined; loading: boolean; starting: boolean; cancelling: boolean; acknowledgingNotice: boolean; error: string | undefined; acknowledgeNoticeError: string | undefined; fetchFailed: boolean; start: (targetMode: "chat" | "tui", policy: "drain" | "interrupt") => Promise<void>; cancel: () => Promise<void>; acknowledgeNotice: (transitionId: string) => Promise<void>; refresh: () => Promise<InterfaceTransitionRecheck | undefined>; };
	turnActive: boolean;
	turnWaiting: boolean;
}) {

	const startInterfaceSwitch = useCallback(
		async (policy: "drain" | "interrupt") => {
			setMenuOpen(false);
			try {
				await interfaceSwitch.start("tui", policy);
			} catch (cause) {
				Alert.alert("Couldn't switch interface", userFacingError(cause));
			}
		},
		[interfaceSwitch],
	);

	const requestInterfaceSwitch = useCallback(() => {
		if (!interfaceSwitch.status?.supported) {
			Alert.alert("Terminal UI unavailable", interfaceSwitch.status?.reason || interfaceSwitch.error || "This agent cannot resume the same native conversation in Terminal UI.");
			return;
		}
		if (!turnActive) {
			void startInterfaceSwitch("drain");
			return;
		}
		Alert.alert(
			"Switch to Terminal UI?",
			turnWaiting
				? "This turn is waiting for your input. Finish waits for your answer; stop cancels it and switches now."
				: `Keep the same ${PRODUCT_NAME} session, worktree, and native agent conversation.`,
			[
				{ text: "Keep Chat", style: "cancel" },
				{ text: "Finish, then switch", onPress: () => void startInterfaceSwitch("drain") },
				{ text: "Stop and switch", style: "destructive", onPress: () => void startInterfaceSwitch("interrupt") },
			],
		);
	}, [interfaceSwitch, startInterfaceSwitch, turnActive, turnWaiting]);

	return { requestInterfaceSwitch };
}
