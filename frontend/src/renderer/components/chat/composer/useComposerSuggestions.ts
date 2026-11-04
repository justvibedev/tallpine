import { cloneElement, useCallback, useEffect, useMemo, useState, isValidElement } from "react";
import { apiErrorMessage, getApiBaseUrl } from "../../../lib/api-client";
import { type ComposerImage, type ComposerTrigger } from "../ComposerEditor";
import { rankFiles, rankSkills, type Suggestion } from "../composerSuggest";
import type { ChatSkill } from "../../../types/conversation";
import { attachmentURL, IMAGE_ATTACHMENT_PATH } from "@tallpine/shared/chat/attachments";
import { setChatDraftBoundary } from "../../../lib/chat-draft-boundary";
import { useComposerDraftState } from "./useComposerDraftState";
import { ChatComposerProps } from "./ComposerProps";
import { SEND_CONCEAL_MS } from "./composer-model";

/** Slash-command, mention and attachment suggestion state for the ChatComposer. */
export function useComposerSuggestions(props: ChatComposerProps, draft: ReturnType<typeof useComposerDraftState>) {
	const {
		busy,
		willQueue,
		disabled,
		skills = [],
		filePaths = [],
		onSteer,
		onInterrupt,
		onContinue,
		canSteer,
		steerPending,
		draftSeed,
		editingQueuedTurnId,
		onDraftEmptyChange,
		savingQueuedEditPending,
		queuedEditRecovery,
		queuedDock,
		onCompact,
		compactUnavailable,
		autoFocus = true,
		assetBaseUrl,
		remoteHost = false,
		assetSessionId,
	} = props;
	const {
		appliedAcceptanceSequence,
		attachmentDraftPersistenceError,
		boundarySessionId,
		composerMutation,
		composerRevision,
		contextReferences,
		deliveryUncertain,
		dismissedKey,
		dismissedKeyRef,
		draftScope,
		draftScopeKey,
		durableDelivery,
		editor,
		fileAttachments,
		hasText,
		highlighted,
		modifierHeld,
		optimisticSend,
		persistedDraft,
		setSendError,
		steerNextRequest,
		submitting,
		synchronouslyClearedDeliveryRevision,
		textDraftPersistenceError,
		translateDraft,
		trigger,
		visibleRetainedAttachments,
	} = draft;
	const slashCommands = useMemo<ChatSkill[]>(() => {
		if (!onCompact || compactUnavailable === "This agent cannot compact its history") return skills;
		return [
			{
				name: "compact",
				displayName: "compact",
				description: "Summarize earlier history to reclaim context",
				source: "Tallpine",
			},
			...skills.filter((skill) => skill.name !== "compact"),
		];
	}, [compactUnavailable, onCompact, skills]);

	const suggestionsFor = useCallback((currentTrigger?: ComposerTrigger): Suggestion[] => {
		if (!currentTrigger || currentTrigger.key === dismissedKeyRef.current) return [];
		// An empty candidate list is the whole reason the sigil stays ordinary: with
		// no commands or skills there is nothing to open, so `/` types a slash.
		if (currentTrigger.kind === "skill") {
			return rankSkills(slashCommands, currentTrigger.query);
		}
		return rankFiles(filePaths, currentTrigger.query);
	}, [slashCommands, filePaths]);

	const suggestions: Suggestion[] = useMemo(
		() => suggestionsFor(trigger),
		[trigger, dismissedKey, suggestionsFor],
	);

	const menuOpen = suggestions.length > 0;
	// Clamped rather than trusted: the list re-ranks on every keystroke, so the
	// index from the previous list can point past the end of this one.
	const activeIndex = Math.min(highlighted, suggestions.length - 1);

	const staged = fileAttachments.attachments.length > 0 || visibleRetainedAttachments.length > 0;
	const nextDraftHasChanged = Boolean(durableDelivery?.draft && composerRevision.current > durableDelivery.revision + 1);
	const stripAttachments = (optimisticSend || nextDraftHasChanged ? [] : [...visibleRetainedAttachments, ...fileAttachments.attachments]).map((file) => {
		const path = "stagedPath" in file ? file.stagedPath : "path" in file ? file.path : undefined;
		const assetOrigin = remoteHost ? assetBaseUrl : assetBaseUrl ?? getApiBaseUrl();
		const preview = file.dataUrl ?? (path && assetOrigin !== undefined && IMAGE_ATTACHMENT_PATH.test(path)
			? attachmentURL(assetOrigin, assetSessionId ?? boundarySessionId ?? "", path) : undefined);
		return { file, path, preview };
	});
	const composerImages: ComposerImage[] = stripAttachments.flatMap(({ file, path, preview }) =>
		path && IMAGE_ATTACHMENT_PATH.test(path) ? [{ path, name: file.name, src: preview }] : []);
	const controlsDisabled = Boolean(disabled || submitting);
	const hasDraft = hasText || staged || contextReferences.length > 0;
	useEffect(() => {
		onDraftEmptyChange?.(!hasDraft);
	}, [hasDraft, onDraftEmptyChange]);
	const savingQueuedEdit = Boolean(editingQueuedTurnId);
	const acceptedMutationWaiting = Boolean(
		composerMutation.accepted &&
			composerMutation.accepted.sequence > appliedAcceptanceSequence,
	);
	const acceptedClearFailed = composerMutation.accepted?.result.ok === false;
	const draftMutationPending =
		submitting || composerMutation.pending || acceptedMutationWaiting || Boolean(durableDelivery);
	const canRecoverDelivery = Boolean(
		durableDelivery &&
			(!busy || durableDelivery.state === "accepted") &&
			!disabled &&
			!steerPending &&
		!savingQueuedEditPending &&
			!submitting &&
			!composerMutation.pending,
	);
	const canAbandonUncertainSteer = Boolean(
		deliveryUncertain &&
			durableDelivery?.kind === "steer" &&
			durableDelivery.state === "dispatching" &&
			draftScope &&
			!submitting,
	);
	// A plain send already shows its echo in the timeline. Its text stays held in the
	// editor until the daemon accepts, so a rejection or lost response can restore it,
	// but it is not drawn meanwhile: the composer reads as sent, not stranded.
	const sendInFlight = Boolean(
		submitting &&
			!staged &&
			!deliveryUncertain &&
			durableDelivery?.kind === "send" &&
			durableDelivery.state === "dispatching",
	);
	// The request has no timeout. If the daemon stalls, bring the text back so a stuck
	// send never reads as a vanished draft.
	const [concealExpired, setConcealExpired] = useState(false);
	useEffect(() => {
		if (!sendInFlight) {
			setConcealExpired(false);
			return;
		}
		const timer = window.setTimeout(() => setConcealExpired(true), SEND_CONCEAL_MS);
		return () => window.clearTimeout(timer);
	}, [sendInFlight]);
	const sendConcealed = sendInFlight && !concealExpired;
	const canSend =
		(hasText || staged) &&
		(savingQueuedEdit || !busy) &&
		!disabled &&
		!steerPending &&
		!savingQueuedEditPending &&
		!draftMutationPending &&
			!acceptedClearFailed &&
			!durableDelivery &&
			!fileAttachments.preparing;
	const sendActionEnabled = canSend || canRecoverDelivery;
	// A plain send while a turn runs is queued, so the button says so instead of
	// looking identical to an immediate send.
	const queuesDraft = Boolean(willQueue && !savingQueuedEdit && !queuedEditRecovery);
	// Cmd/Ctrl+Enter or Cmd/Ctrl+click steers the current draft into the running
	// turn; the send button only shows it while the modifier is held.
	// Steering has no excerpt payload, so attached excerpts would be cleared with
	// the accepted draft without reaching the agent. Queue those drafts instead.
	const canSteerDraft =
		Boolean(canSteer && onSteer) && !savingQueuedEdit && contextReferences.length === 0;
	const steersDraft = modifierHeld && canSteerDraft;
	const showsSteer = steersDraft && !durableDelivery && !queuedEditRecovery;
	const sendActionLabel = translateDraft(durableDelivery && !submitting
		? durableDelivery.state === "accepted"
			? "chat.draft.clearMessage"
			: "chat.draft.retryMessage"
		: queuedEditRecovery
			? "chat.draft.retryEdit"
			: showsSteer ? "Steer message" : queuesDraft ? "Queue message" : "Send message");
	const canStopTurn = Boolean(
		willQueue && onInterrupt && !controlsDisabled && !hasDraft && !savingQueuedEdit,
	);
	// Typing a draft turns the button back into Send, so after a stop the user can
	// still ask something else instead.
	const canContinueTurn = Boolean(
		onContinue && !canStopTurn && !controlsDisabled && !hasDraft && !savingQueuedEdit && !durableDelivery,
	);
	const [continuing, setContinuing] = useState(false);
	const continueTurn = () => {
		if (!onContinue || continuing) return;
		setSendError(null);
		setContinuing(true);
		void Promise.resolve().then(onContinue)
			.catch((error: unknown) => setSendError(apiErrorMessage(error, "Couldn't continue the turn. Try again.")))
			.finally(() => setContinuing(false));
	};
	const canSteerNext =
		Boolean(canSteer && onSteer) &&
		!controlsDisabled &&
		!hasDraft &&
		!savingQueuedEdit &&
		Boolean(queuedDock);
	const sendHint = menuOpen
		? "Enter to insert"
		: savingQueuedEdit
			? "⏎ save edit"
			: showsSteer
				? "Steer into running turn"
				: willQueue
					? "⏎ queue"
					: "Enter to send";
	const persistedText = persistedDraft?.composer.text;
	const draftSeedId = draftSeed?.id ?? (draftScopeKey ? `session:${draftScopeKey}` : undefined);
	const draftSeedText = draftSeed?.text ?? persistedText;
	const draftPersistenceError =
		textDraftPersistenceError ?? attachmentDraftPersistenceError;

	useEffect(() => {
		if (!boundarySessionId) return;
		const deliveryWasSynchronouslyCleared = Boolean(
			durableDelivery &&
				synchronouslyClearedDeliveryRevision.current === durableDelivery.revision,
		);
		setChatDraftBoundary(
			boundarySessionId,
			"composer",
			[
				...(draftPersistenceError && !deliveryWasSynchronouslyCleared
					? (["persistence-failed"] as const)
					: []),

				...(fileAttachments.preparing ? (["pending-attachments"] as const) : []),
			],
		);
	}, [draftPersistenceError, boundarySessionId, durableDelivery, fileAttachments.preparing]);

	useEffect(
		() => () => {
			if (boundarySessionId) setChatDraftBoundary(boundarySessionId, "composer", undefined);
		},
		[boundarySessionId],
	);
	const queuedDockWithSteer = isValidElement<{
		canSteerNext?: boolean;
		steerNextRequest?: number;
		disabled?: boolean;
	}>(queuedDock)
		? cloneElement(
				queuedDock,
				{ canSteerNext, steerNextRequest, disabled: submitting || queuedDock.props.disabled },
			)
		: queuedDock;

	const focusEditor = useCallback(() => {
		if (!autoFocus || disabled) return;
		editor.current?.focus();
	}, [autoFocus, disabled]);

	return {
		activeIndex,
		canAbandonUncertainSteer,
		canContinueTurn,
		canSteerDraft,
		canSteerNext,
		canStopTurn,
		composerImages,
		continueTurn,
		continuing,
		controlsDisabled,
		draftMutationPending,
		draftPersistenceError,
		draftSeedId,
		draftSeedText,
		focusEditor,
		hasDraft,
		menuOpen,
		queuedDockWithSteer,
		savingQueuedEdit,
		sendActionEnabled,
		sendActionLabel,
		sendConcealed,
		sendHint,
		showsSteer,
		slashCommands,
		staged,
		steersDraft,
		stripAttachments,
		suggestions,
		suggestionsFor,
	};
}
