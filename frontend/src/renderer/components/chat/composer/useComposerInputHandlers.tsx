import { useCallback, useEffect, type ClipboardEvent, type DragEvent, type KeyboardEvent, type ReactElement } from "react";
import { type ComposerEditorSnapshot } from "../ComposerEditor";
import { moveHighlight } from "../composerSuggest";
import { isInlineImagePath, proseBesideImages, removeInlineImagePath } from "@tallpine/shared/chat/attachments";
import { firstDragEntryIsDirectory } from "../../../lib/dropClassify";
import { useComposerDraftState } from "./useComposerDraftState";
import { useComposerSuggestions } from "./useComposerSuggestions";
import { useComposerDraftPersistence } from "./useComposerDraftPersistence";
import { useComposerSubmit } from "./useComposerSubmit";
import { ChatComposerProps } from "./ComposerProps";

/** Keyboard, paste, drop and attachment handlers. */
export function useComposerInputHandlers(props: ChatComposerProps, draft: ReturnType<typeof useComposerDraftState>, suggestionState: ReturnType<typeof useComposerSuggestions>, persistence: ReturnType<typeof useComposerDraftPersistence>, submitState: ReturnType<typeof useComposerSubmit>) {
	const {
		disabled,
		approval,
		elicitation,
		editingQueuedTurnId,
		onCancelQueuedEdit,
		queuedEditRecovery,
		commandError,
		attachedTop = false,
		queuedDock,
		onCompact,
		sentMessages,
	} = props;
	const {
		canAttach,
		deliveryRecoveryNotice,
		dismissedKeyRef,
		editor,
		fileAttachments,
		highlightedRef,
		historyBrowse,
		isComposing,
		optimisticSend,
		sendError,
		setDismissedKey,
		setDragging,
		setHighlighted,
		setModifierHeld,
		setSteerNextRequest,
		submitInFlight,
		textRef,
		triggerRef,
	} = draft;
	const {
		canSteerDraft,
		canSteerNext,
		composerImages,
		draftMutationPending,
		draftPersistenceError,
		queuedDockWithSteer,
		suggestionsFor,
	} = suggestionState;
	const {
		onEditorChange,
		persistComposerText,
		pick,
	} = persistence;
	const {
		submit,
	} = submitState;
	function onEditorEnter(
		snapshot: ComposerEditorSnapshot,
		event: globalThis.KeyboardEvent,
	): boolean {
		textRef.current = snapshot.text;
		return handleEnterKey(event);
	}

	const handleEnterKey = useCallback(
		(event: globalThis.KeyboardEvent): boolean => {
			const liveSnapshot = editor.current?.getSnapshot();
			if (liveSnapshot) textRef.current = liveSnapshot.text;
			const liveTrigger = liveSnapshot?.trigger;
			const liveSuggestions = suggestionsFor(liveTrigger);
			if (liveSuggestions.length > 0) {
				if (textRef.current.trim() === "/compact" && onCompact) {
					void submit();
					return true;
				}
				triggerRef.current = liveTrigger;
				const chosen = liveSuggestions[Math.min(highlightedRef.current, liveSuggestions.length - 1)];
				if (chosen) pick(chosen.value);
				return true;
			}

			if (canSteerNext && !textRef.current.trim() && !fileAttachments.hasPendingReads()) {
				setSteerNextRequest((request) => request + 1);
				return true;
			}
			const wantsSteer = (event.metaKey || event.ctrlKey) && canSteerDraft;
			void submit(undefined, wantsSteer);
			return true;
		},
		[canSteerDraft, canSteerNext, fileAttachments, onCompact, pick, suggestionsFor],
	);

	function onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
		if (event.nativeEvent.isComposing) return;
		// Enter is handled in Lexical before a newline is inserted; this handler is
		// only for menu navigation and escape while a completion menu is open.
		const liveSnapshot = editor.current?.getSnapshot();
		if (liveSnapshot) textRef.current = liveSnapshot.text;
		const liveTrigger = liveSnapshot?.trigger;
		const liveSuggestions = suggestionsFor(liveTrigger);
		if (liveSuggestions.length > 0) {
			if (event.key === "ArrowDown" || event.key === "ArrowUp") {
				event.preventDefault();
				const next = moveHighlight(
					Math.min(highlightedRef.current, liveSuggestions.length - 1),
					event.key === "ArrowDown" ? 1 : -1,
					liveSuggestions.length,
				);
				highlightedRef.current = next;
				setHighlighted(next);
				return;
			}
			if (event.key === "Escape") {
				event.preventDefault();
				event.stopPropagation();
				dismissedKeyRef.current = liveTrigger?.key ?? null;
				setDismissedKey(dismissedKeyRef.current);
				return;
			}
		}

		if ((event.key === "ArrowUp" || event.key === "ArrowDown") && recallSentMessage(event.key === "ArrowUp", event)) {
			event.preventDefault();
			return;
		}

		if (event.key === "Escape" && editingQueuedTurnId && onCancelQueuedEdit && !queuedEditRecovery && !submitInFlight.current) {
			event.preventDefault();
			onCancelQueuedEdit();
		}
	}

	/**
	 * Up walks back through sent messages and Down forward, ending on the draft the
	 * user had. Up starts a walk only with the caret at the very start of the text
	 * (always true when it is empty), so the arrows still move the caret inside a
	 * multiline draft. Every step leaves the caret at the start, where either key
	 * continues; Down also continues from the end, after reading down a long message.
	 */
	function recallSentMessage(older: boolean, event: KeyboardEvent<HTMLDivElement>): boolean {
		if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey || isComposing) return false;
		// A queued edit, a delivery still settling, or a disabled controller owns the text.
		if (editingQueuedTurnId || queuedEditRecovery || disabled || submitInFlight.current || draftMutationPending) {
			return false;
		}
		// A pasted image holds its place in the draft until staging fills it in.
		if (fileAttachments.preparing || fileAttachments.hasPendingReads()) return false;
		const caret = editor.current?.caretBoundary();
		const browsing = historyBrowse.current;
		if (!caret || !(caret.atStart || (browsing && !older && caret.atEnd))) return false;
		const browse = browsing ?? (older && sentMessages?.length
			? { entries: [...sentMessages].reverse(), index: -1, draft: textRef.current, shown: textRef.current }
			: null);
		if (!browse) return false;
		const index = browse.index + (older ? 1 : -1);
		if (index >= browse.entries.length) return false;
		editor.current?.setText(index < 0 ? browse.draft : browse.entries[index]!, "start");
		const snapshot = editor.current?.getSnapshot();
		historyBrowse.current = index < 0 || !snapshot ? null : { ...browse, index, shown: snapshot.text };
		if (snapshot) onEditorChange(snapshot);
		return true;
	}

	/** The image left the draft, which is held aside while a recalled message shows. */
	function removeImageFromSavedDraft(path: string) {
		const browse = historyBrowse.current;
		if (!browse) return;
		const draft = removeInlineImagePath(browse.draft, path);
		if (draft === browse.draft) return;
		historyBrowse.current = { ...browse, draft };
		persistComposerText(draft);
	}

	// Images get an inline chip only beside prose that can refer to them.
	// Image-only drafts already show every image in the attachment strip.
	function attachFiles(files: File[]) {
		// Reserve the spot now: staging can take a while, and the user keeps typing.
		const reservation = proseBesideImages(textRef.current.trim(), composerImages.map((image) => image.path)) &&
			files.some((file) => file.type.startsWith("image/"))
			? editor.current?.reserveImages()
			: undefined;
		void fileAttachments.addFiles(files).then((added) => {
			if (!reservation) return;
			editor.current?.fillImages(reservation, added.flatMap((attachment) =>
				attachment.stagedPath && isInlineImagePath(attachment.stagedPath) ? [attachment.stagedPath] : []));
		});
	}

	const attachDropped = (files: File[]): boolean => {
		if (!canAttach || fileAttachments.preparing || draftMutationPending || submitInFlight.current) return false;
		attachFiles(files);
		return true;
	};
	draft.attachFilesRef.current = attachDropped;

	function onPaste(event: ClipboardEvent<HTMLDivElement>) {
		if (!canAttach || fileAttachments.preparing || draftMutationPending || submitInFlight.current) return;
		const clipboard = event.clipboardData;
		const files = Array.from(clipboard?.files ?? []);
		if (files.length === 0) return;
		// The paste is only claimed when there is no text alongside the image: a copy
		// carrying both should still paste its text.
		const hasText = typeof clipboard?.getData === "function" && clipboard.getData("text/plain") !== "";
		if (!hasText) event.preventDefault();
		attachFiles(files);
	}

	function onDrop(event: DragEvent<HTMLFormElement>) {
		setDragging(false);
		// A folder is left to bubble to the app-wide drop that adds it as a project.
		if (firstDragEntryIsDirectory(event.dataTransfer)) return;
		if (!canAttach || fileAttachments.preparing || draftMutationPending || submitInFlight.current) return;
		const files = Array.from(event.dataTransfer?.files ?? []);
		if (files.length === 0) return;
		event.preventDefault();
		event.stopPropagation();
		attachFiles(files);
	}

	// Only modifier transitions re-render: React bails out when the value is unchanged.
	useEffect(() => {
		const onKey = (event: globalThis.KeyboardEvent) => {
			setModifierHeld(event.metaKey || event.ctrlKey);
		};
		const onBlur = () => {
			setModifierHeld(false);
		};
		window.addEventListener("keydown", onKey);
		window.addEventListener("keyup", onKey);
		window.addEventListener("blur", onBlur);
		return () => {
			window.removeEventListener("keydown", onKey);
			window.removeEventListener("keyup", onKey);
			window.removeEventListener("blur", onBlur);
		};
	}, []);

	const attachmentError =
		fileAttachments.error ??
		draftPersistenceError ??
		(optimisticSend ? null : deliveryRecoveryNotice) ??
		sendError ??
		(fileAttachments.attachments.some((file) => !file.data && !file.stagedPath)
			? "chat.draft.filesUnavailable" : null) ??
		commandError;
	const withQueueStack = (form: ReactElement) =>
		(
			<div className="relative mx-auto flex w-full max-w-3xl flex-col">
				{queuedDock ? (
				<div
					className="cursor-chat-composer-queue queue-dock-enter relative z-10 mx-auto mb-2 w-[calc(100%-2rem)]"
					data-testid="queued-composer-dock"
				>
					{queuedDockWithSteer}
				</div>
				) : null}
				{elicitation ? (
					<div
						className="queue-dock-enter relative z-10 mx-auto mb-2 w-[calc(100%-2rem)]"
						data-testid="elicitation-composer-dock"
					>
						{elicitation}
					</div>
				) : null}
				{form}
			</div>
		);

	if (approval) {
		return { earlyView: withQueueStack(
			<form
				onSubmit={(event) => event.preventDefault()}
				data-attached-top={attachedTop && !queuedDock && !elicitation ? true : undefined}
				className="cursor-chat-composer relative flex flex-col gap-1.5 px-3 py-3"
			>
				{approval}
				{commandError ? (
					<p role="alert" className="px-1.5 text-xs leading-snug text-destructive">
						{commandError}
					</p>
				) : null}
			</form>,
		) };
	}

	return {
		attachFiles,
		attachmentError,
		onDrop,
		onEditorEnter,
		onKeyDown,
		onPaste,
		removeImageFromSavedDraft,
		withQueueStack,
	};
}
