import type { ChatComposerProps } from "./composer/ComposerProps";
import { useComposerDraftState } from "./composer/useComposerDraftState";
import { useComposerSuggestions } from "./composer/useComposerSuggestions";
import { useComposerDraftPersistence } from "./composer/useComposerDraftPersistence";
import { useComposerSubmit } from "./composer/useComposerSubmit";
import { useComposerInputHandlers } from "./composer/useComposerInputHandlers";
import { memo } from "react";
import { ArrowUp, CornerUpRight, Loader2, Paperclip, Play, Plus, Square, X } from "lucide-react";
import { OptionMenu, OptionMenuContent, OptionMenuLabel, OptionMenuTrigger } from "../ui/option-menu";
import { ComposerMenuRow } from "./turn-settings/composer-menu";
import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { ChatAnnotationSummary } from "./ChatAnnotationSummary";
import { cn } from "../../lib/utils";
import { ComposerSuggestMenu } from "./ComposerSuggestMenu";
import { ComposerEditor } from "./ComposerEditor";
import { File } from "lucide-react";
import { writeChatExcerptReferences } from "../../lib/chat-drafts";
import { Spinner } from "../ui/spinner";

/**
 * The Chat composer.
 *
 * Submitting is a typed send, not a keystroke: there is no notion of "press
 * Enter at the agent" here, and an empty message is never a way to nudge it.
 *
 * A message typed mid-turn is held by the daemon and sent when the turn ends,
 * because the agent is one conversation and cannot run a second turn alongside
 * the first. The placeholder says so rather than leaving the user to guess where
 * their text went, and the queued message stays visible only in the dock.
 *
 * The model, reasoning effort and approval controls belong here rather than in
 * settings because the provider takes all three per turn: choosing one changes the
 * next message and never restarts the agent.
 *
 * Three completions live in the editor — `/` for Tallpine commands and the agent's own
 * skills, `@` for worktree files, and pasted or dropped files. Completed skills
 * and paths are atomic inline chips but serialize to the plain text the agent
 * expects. The original keyboard contract remains: Enter sends, Shift+Enter makes
 * a newline, and ordinary typing stays local to the editor instead of rerendering
 * the surrounding chat surface.
 *
 * Every affordance is conditional on being able to deliver. The `/` menu only opens
 * when the provider actually reported skills, and the attach control only appears
 * when a caller supplied somewhere to put the bytes — a control that cannot do what
 * it says should not be drawn.
 */
