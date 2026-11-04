import { useCallback, useEffect, useLayoutEffect, type FormEvent } from "react";
import { type ComposerEditorSnapshot } from "../ComposerEditor";
import { beginChatComposerMutation, cancelChatComposerMutation, type ChatDraftMutationToken } from "../../../lib/chat-drafts";
import { useComposerDraftState } from "./useComposerDraftState";
import { useComposerSuggestions } from "./useComposerSuggestions";
import { useComposerDraftPersistence } from "./useComposerDraftPersistence";
import { ChatComposerProps } from "./ComposerProps";
import { handleCompact, prepareSubmission, sendDurable, sendLocal, type SubmitEnv } from "./composer-submit-flow";

/** Submit, steer and claimed-delivery handling. */
export function useComposerSubmit(props: ChatComposerProps, draft: ReturnType<typeof useComposerDraftState>, suggestionState: ReturnType<typeof useComposerSuggestions>, persistence: ReturnType<typeof useComposerDraftPersistence>) {
	const env: SubmitEnv = { props, draft, suggestionState, persistence };
	const {
		skills = [],
		nativeImages,
		onCompact,
	} = props;
	const {
		dismissedKey,
		dismissedKeyRef,
		draftScope,
		durableDelivery,
		editor,
		highlightedRef,
		historyBrowse,
		isComposing,
		restoreFocusAfterSubmission,
		setSendError,
		setSteerOutcomeNotice,
		setSubmitting,
		submitInFlight,
		submitting,
		textRef,
		trigger,
		triggerRef,
	} = draft;
	const {
		savingQueuedEdit,
		slashCommands,
		suggestionsFor,
	} = suggestionState;
	const {
		pick,
	} = persistence;
	useEffect(() => {
		if (isComposing || !trigger || trigger.kind !== "skill" || trigger.key === dismissedKey) return;
		// A recalled message is shown, not typed: moving the caret through its `/skill`
		// must not rewrite it, which would also make it the saved draft.
		if (historyBrowse.current) return;
		const query = trigger.query.toLowerCase();
		if (!query) return;
		const exact = slashCommands.find((skill) => skill.name.toLowerCase() === query);
		if (!exact) return;

		// Do not eagerly accept a skill whose full name is also the start of another
		// skill. The user must still be able to type `/review-pr` when `/review`
		// exists; Enter remains available to accept the shorter exact match.
		const hasLongerPrefix = slashCommands.some((skill) => {
			const name = skill.name.toLowerCase();
			return name.length > query.length && name.startsWith(query);
		});
		if (!hasLongerPrefix) pick(exact.name);
	}, [dismissedKey, isComposing, pick, slashCommands, trigger]);

	useLayoutEffect(() => {
		if (submitting || !restoreFocusAfterSubmission.current) return;
		restoreFocusAfterSubmission.current = false;
		if (typeof document === "undefined") return;
		const active = document.activeElement;
		// Restore focus only when disabling the editor caused the blur. Do not steal
		// focus if the user deliberately moved to another control while awaiting.
		if (active === document.body || active === null) editor.current?.focus();
	}, [submitting]);

	const completeFromEditor = useCallback(
		(snapshot: ComposerEditorSnapshot, key: "Enter" | "Tab"): string | undefined => {
			const currentTrigger = snapshot.trigger;
			if (!currentTrigger) return undefined;
			if (key === "Enter" && /^\/(btw|side)$/.test(snapshot.text.trim()) && skills.some((skill) => skill.name === snapshot.text.trim().slice(1) && skill.source === "Tallpine")) return undefined;
			if (key === "Enter" && snapshot.text.trim() === "/compact" && onCompact) {
				return undefined;
			}
			const matches = suggestionsFor(currentTrigger);
			const chosen = matches[Math.min(highlightedRef.current, matches.length - 1)];
			if (!chosen) return undefined;
			triggerRef.current = currentTrigger;
			highlightedRef.current = 0;
			dismissedKeyRef.current = null;
			return chosen.value;
		},
		[onCompact, skills, suggestionsFor],
	);

	function submit(event?: FormEvent, forceSteer?: boolean): Promise<void> {
		event?.preventDefault();
		// React cannot publish the next busy prop until after this event returns. A
		// second Enter in that gap joins the accepted submission instead of opening a
		// second transport whose local admission rejection would look like a real
		// provider failure.
		if (submitInFlight.current) return submitInFlight.current;
		// Sending a recalled message makes it the message, not a history position.
		historyBrowse.current = null;
		restoreFocusAfterSubmission.current =
			typeof document !== "undefined" &&
			document.activeElement?.getAttribute("aria-label") === "Message the agent";
		setSubmitting(true);
		const pending = performSubmit(forceSteer);
		submitInFlight.current = pending;
		const release = () => {
			if (submitInFlight.current !== pending) return;
			submitInFlight.current = null;
			setSubmitting(false);
		};
		void pending.then(release, release);
		return pending;
	}

	async function performSubmit(forceSteer?: boolean) {
		// Own the scoped draft before file reading or staging can yield. A replacement
		// surface observes this reservation and cannot edit underneath this send.
		const mutationToken = draftScope && !savingQueuedEdit
			? beginChatComposerMutation(draftScope)
			: undefined;
		if (draftScope && !savingQueuedEdit && !mutationToken) return;
		try {
			await performClaimedSubmit(forceSteer, mutationToken);
		} finally {
			if (draftScope && mutationToken) cancelChatComposerMutation(draftScope, mutationToken);
		}
	}

	async function performClaimedSubmit(forceSteer?: boolean, mutationToken?: ChatDraftMutationToken) {
		let currentText = textRef.current;
		let body = currentText.trim();
		const recoveringDelivery = durableDelivery;
		const sendNativeImages = recoveringDelivery?.nativeImages ?? Boolean(nativeImages);
		setSendError(null);
		setSteerOutcomeNotice(null);

		if (!recoveringDelivery && !savingQueuedEdit && body === "/compact" && onCompact) {
			await handleCompact(env, onCompact, mutationToken);
			return;
		}
		const sub = await prepareSubmission(env, forceSteer, { currentText, body, recoveringDelivery, sendNativeImages });
		if (!sub) return;
		if (!draftScope || savingQueuedEdit) {
			await sendLocal(env, sub);
			return;
		}
		await sendDurable(env, sub, draftScope, mutationToken);
	}

	return {
		completeFromEditor,
		submit,
	};
}
