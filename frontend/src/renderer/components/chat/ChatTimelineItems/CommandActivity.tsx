import { PRODUCT_NAME } from "../../../../shared/brand";
import { useEffect, useMemo, useRef } from "react";
import { Keyboard } from "lucide-react";
import { cn } from "../../../lib/utils";
import { caretNotation, stripAnsi } from "../../../lib/ansi";
import { commandBinaryLabel } from "../activity-command";
import { type ConversationActivity } from "../../../types/conversation";

/**
 * Expanded command / explore body: one soft chat-surface card with the shell
 * line nested in its own chip, then muted monospace output underneath — the
 * same anatomy as the Cursor explore block, restated in Tallpine chat tokens.
 */
export function CommandExploreBody({ activity }: { activity: ConversationActivity }) {
	const detail = activity.detail;
	const command = detail?.command?.trim();
	const reason = (detail?.reason ?? detail?.text)?.trim();
	const binary = command ? commandBinaryLabel(command) : undefined;
	const showPrompt = Boolean(reason && reason !== command);

	return (
		<div className="cursor-chat-explore-box mt-1 flex min-w-0 flex-col overflow-hidden rounded-lg border">
			{showPrompt ? (
				<div className="flex min-w-0 items-start gap-2 border-b border-border/60 px-3 py-2">
					<span
						aria-hidden="true"
						className="shrink-0 select-none pt-px font-mono text-caption leading-relaxed text-muted-foreground/70"
					>
						&gt;_
					</span>
					<div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2 gap-y-0.5">
						<span className="min-w-0 break-words text-xs leading-relaxed text-foreground/90">
							{reason}
						</span>
						{binary ? (
							<span className="shrink-0 font-mono text-caption text-muted-foreground/55">
								{binary}
							</span>
						) : null}
					</div>
				</div>
			) : null}

			{command ? (
				<pre
					className={cn(
						"cursor-chat-explore-command overflow-x-auto px-3 py-2 font-mono text-caption leading-relaxed text-foreground/85",
						(detail?.output || detail?.terminalInput) && "border-b border-border/60",
					)}
				>
					{command}
				</pre>
			) : null}

			{detail?.terminalInput ? (
				<div className={cn("px-3 py-2", detail?.output && "border-b border-border/60")}>
					<TerminalInput
						text={detail.terminalInput}
						truncated={detail.terminalInputTruncated}
					/>
				</div>
			) : null}
			{detail?.output ? <CommandOutput activity={activity} embedded /> : null}
		</div>
	);
}

/**
 * What the agent typed into a running command's terminal.
 *
 * Shown apart from the output, and labelled as input, because it is the one thing in
 * the row the agent did rather than observed — usually `^C` on a command that was
 * never going to finish. The daemon keeps it out of `output` for the same reason it
 * is drawn separately here: the PTY echoes keystrokes, so merging them would print
 * the abort twice and leave no way to tell which was which.
 *
 * Control characters are spelled the way a terminal spells them. Stripping them
 * would leave an empty box where the interesting thing happened: an abort IS one
 * unprintable byte.
 */
export function TerminalInput({ text, truncated }: { text: string; truncated?: boolean }) {
	const shown = useMemo(() => caretNotation(text), [text]);
	return (
		<div className="flex flex-col gap-1">
			<span className="flex items-center gap-1.5 text-caption text-muted-foreground/70">
				<Keyboard aria-hidden="true" className="size-3" />
				Agent typed
			</span>
			<pre className="scrollbar-none overflow-x-auto border border-dashed border-border-strong bg-background px-2.5 py-1.5 font-mono text-caption leading-relaxed text-accent">
				{shown}
			</pre>
			{truncated ? (
				<p className="text-micro text-muted-foreground/70">
					{PRODUCT_NAME} stopped recording keystrokes at its cap; more were sent.
				</p>
			) : null}
		</div>
	);
}

/**
 * A command's output, with an honest account of what it is.
 *
 * The pre scrolls to its own end while the command runs, because output that
 * arrives below the fold is output nobody sees. It only auto-scrolls while the
 * activity is `running` and only when the reader has not scrolled up: hijacking
 * the viewport of someone reading back through a build log is worse than making
 * them scroll down once.
 *
 * The text is what a terminal would have shown, not the bytes: output arrives with
 * its escape sequences intact — nothing in the stack strips them — so a colourized
 * test run rendered here verbatim is a wall of `[0m`, and a progress bar is a
 * hundred stacked copies of itself. See `lib/ansi.ts` for why this is a text pass
 * rather than a terminal.
 */
export function CommandOutput({
	activity,
	embedded = false,
}: {
	activity: ConversationActivity;
	/** Inside the explore card: no second bordered surface. */
	embedded?: boolean;
}) {
	const pre = useRef<HTMLPreElement>(null);
	const detail = activity.detail;
	// Older ACP-backed conversations may contain the provider's structured
	// rawOutput even though Tallpine's view model promises a string. New events are
	// normalized at the adapter boundary; this compatibility read keeps those
	// already-durable rows from taking down the entire session surface.
	const raw = commandOutputText(detail?.output as unknown);
	// Memoized per row: the timeline re-renders once a second while a turn runs, and
	// only the rows a reader has opened pay for this at all.
	const output = useMemo(() => stripAnsi(raw), [raw]);
	const streaming = activity.status === "running";

	useEffect(() => {
		const node = pre.current;
		if (!node || !streaming) return;
		// Within a line of the bottom counts as "following along". Anything further
		// up is a deliberate scroll and is left alone.
		const atBottom = node.scrollHeight - node.scrollTop - node.clientHeight < 24;
		if (atBottom) node.scrollTop = node.scrollHeight;
	}, [output, streaming]);

	return (
		<>
			<pre
				ref={pre}
				aria-live={streaming ? "polite" : undefined}
				className={cn(
					"scrollbar-none max-h-64 overflow-auto font-mono leading-relaxed text-muted-foreground",
					embedded
						? "cursor-chat-explore-output px-3 py-2 text-caption"
						: "border border-border bg-background px-2.5 py-2 text-caption",
				)}
			>
				{output}
			</pre>
			{detail?.outputTruncated ? (
				<p className="text-xs leading-relaxed text-warning">
					This command printed more than {PRODUCT_NAME} stores, so the output above stops early. Open a shell in
					the worktree to see the rest.
				</p>
			) : null}
		</>
	);
}

function commandOutputText(raw: unknown): string {
	if (typeof raw === "string") return raw;
	if (!raw || typeof raw !== "object") return "";

	const value = raw as Record<string, unknown>;
	for (const key of ["output", "text", "error", "metadata"]) {
		const text = commandOutputText(value[key]);
		if (text) return text;
	}

	try {
		return JSON.stringify(raw);
	} catch {
		return "";
	}
}