export const ChatComposer = memo(function ChatComposer(props: ChatComposerProps) {
	const {
		willQueue,
		queuePlaceholder,
		disabled,
		disabledPlaceholder,
		starting,
		settings,
		addMenuItems,
		elicitation,
		filePathsTruncated,
		showSteerButton,
		onInterrupt,
		sendPending,
		steerPending,
		steerRefusal,
		editingQueuedTurnId,
		onCancelQueuedEdit,
		onQueuedRetainedAttachmentsChange,
		savingQueuedEditPending,
		queuedEditRecovery,
		attachedTop = false,
		queuedDock,
		emptyPlaceholder,
		promptSuggestion,
		onSelectAnnotation,
	} = props;
	const draft = useComposerDraftState(props);
	const suggestionState = useComposerSuggestions(props, draft);
	const persistence = useComposerDraftPersistence(props, draft, suggestionState);
	const submitState = useComposerSubmit(props, draft, suggestionState, persistence);
	const input = useComposerInputHandlers(props, draft, suggestionState, persistence, submitState);
	if ("earlyView" in input) return input.earlyView;
	const {
		addMenuBox,
		addMenuTrigger,
		canAttach,
		composerRevision,
		contextReferences,
		draftScope,
		dragging,
		durableDelivery,
		editor,
		fileAttachments,
		filePicker,
		hasText,
		menuId,
		optimisticSend,
		retainedAttachments,
		setAddMenuBox,
		setDragging,
		setIsComposing,
		setRetainedAttachments,
		setTextDraftPersistenceError,
		steerOutcomeNotice,
		submitInFlight,
		submitting,
		translateDraft,
		trigger,
		visibleRetainedAttachments,
	} = draft;
	const {
		activeIndex,
		canAbandonUncertainSteer,
		canContinueTurn,
		canSteerDraft,
		canStopTurn,
		composerImages,
		continueTurn,
		continuing,
		controlsDisabled,
		draftMutationPending,
		menuOpen,
		sendActionEnabled,
		sendActionLabel,
		sendConcealed,
		sendHint,
		showsSteer,
		staged,
		steersDraft,
		stripAttachments,
		suggestions,
	} = suggestionState;
	const {
		abandonUncertainSteer,
		onEditorChange,
		pick,
	} = persistence;
	const {
		completeFromEditor,
		submit,
	} = submitState;
	const {
		attachFiles,
		attachmentError,
		onDrop,
		onEditorEnter,
		onKeyDown,
		onPaste,
		removeImageFromSavedDraft,
		withQueueStack,
	} = input;

	return withQueueStack(
		<form
			// Cmd/Ctrl steering remains available as a quiet power-user action.
			onSubmit={(event) => void submit(event, steersDraft)}
				onDragOver={(event) => {
					if (!canAttach || submitInFlight.current) return;
					event.preventDefault();
					setDragging(true);
				}}
				onDragLeave={() => setDragging(false)}
				onDropCapture={onDrop}
				// The border colors for rest, hover, focus and drag are one set of states
				// on one surface, so they are declared together in CSS rather than half
				// here and half there.
				data-dragging={dragging || undefined}
				data-starting={starting || undefined}
				aria-busy={starting || undefined}
				data-attached-top={attachedTop && !queuedDock && !elicitation ? true : undefined}
			onClick={(e) => {
					if (controlsDisabled) return;
					// Portal menus still bubble clicks here; focusing the editor would close them.
					if (!e.currentTarget.contains(e.target as Node)) return;
					if ((e.target as HTMLElement).closest('[contenteditable="true"]')) return;
					// The focusable context tooltip must keep its focus on click.
					if (
						e.target === e.currentTarget ||
						!(e.target as HTMLElement).closest("button, a, [role='option'], [data-context-meter], ul")
					) {
						editor.current?.focus();
					}
				}}
				className="cursor-chat-composer relative flex cursor-text flex-col gap-1.5 px-3 pt-3 pb-3"
			>
				<span aria-hidden="true" className="chat-composer-startup-shimmer" />
				{menuOpen && trigger ? (
					<ComposerSuggestMenu
						id={menuId}
						kind={trigger.kind}
						items={suggestions}
						highlighted={activeIndex}
						onPick={pick}
						truncated={trigger?.kind === "file" && filePathsTruncated}
					/>
				) : null}

				{editingQueuedTurnId ? (
					<div className="flex items-center justify-between text-xs text-muted-foreground">
						<span>Editing queued message</span>
						<button
							type="button"
							disabled={controlsDisabled || queuedEditRecovery}
							onClick={onCancelQueuedEdit}
							className="rounded px-1.5 py-0.5 hover:text-foreground focus-visible:outline focus-visible:outline-ring"
						>
							Cancel edit
						</button>
					</div>
				) : null}
				{staged || contextReferences.length > 0 ? (
					<div className="flex flex-wrap items-center gap-1.5" data-testid="composer-attachments-and-annotations">
						{staged ? (
							<ul className="contents" aria-label="Attached files">
								{stripAttachments.map(({ file, path, preview }) => {
									return (
									<li
										key={file.id}
										className="flex items-center gap-1.5 rounded bg-interactive-hover py-0.5 pl-0.5 pr-1"
									>
										{preview ? (
											<img src={preview} alt="" className="size-6 rounded-sm object-cover" />
										) : (
											<div className="flex size-6 items-center justify-center rounded-sm bg-surface">
												<File aria-hidden="true" className="size-3.5 text-muted-foreground" />
											</div>
										)}
										<span
											className="max-w-[120px] truncate text-xs text-muted-foreground"
											title={file.name}
										>
											{file.name}
										</span>
										<button
											type="button"
											onClick={() => {
											if (submitInFlight.current) return;
											if (path) {
											editor.current?.removeImage(path);
											removeImageFromSavedDraft(path);
										}
											if (visibleRetainedAttachments.some((attachment) => attachment.id === file.id)) {
												const next = retainedAttachments.filter((attachment) => attachment.id !== file.id);
												setRetainedAttachments(next);
												onQueuedRetainedAttachmentsChange?.(next);
											} else fileAttachments.remove(file.id);
											}}
											disabled={controlsDisabled || queuedEditRecovery || draftMutationPending || fileAttachments.preparing}
											aria-label={`Remove ${file.name}`}
											className="text-muted-foreground hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50"
										>
											<X aria-hidden="true" className="size-3" />
										</button>
									</li>
									);
								})}
							</ul>
						) : null}
						{contextReferences.length > 0 ? (
							<ChatAnnotationSummary
								annotations={contextReferences}
								onSelect={onSelectAnnotation}
								disabled={controlsDisabled || draftMutationPending}
								onRemoveAll={() => {
									if (!draftScope || submitInFlight.current) return;
									const result = writeChatExcerptReferences(draftScope, []);
									composerRevision.current = result.draft.composer.revision;
									setTextDraftPersistenceError(result.ok ? null : "chat.draft.saveFailed");
								}}
								onRemove={(excerpt) => {
									if (!draftScope || submitInFlight.current || !excerpt.id) return;
									const result = writeChatExcerptReferences(
										draftScope,
										contextReferences.filter((item) => item.id !== excerpt.id),
									);
									composerRevision.current = result.draft.composer.revision;
									setTextDraftPersistenceError(result.ok ? null : "chat.draft.saveFailed");
								}}
							/>
						) : null}
					</div>
				) : null}

				<ComposerEditor
					ref={editor}
					images={composerImages}
					promptSuggestion={willQueue || starting || editingQueuedTurnId || staged ? undefined : promptSuggestion}
					disabled={Boolean(disabled || queuedEditRecovery || (!optimisticSend && (submitting || draftMutationPending)))}
					concealed={sendConcealed && !optimisticSend}
					label="Message the agent"
					placeholder={
						disabledPlaceholder ?? (disabled
							? "The controller is not connected"
							: willQueue
								? (queuePlaceholder ?? "Next message you send will be queued")
								: emptyPlaceholder ?? "Message the agent")
					}
					menuOpen={menuOpen}
					menuId={menuId}
					activeIndex={activeIndex}
					onChange={onEditorChange}
					onComplete={completeFromEditor}
					onEnter={onEditorEnter}
					onCompositionChange={setIsComposing}
					onKeyDown={onKeyDown}
					onPaste={onPaste}
				/>

				{attachmentError ? (
					<p role="alert" className="px-1.5 text-xs leading-snug text-destructive">
						{translateDraft(attachmentError)}
					</p>
				) : null}
				{canAbandonUncertainSteer ? (
					<div className="px-1.5">
						<Button
							type="button"
							variant="outline"
							size="sm"
							onClick={abandonUncertainSteer}
						>
							{translateDraft("chat.draft.abandon")}
						</Button>
					</div>
				) : null}
				{fileAttachments.preparing ? (
					<p role="status" className="px-1.5 text-xs leading-snug text-muted-foreground">
						Saving attachments… Wait before leaving this chat.
					</p>
				) : null}

				{/* A refused steer is an ordinary outcome, not a failure: the text is still
			    in the box and the message says which of "send it instead" and "try again
			    in a moment" applies. */}
				{steerRefusal ?? steerOutcomeNotice ? (
					<p role="status" className="px-1.5 text-xs leading-snug text-warning">
						{translateDraft(steerRefusal ?? steerOutcomeNotice)}
					</p>
				) : null}

				<div className="flex h-7 items-center gap-1.5">
					<div role="group" aria-label="Message tools" className="flex min-w-0 flex-1 items-center gap-0.5">
						{canAttach ? (
							<>
								<input
									ref={filePicker}
									type="file"
									multiple
									hidden
									disabled={controlsDisabled}
									onChange={(event) => {
										if (!disabled && !submitInFlight.current && !draftMutationPending && !fileAttachments.preparing) {
											attachFiles(Array.from(event.target.files ?? []));
										}
										// Cleared so picking the same file twice still fires a change.
										event.target.value = "";
									}}
								/>
								{addMenuItems ? (
									<OptionMenu
										onOpenChange={(open) => {
											// Measured on open: the panel sits on the chat box, as wide as it.
											const button = addMenuTrigger.current;
											const box = button?.closest("form")?.getBoundingClientRect();
											const origin = button?.getBoundingClientRect();
											if (open && box && origin) setAddMenuBox({ lift: origin.top - box.top + 6, shift: box.left - origin.left, width: box.width });
										}}
									>
										<OptionMenuTrigger
											ref={addMenuTrigger}
											showCaret={false}
											disabled={controlsDisabled}
											aria-label="Add"
											title="Add files or change mode"
											className="size-7 shrink-0 justify-center rounded-full bg-transparent p-0 text-muted-foreground hover:bg-interactive-active hover:text-foreground data-[state=open]:bg-interactive-active"
										>
											<Plus aria-hidden="true" className="size-3.5" />
										</OptionMenuTrigger>
										<OptionMenuContent
											align="start"
											side="top"
											sideOffset={addMenuBox?.lift}
											alignOffset={addMenuBox?.shift}
											avoidCollisions={false}
											style={addMenuBox ? { width: addMenuBox.width } : undefined}
											className="chat-settings-menu max-w-none text-[12px]!"
										>
											<OptionMenuLabel className="px-3 pt-1.5 pb-1 text-[11px]!">Add</OptionMenuLabel>
											<ComposerMenuRow
												icon={Paperclip}
												title="Files"
												description="Attach files to this message"
												disabled={queuedEditRecovery || draftMutationPending || fileAttachments.preparing}
												onSelect={() => filePicker.current?.click()}
											/>
											{addMenuItems}
										</OptionMenuContent>
									</OptionMenu>
								) : (
								<Tooltip>
									<TooltipTrigger asChild>
										<span className="inline-flex">
											<Button
												type="button"
												variant="ghost"
												size="icon-sm"
												disabled={controlsDisabled || queuedEditRecovery || draftMutationPending || fileAttachments.preparing}
												onClick={() => filePicker.current?.click()}
												aria-label="Attach a file"
												className="size-7 shrink-0 rounded-full p-0 text-muted-foreground hover:bg-interactive-active! hover:text-foreground"
											>
												<Plus aria-hidden="true" className="size-3.5 text-muted-foreground" />
											</Button>
										</span>
									</TooltipTrigger>
									<TooltipContent side="bottom">Attach a file</TooltipContent>
								</Tooltip>
								)}
							</>
						) : null}
						{settings}
					</div>

					<div role="group" aria-label="Send message controls" className="flex h-7 shrink-0 items-center">
						{showSteerButton && canSteerDraft && (hasText || staged) ? (
							<Button type="button" variant="ghost" size="sm" disabled={!sendActionEnabled} onClick={() => void submit(undefined, true)} aria-label="Steer into running turn" className="h-7 px-2 text-xs">
								Steer
							</Button>
						) : null}
						<Tooltip>
							<TooltipTrigger asChild>
								<span className="inline-flex">
									<Button
										type={canStopTurn || canContinueTurn ? "button" : "submit"}
										variant="ghost"
										size="icon-sm"
										disabled={canStopTurn ? false : canContinueTurn ? continuing : !sendActionEnabled}
										onClick={canStopTurn ? onInterrupt : canContinueTurn ? continueTurn : undefined}
										aria-label={canStopTurn ? "Stop turn" : canContinueTurn ? "Continue turn" : sendActionLabel}
										className={cn(
											"size-7 rounded-full border-transparent focus-visible:ring-ring/40",
											canStopTurn || canContinueTurn || sendActionEnabled
												? "bg-foreground text-on-foreground hover:bg-foreground/90 hover:text-on-foreground dark:hover:bg-foreground/90 dark:hover:text-on-foreground"
												: "bg-primary text-primary-foreground",
										)}
									>
										{canStopTurn ? (
											<Square aria-hidden="true" className="size-2.5 fill-current" />
										) : canContinueTurn && !continuing ? (
											<Play aria-hidden="true" className="size-3 translate-x-px fill-current" />
										) : continuing || submitting || steerPending || savingQueuedEditPending || sendPending ? (
											<Spinner icon={Loader2} className="size-3.5" />
										) : showsSteer ? (
											<CornerUpRight aria-hidden="true" className="size-3.5" />
										) : (
											<ArrowUp aria-hidden="true" className="size-3.5" />
										)}
									</Button>
								</span>
							</TooltipTrigger>
							<TooltipContent side="bottom">{canStopTurn ? "Stop turn" : canContinueTurn ? "Continue from where it stopped" : durableDelivery ? sendActionLabel : sendHint}</TooltipContent>
						</Tooltip>
					</div>
				</div>
			</form>,
	);
});
