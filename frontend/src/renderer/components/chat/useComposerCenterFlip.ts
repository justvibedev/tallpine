import type { SessionKind } from "../../types/workspace";
import { useLayoutEffect, useRef, useState } from "react";
import { EMPTY_CHAT_PLACEHOLDERS } from "./WelcomePromptSuggestions";
import { WELCOME_FADE_MS } from "./timeline-constants";

/**
 * Centered-composer placement and its glide when the first message is sent.
 */
export function useComposerCenterFlip({ sessionRole, conversationEmpty, uiSessionId }: {
	sessionRole: SessionKind;
	conversationEmpty: boolean;
	uiSessionId: string;
}) {
	const [emptyChatPlaceholder] = useState(
		() => sessionRole === "orchestrator"
			? "Ask anything about this project"
			: EMPTY_CHAT_PLACEHOLDERS[Math.floor(Math.random() * EMPTY_CHAT_PLACEHOLDERS.length)],
	);

	const composerDockRef = useRef<HTMLDivElement>(null);

	// The composer itself, not the dock: the dock also holds the welcome heading, which
	// disappears on the first send, and sliding from the dock's top would make the
	// composer jump up by the heading's height before it moved.
	const composerBoxRef = useRef<HTMLDivElement>(null);

	const wasConversationEmpty = useRef(conversationEmpty);

	const [welcomeLeaving, setWelcomeLeaving] = useState(false);

	const composerCenteredTopRef = useRef<number | null>(null);

	const composerFlipDyRef = useRef<number | null>(null);

	const composerSessionRef = useRef(uiSessionId);

	useLayoutEffect(() => {
		const dock = composerDockRef.current;
		if (!dock) return;

		const sessionChanged = composerSessionRef.current !== uiSessionId;
		if (sessionChanged) {
			composerSessionRef.current = uiSessionId;
			composerCenteredTopRef.current = null;
			composerFlipDyRef.current = null;
			dock.style.transition = "";
			dock.style.transform = "";
			dock.removeAttribute("data-composer-motion");
		}

		const composerTop = () => (composerBoxRef.current ?? dock).getBoundingClientRect().top;
		if (conversationEmpty) {
			composerCenteredTopRef.current = composerTop();
			composerFlipDyRef.current = null;
			return;
		}

		// Capture the centered→docked delta once. Keep it across Strict Mode's
		// setup→cleanup→setup so the docking motion still plays.
		if (composerFlipDyRef.current == null && composerCenteredTopRef.current != null) {
			composerFlipDyRef.current = composerCenteredTopRef.current - composerTop();
			composerCenteredTopRef.current = null;
		}

		const dy = composerFlipDyRef.current;
		const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
		if (dy == null || reduceMotion || Math.abs(dy) < 1) {
			composerFlipDyRef.current = null;
			return;
		}

		dock.dataset.composerMotion = "animating";
		dock.style.transition = "none";
		dock.style.transform = `translateY(${dy}px)`;
		void dock.offsetHeight;
		dock.style.transition = "transform 500ms cubic-bezier(0.22, 1, 0.36, 1)";
		dock.style.transform = "translateY(0)";

		const onEnd = (event: TransitionEvent) => {
			if (event.target !== dock || event.propertyName !== "transform") return;
			composerFlipDyRef.current = null;
			dock.style.transition = "";
			dock.style.transform = "";
			dock.removeAttribute("data-composer-motion");
		};
		dock.addEventListener("transitionend", onEnd);
		return () => {
			dock.removeEventListener("transitionend", onEnd);
		};
	}, [conversationEmpty, uiSessionId]);

	// The welcome heading fades out where it stood instead of vanishing, so the first
	// send does not pop it off the screen.
	useLayoutEffect(() => {
		const leaving = wasConversationEmpty.current && !conversationEmpty;
		wasConversationEmpty.current = conversationEmpty;
		if (!leaving) {
			if (conversationEmpty) setWelcomeLeaving(false);
			return;
		}
		setWelcomeLeaving(true);
		const timer = window.setTimeout(() => setWelcomeLeaving(false), WELCOME_FADE_MS);
		return () => window.clearTimeout(timer);
	}, [conversationEmpty]);

	return { emptyChatPlaceholder, composerDockRef, composerBoxRef, welcomeLeaving };
}
