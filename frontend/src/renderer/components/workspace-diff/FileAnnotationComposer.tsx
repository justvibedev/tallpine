/** File annotation composer and send bar. */
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Check, LoaderCircle, Send as SendIcon } from "lucide-react";
import { tallpineBridge } from "../../lib/bridge";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Spinner } from "../ui/spinner";
import { FileAnnotationModel, ActiveFileAnnotationTarget } from "./diff-annotation-types";

// Like the browser's annotation box: one rounded card with a single
// auto-growing line, and the send shortcut, Cancel and Send underneath so the
// way out and the way forward are both obvious. ⌘/Ctrl+Enter sends (plain Enter
// adds a line, as the hint says), Esc cancels.
export const COMPOSER_MAX_HEIGHT_PX = 160;

export function FileAnnotationComposer({ annotation, target }: { annotation: FileAnnotationModel; target: ActiveFileAnnotationTarget }) {
	const { t } = useTranslation();
	const textareaRef = useRef<HTMLTextAreaElement>(null);
	// Typing stays local to the box, so the diffs that read the shared model
	// don't re-render per keystroke; the model hears the text on send, when the
	// box loses focus (so a comment started elsewhere, or a send from another
	// box, sees it), or when the box goes away (a virtualized row can unmount
	// and remount it).
	const [text, setText] = useState(() => annotation.draftFor(target));
	const textRef = useRef(text);
	textRef.current = text;
	const saveDraftRef = useRef(() => annotation.setDraft(target, textRef.current));
	saveDraftRef.current = () => annotation.setDraft(target, textRef.current);
	useEffect(() => () => saveDraftRef.current(), []);
	// Only the newest box takes focus, so an older one scrolling back into view
	// doesn't pull the caret away from the comment being written.
	const newest = annotation.targets[annotation.targets.length - 1] === target;
	useEffect(() => {
		if (!newest) return;
		const frame = window.requestAnimationFrame(() => textareaRef.current?.focus({ preventScroll: true }));
		return () => window.cancelAnimationFrame(frame);
	}, [newest, target]);
	const fitHeight = useCallback(() => {
		const textarea = textareaRef.current;
		if (!textarea) return;
		// Back to one row first; an empty box stays one row, so a placeholder that
		// wrapped while a popover was still settling its width can't size it.
		textarea.style.height = "";
		if (!textarea.value) return;
		textarea.style.height = `${Math.min(textarea.scrollHeight, COMPOSER_MAX_HEIGHT_PX)}px`;
	}, []);
	useLayoutEffect(fitHeight, [fitHeight, text, target]);
	// Text rewraps when the box's width changes (popover settling, panel resize).
	useEffect(() => {
		const textarea = textareaRef.current;
		if (!textarea || typeof ResizeObserver === "undefined") return;
		let width = textarea.clientWidth;
		const observer = new ResizeObserver(() => {
			if (textarea.clientWidth === width) return;
			width = textarea.clientWidth;
			fitHeight();
		});
		observer.observe(textarea);
		return () => observer.disconnect();
	}, [fitHeight, target]);
	const side = target.side === "file" ? "" : t(target.side === "old" ? "files.oldSide" : "files.newSide");
	const targetLabel =
		target.quote != null
			? t("files.selectionFeedbackTarget", { file: target.path })
			: target.side === "file"
			? t("files.fileFeedbackTarget", { file: target.path })
			: t("files.lineFeedbackTarget", { file: target.path, line: target.line, side });
	// The quoted page selection, unless the box has its own text selected.
	const quoteToCopy = (box: HTMLTextAreaElement) => (box.selectionStart === box.selectionEnd ? target.quote : undefined);
	const status = annotation.statusFor(target);
	const sending = status === "sending";
	const sent = status === "sent";
	const submit = () => {
		if (!text.trim() || sending || sent) return;
		void annotation.submit(target, text);
	};
	// The button sends this box; the shortcut sends every written comment, so
	// a review with several boxes open goes out in one keystroke.
	const writtenCount = annotation.targets.filter((open) => (open === target ? text : annotation.draftFor(open)).trim()).length;
	const submitAll = () => {
		if (sending || sent) return;
		annotation.setDraft(target, text);
		void annotation.submit();
	};

	return (
		<div className="p-2 font-sans">
			<form
				className="rounded-2xl border border-border bg-background px-2.5 py-2"
				onSubmit={(event) => {
					event.preventDefault();
					submit();
				}}
			>
				<textarea
					aria-label={t("files.feedbackLabel", { target: targetLabel })}
					className={cn(
						"board-scrollbar block min-h-7 w-full resize-none border-0 bg-transparent py-1 text-[13px] leading-5 text-foreground outline-none placeholder:text-muted-foreground disabled:opacity-60",
						// Empty, a placeholder that wraps in a narrow column is clipped rather
						// than growing a scrollbar in a one-line box.
						text ? "overflow-y-auto" : "overflow-hidden",
					)}
					disabled={sending || sent}
					onBlur={() => annotation.setDraft(target, text)}
					// Focusing the box clears the page selection it was opened from, so
					// Cmd/Ctrl+C (or Edit > Copy) with nothing selected in the box copies
					// the quoted selection.
					onCopy={(event) => {
						const quote = quoteToCopy(event.currentTarget);
						if (quote === undefined) return;
						event.preventDefault();
						event.clipboardData.setData("text/plain", quote);
					}}
					onChange={(event) => {
						const next = event.target.value;
						setText(next);
						// The review bar counts written comments, so tell the model when
						// this box starts or stops having text, not on every keystroke.
						if (Boolean(next.trim()) !== Boolean(text.trim())) annotation.setDraft(target, next);
					}}
					onKeyDown={(event) => {
						if (event.key === "Escape") {
							event.preventDefault();
							annotation.cancel(target);
						} else if (event.key === "Enter" && (event.metaKey || event.ctrlKey) && !event.nativeEvent.isComposing) {
							event.preventDefault();
							submitAll();
						} else if (event.key.toLowerCase() === "c" && (event.metaKey || event.ctrlKey) && !event.altKey && !event.shiftKey) {
							const quote = quoteToCopy(event.currentTarget);
							if (quote === undefined) return;
							event.preventDefault();
							void tallpineBridge.clipboard.writeText(quote);
						}
					}}
					placeholder={t("files.feedbackPlaceholder")}
					ref={textareaRef}
					rows={1}
					title={targetLabel}
					value={text}
				/>
				{status === "error" ? (
					<p className="pt-1 text-xs text-error" role="alert">
						{annotation.error}
					</p>
				) : null}
				<div className="mt-1 flex items-center justify-end gap-1">
					{/* Truncates rather than squeezing the buttons in a narrow split column. */}
					<span className="mr-auto min-w-0 truncate text-caption text-passive">{writtenCount > 1 ? t("files.feedbackShortcutAll") : t("files.feedbackShortcut")}</span>
					<Button
						className="px-2 text-xs text-muted-foreground hover:text-foreground"
						disabled={sending}
						onClick={() => annotation.cancel(target)}
						size="sm"
						type="button"
						variant="ghost"
					>
						{t("files.cancelFeedback")}
					</Button>
					<Button
						aria-label={sent ? t("files.feedbackSent") : t("files.sendFeedback")}
						className="text-muted-foreground hover:text-foreground disabled:opacity-100"
						disabled={!text.trim() || sending || sent}
						size="icon-sm"
						title={sent ? t("files.feedbackSent") : t("files.sendFeedback")}
						type="submit"
						variant="ghost"
					>
						{sending ? <Spinner icon={LoaderCircle} className="" /> : sent ? <Check aria-hidden="true" className="text-success" /> : <SendIcon aria-hidden="true" className={cn(!text.trim() && "opacity-50")} />}
					</Button>
				</div>
			</form>
		</div>
	);
}

