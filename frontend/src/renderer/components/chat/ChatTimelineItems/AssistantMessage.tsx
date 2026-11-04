import { useEffect, useMemo, useState, type ReactNode } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { Loader2, Undo2, X } from "lucide-react";
import { cn } from "../../../lib/utils";
import { ChatMarkdown } from "../ChatMarkdown";
import { CopyButton } from "../CopyButton";
import { Tooltip, TooltipContent, TooltipTrigger } from "../../ui/tooltip";
import { type ConversationMessage } from "../../../types/conversation";
import { formatDuration, formatMessageTimestamp } from "./format";
import { useSmoothStreamingText } from "./streaming";
import { Spinner } from "../../ui/spinner";

export function ResponseSpinner() {
	return (
		<Spinner icon={Loader2}
			data-testid="response-spinner"
			
		 className="size-3 shrink-0 text-muted-foreground" />
	);
}

/** The agent's prose. Streaming is represented by text arriving in place. */
export function AssistantMessage({
	message,
	showCopy = false,
	live = false,
	onRollback,
	rollbackDisabled = false,
}: {
	message: ConversationMessage;
	/** The final answer owns the copy action; it stays available while that answer streams. */
	showCopy?: boolean;
	/** The enclosing turn is still active, even if its last text chunk has landed. */
	live?: boolean;
	/**
	 * Discard this turn and everything after it. Lives next to copy so the finished
	 * answer owns both "keep this" and "undo from here".
	 */
	onRollback?: () => void;
	/** Keep the rollback action mounted while another response is streaming. */
	rollbackDisabled?: boolean;
}) {
	const visibleText = useSmoothStreamingText(message);
	const renderingStreaming = message.streaming || visibleText.length < message.text.length;
	// Copy is ready the moment the text has fully arrived: it copies the stored markdown,
	// so it need not wait for the typewriter to catch up or for the turn to settle.
	// Rollback acts on the turn, so it waits for both.
	const copyReady = showCopy && !message.streaming;
	const rollbackReady = Boolean(onRollback) && !live && !renderingStreaming;
	const showActions = copyReady || rollbackReady;
	return (
		<div
			className="group/message relative"
			data-chat-streaming-output={renderingStreaming ? "" : undefined}
			data-chat-message-id={!live && !renderingStreaming ? message.id : undefined}
			data-chat-message-revision={!live && !renderingStreaming ? message.revision : undefined}
			data-chat-message-role={!live && !renderingStreaming ? message.role : undefined}
		>
			<div data-chat-message-body=""><ChatMarkdown text={visibleText} streaming={renderingStreaming} /></div>
			{showActions ? (
				// One action row for the completed answer, not one after every prose
				// fragment the provider emitted while working. Copy, rollback, and
				// duration stay visible; only the wall-clock time reveals on hover.
				<div className="mt-1 flex h-7 items-center gap-0.5">
					{copyReady ? (
						<div className="-ml-1.5 size-7 shrink-0">
							{/* The stored markdown, not a re-serialization of what was rendered:
							   pasting it into an editor has to give back what the agent wrote. */}
							<CopyButton
								text={message.text}
								label="Copy message as markdown"
								compact
								className="size-7 justify-center rounded-md px-0 py-0 transition-[scale,background-color,color] duration-150 ease-out hover:bg-interactive-hover hover:text-foreground active:scale-[0.96] motion-reduce:transition-none motion-reduce:active:scale-100"
							/>
						</div>
					) : null}
					{rollbackReady ? (
						<Tooltip>
							<TooltipTrigger asChild>
								<button
									type="button"
									onClick={onRollback}
									disabled={rollbackDisabled}
									aria-label="Roll back to here"
									className="flex size-7 items-center justify-center rounded-md text-muted-foreground transition-[scale,background-color,color] duration-150 ease-out hover:bg-interactive-hover hover:text-foreground active:scale-[0.96] disabled:pointer-events-none disabled:opacity-40 motion-reduce:transition-none motion-reduce:active:scale-100"
								>
									<Undo2 aria-hidden="true" className="size-3" />
								</button>
							</TooltipTrigger>
							<TooltipContent side="bottom">Roll back to here</TooltipContent>
						</Tooltip>
					) : null}
					<span
						className="w-auto shrink-0 px-1 text-caption tabular-nums text-muted-foreground/75 opacity-0 transition-opacity duration-150 ease-out group-hover/message:opacity-100 group-focus-within/message:opacity-100 motion-reduce:transition-none"
						aria-label={`Sent ${formatMessageTimestamp(message.createdAt)}`}
					>
						{formatMessageTimestamp(message.createdAt)}
					</span>
				</div>
			) : null}
		</div>
	);
}

export function LiveResponseStatus({ startedAt, settling = false, startupLabel, failed = false, children }: {
	startedAt?: string;
	settling?: boolean;
	startupLabel?: string;
	failed?: boolean;
	children?: ReactNode;
}) {
	const reducedMotion = useReducedMotion();
	const started = useMemo(() => {
		const parsed = startedAt ? Date.parse(startedAt) : Date.now();
		return Number.isFinite(parsed) ? parsed : Date.now();
	}, [startedAt]);
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (settling) {
			// The interval stops while settling; refresh once so the frozen label
			// matches the final duration instead of lagging up to a second.
			setNow(Date.now());
			return;
		}
		const timer = window.setInterval(() => setNow(Date.now()), 1000);
		return () => window.clearInterval(timer);
	}, [settling]);
	const elapsedMs = Math.max(0, now - started);
	const label = startupLabel ?? `${settling ? "Worked for" : "Working for"} ${formatDuration(elapsedMs)}`;
	// Settling animates opacity and transform only, never layout, so it stays smooth
	// even when the main thread is busy finishing the turn. These are keyframe
	// animations rather than transitions so they also play when the row first
	// mounts already settled. The spinner shrinks and fades in place while the label
	// slides left by the spinner's footprint (12px + 6px gap), landing exactly where
	// the settled "Worked for" row puts it. The label keeps its shimmer class
	// throughout (only the highlight fades), so the text never changes paint
	// technique and cannot blink.
	return (
		<div className="min-w-0">
			<div className="-mx-1 flex h-7 select-none items-center gap-1.5 border-b border-border px-1 py-0">
				<span
					aria-hidden={settling || undefined}
					data-settling={settling || undefined}
					className="chat-working-spinner-slot flex shrink-0 origin-center"
				>
					{failed ? <X className="size-3 text-destructive" /> : <ResponseSpinner />}
				</span>
				<span
					role={failed ? "alert" : "status"}
					data-testid="live-working-label"
					data-settling={settling || undefined}
					data-shimmer={label}
					className={cn("text-sm font-normal", failed ? "text-destructive" : "chat-working-shimmer")}
				>
					{label}
				</span>
			</div>
			<AnimatePresence initial={false}>
				{children ? (
					<motion.div
						key="startup-checklist"
						initial={false}
						animate={{ height: "auto", opacity: 1 }}
						exit={{ height: 0, opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.2, ease: "easeOut" }}
						className="overflow-hidden"
					>
						{children}
					</motion.div>
				) : null}
			</AnimatePresence>
		</div>
	);
}
