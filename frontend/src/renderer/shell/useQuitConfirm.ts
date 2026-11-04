import { useCallback, useEffect, useState } from "react";
import { tallpineBridge } from "../lib/bridge";

export function useQuitConfirm() {
	// macOS ⌘Q asks before quitting; the main process quits once confirmed.
	const [isQuitConfirmOpen, setIsQuitConfirmOpen] = useState(false);
	const [quitKeepsRunning, setQuitKeepsRunning] = useState(false);
	useEffect(
		() =>
			tallpineBridge.app.onQuitConfirmRequest(({ keepsRunning }) => {
				setQuitKeepsRunning(keepsRunning);
				setIsQuitConfirmOpen(true);
			}),
		[],
	);
	const resolveQuitConfirm = useCallback((confirmed: boolean) => {
		setIsQuitConfirmOpen(false);
		tallpineBridge.app.resolveQuitConfirm(confirmed);
	}, []);
	return { isQuitConfirmOpen, quitKeepsRunning, resolveQuitConfirm };
}
