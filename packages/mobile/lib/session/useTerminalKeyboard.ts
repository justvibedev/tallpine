import { useEffect, useState } from "react";
import { Keyboard, LayoutAnimation, Platform } from "react-native";

/** Keyboard height/visibility the screen reserves by hand on both platforms. */
export function useTerminalKeyboard() {
	const [kbHeight, setKbHeight] = useState(0); // iOS: space to reserve for keyboard
	const [kbVisible, setKbVisible] = useState(false); // both platforms
	// Neither platform shrinks the layout for the keyboard: iOS never has, and on
	// Android edge-to-edge (edgeToEdgeEnabled) defeats windowSoftInputMode=adjustResize
	// so the window no longer resizes - the keyboard just draws over our content.
	// So reserve the keyboard on BOTH platforms and let the screen pad itself above
	// it, else the input dock (and its send button) hide behind it. What the event
	// reports is not the same quantity on the two platforms — rootKeyboardPad owns
	// that difference. This is the ONLY place the keyboard height is applied — the
	// dock adds nothing on top of it (see dockInset), because doing both is what
	// made the bar kick.
	// Deliberately still on the platform listeners, unlike the spawn sheet, the
	// Workers board and the chat screen, which all moved to keyboard-controller's
	// useKeyboardState.
	//
	// That hook subscribes to keyboardWillShow and keyboardDidHide only. This
	// screen needs keyboardDidShow as well — see the corrector below, which exists
	// because willShow reports a height that still includes the accessory bar we
	// hide. Migrating here would reinstate exactly the gap that corrector removes,
	// and would lose the iOS-only didHide backup whose absence on Android is itself
	// a fix (registering it there subscribed the same handler twice and collapsed
	// the dock). The gain would be earlier Android events on the one screen that
	// reserves keyboard space by hand, which is not worth reopening two fixed bugs.
	useEffect(() => {
		const isIOS = Platform.OS === "ios";
		const showEvt = isIOS ? "keyboardWillShow" : "keyboardDidShow";
		const hideEvt = isIOS ? "keyboardWillHide" : "keyboardDidHide";
		// Ride the system keyboard curve. Without this the relayout lands as an
		// instant jump while the keyboard is still sliding, which is most of why
		// the dock looked like it kicked.
		const animate = (duration?: number) =>
			LayoutAnimation.configureNext({
				duration: duration || 250,
				update: { type: LayoutAnimation.Types.keyboard },
			});
		const show = Keyboard.addListener(showEvt, (e) => {
			animate(e.duration);
			setKbVisible(true);
			setKbHeight(e.endCoordinates.height);
		});
		const hide = Keyboard.addListener(hideEvt, (e) => {
			animate(e?.duration);
			setKbVisible(false);
			setKbHeight(0);
		});
		// willShow can report a height that still includes the accessory bar we hid,
		// leaving a gap. didShow reports the actual final frame - use it to correct.
		// Guarded so the common case where the two agree is a no-op instead of a
		// second visible nudge.
		const didShow = isIOS
			? Keyboard.addListener("keyboardDidShow", (e) => {
					const next = e.endCoordinates.height;
					setKbHeight((h) => {
						if (h === next) return h;
						animate(e.duration);
						return next;
					});
				})
			: null;
		// Backup: guarantee the reserved space collapses even if willHide is missed.
		// iOS-only, like didShow above — Android's hide listener is already bound to
		// keyboardDidHide, so registering this there subscribed the same handler to
		// the same event twice and collapsed the dock on both.
		const didHide = isIOS
			? Keyboard.addListener("keyboardDidHide", () => {
					setKbVisible(false);
					setKbHeight(0);
				})
			: null;
		return () => {
			show.remove();
			hide.remove();
			didShow?.remove();
			didHide?.remove();
		};
	}, []);
	return { kbHeight, kbVisible };
}