// Each box sends its own comment; once several are written, this bar sends
// or drops them all together. `surface` keeps it to the pane that holds
// comments; the send covers every pane's.
export function FileAnnotationSendBar({ annotation, className, surface }: { annotation: FileAnnotationModel; className?: string; surface: "focused" | "review" }) {
	const { t } = useTranslation();
	const written = annotation.targets.filter((target) => annotation.draftFor(target).trim());
	const here = written.some((target) => (surface === "review" ? target.surface !== "focused" : target.surface !== "review"));
	if (!here || written.length < 2) return null;
	const sending = annotation.status === "sending";
	const sent = annotation.status === "sent";
	return (
		<div className={cn("flex min-h-9 shrink-0 items-center gap-1 border-t border-border bg-background px-3 py-1 font-sans", className)} data-testid="file-feedback-bar">
			<span className="mr-auto min-w-0 truncate text-xs text-muted-foreground" role="status">
				{annotation.status === "error" ? <span className="text-error">{annotation.error}</span> : sent ? t("files.feedbackSent") : null}
			</span>
			<Button className="px-2 text-xs text-muted-foreground hover:bg-error/10 hover:text-error focus-visible:text-error active:bg-error/15 dark:hover:bg-error/10" disabled={sending || sent} onClick={() => annotation.cancel()} size="sm" type="button" variant="ghost">
				{t("files.discardFeedback")}
			</Button>
			<Button className="px-2 text-xs" disabled={sending || sent} onClick={() => void annotation.submit()} size="sm" type="button" variant="primary">
				{sending ? <Spinner icon={LoaderCircle} className="" /> : sent ? <Check aria-hidden="true" /> : <SendIcon aria-hidden="true" />}
				{t("files.sendAllFeedback", { count: written.length })}
			</Button>
		</div>
	);
}
