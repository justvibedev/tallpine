import { PRODUCT_NAME } from "../../../../shared/brand";
import { useMemo, useState } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { ChevronRight, Loader2 } from "lucide-react";
import { cn } from "../../../lib/utils";
import { HighlightedCode } from "../HighlightedCode";
import { ACTIVITY_SUMMARY_BUTTON_CLASS } from "../activity-command";
import { type ConversationActivity } from "../../../types/conversation";
import { formatBytes, formatJson, lastLine } from "./format";
import { Spinner } from "../../ui/spinner";

/**
 * A call to a tool served by an MCP server.
 *
 * Deliberately not shaped like a command row. These used to render as though the
 * agent had run something in the worktree, which is a different and more alarming
 * claim than "it asked a tool server a question": nothing was executed here, and the
 * cwd it appeared to run in did not exist. So the row names the server and the tool,
 * and opens onto the arguments and the answer rather than onto terminal output.
 */
export function McpToolRow({ activity }: { activity: ConversationActivity }) {
	const [open, setOpen] = useState(false);
	const reducedMotion = useReducedMotion();
	const detail = activity.detail;
	const tool = detail?.toolName ?? activity.summary;
	const server = detail?.server ?? detail?.namespace;
	const sourceLabel = server ? `MCP · ${server}` : detail?.progress ? lastLine(detail.progress) : undefined;
	const failed = activity.status === "failed" || detail?.success === false || Boolean(detail?.error);
	const hasBody = Boolean(
			detail?.arguments !== undefined ||
			detail?.result !== undefined ||
			detail?.content !== undefined ||
			detail?.error ||
			detail?.progress,
	);

	return (
		<div className="min-w-0 max-w-full">
			<button
				type="button"
				onClick={() => setOpen((prev) => !prev)}
				disabled={!hasBody}
				aria-expanded={hasBody ? open : undefined}
				className={cn(
					ACTIVITY_SUMMARY_BUTTON_CLASS,
					"activity-row-toggle",
					!hasBody && "cursor-default",
				)}
			>
				<strong
					className={cn(
						"activity-row-label shrink-0 text-xs font-normal",
						failed ? "text-destructive" : "text-muted-foreground",
					)}
				>
					<span>{tool}</span>
				</strong>
				{sourceLabel ? (
					<span className="min-w-0 flex-1 truncate text-xs text-muted-foreground group-hover/activity:text-foreground">
						{sourceLabel}
					</span>
				) : null}
				{activity.status === "running" ? (
					<Spinner icon={Loader2}
						aria-label="running"
						
					 className="size-3 shrink-0 text-muted-foreground/60" />
				) : failed ? (
					<span className="shrink-0 text-xs text-destructive">failed</span>
				) : activity.status === "recovered" ? (
					<span className="shrink-0 text-xs text-muted-foreground/70">outcome unknown</span>
				) : activity.status === "cancelled" ? (
					<span className="shrink-0 text-xs text-muted-foreground/70">stopped</span>
				) : hasBody ? (
					<ChevronRight
						aria-hidden="true"
						className={cn("size-3 shrink-0 text-muted-foreground/40 transition-transform", open && "rotate-90")}
					/>
				) : null}
			</button>

			<AnimatePresence initial={false}>
				{open && hasBody ? (
					<motion.div
						initial={{ height: 0, opacity: 0 }}
						animate={{ height: "auto", opacity: 1 }}
						exit={{ height: 0, opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.18, ease: [0.22, 1, 0.36, 1] }}
						className="overflow-hidden rounded-lg"
					>
						<div className="flex flex-col gap-2 pb-2.5">
					{detail?.error ? (
						<p className="border border-destructive/30 bg-background px-2.5 py-1.5 text-xs leading-relaxed text-destructive">
							{detail.error}
						</p>
					) : null}
					{detail?.arguments !== undefined ? (
						<JsonPayload label="Arguments" value={detail.arguments} />
					) : null}
					{detail?.result !== undefined ? (
						<JsonPayload label="Result" value={detail.result} />
					) : null}
					{detail?.content !== undefined ? <ToolContent value={detail.content} /> : null}
					{detail?.progress ? (
						<div className="flex flex-col gap-1">
							<span className="text-caption text-muted-foreground/70">
								Progress
							</span>
							<pre className="scrollbar-none max-h-40 overflow-auto border border-border bg-background px-2.5 py-1.5 font-mono text-caption leading-relaxed text-muted-foreground">
								{detail.progress}
							</pre>
						</div>
					) : null}
						</div>
					</motion.div>
				) : null}
			</AnimatePresence>
		</div>
	);
}

function ToolContent({ value }: { value: unknown }) {
	const text = normalizeToolCode(toolContentText(value));
	if (text) {
		if (looksLikeUnifiedDiff(text)) return <ToolDiffCode text={text} />;
		return (
			<pre className="chat-code max-h-64 overflow-auto whitespace-pre rounded-lg border border-border bg-background px-2.5 py-1.5">
				<code className="block whitespace-pre font-mono text-caption leading-relaxed text-muted-foreground">
					{ text }
				</code>
			</pre>
		);
	}
	const truncated = truncationNote(value);
	return (
		<pre className="chat-code max-h-56 overflow-auto rounded-lg border border-border bg-background px-2.5 py-1.5">
			<code className="font-mono text-caption leading-[1.55] text-foreground">
				{truncated ?? formatJson(value)}
			</code>
		</pre>
	);
}

function normalizeToolCode(text: string): string {
	const fenced = text.match(/^\s*```[^\n]*\n([\s\S]*?)\n```\s*$/);
	return (fenced?.[1] ?? text).replace(/\r\n?/g, "\n");
}

function looksLikeUnifiedDiff(text: string): boolean {
	const lines = text.split("\n");
	return (
		lines.some((line) => line.startsWith("@@")) ||
		(lines.some((line) => line.startsWith("---")) && lines.some((line) => line.startsWith("+++"))) ||
		(lines.some((line) => line.startsWith("-")) && lines.some((line) => line.startsWith("+")))
	);
}

export function ToolDiffCode({ text }: { text: string }) {
	return (
		<div className="chat-code max-h-64 overflow-auto rounded-lg border border-border bg-background px-0 py-1 font-mono text-caption leading-[1.55]">
			{ text.split("\n").map((line, index) => {
				const kind = line.startsWith("+") ? "add" : line.startsWith("-") ? "del" : "context";
				const marker = kind === "add" ? "+" : kind === "del" ? "-" : " ";
				const content = kind === "context" ? line : line.slice(1);
				return (
					<div
						key={`${index}-${line}`}
						data-tool-diff-row=""
						className={cn(
							"flex min-w-max whitespace-pre px-2.5",
							kind === "add" && "bg-success/10",
							kind === "del" && "bg-error/10",
						)}
					>
						<span
							aria-hidden="true"
							className={cn(
								"w-4 shrink-0 select-none text-center",
								kind === "add" && "text-success",
								kind === "del" && "text-error",
							)}
						>
							{marker}
						</span>
						<span className="whitespace-pre text-foreground/90">{content}</span>
					</div>
				);
			})}
		</div>
	);
}

function toolContentText(value: unknown): string {
	if (typeof value === "string") return value;
	if (Array.isArray(value)) return value.map(toolContentText).filter(Boolean).join("\n");
	if (!value || typeof value !== "object") return "";
	const record = value as Record<string, unknown>;
	for (const key of ["text", "content", "output", "data"]) {
		const text = toolContentText(record[key]);
		if (text) return text;
	}
	return "";
}

/**
 * A tool's arguments or answer, as JSON.
 *
 * A payload the daemon could not store arrives as a stand-in object rather than as
 * the real thing, and printing that stand-in as if it were the tool's answer would
 * be a small lie with a confusing shape. It is recognized and said plainly instead.
 */
function JsonPayload({ label, value }: { label: string; value: unknown }) {
	const capped = truncationNote(value);
	const text = useMemo(() => (capped ? "" : formatJson(value)), [capped, value]);

	return (
		<div className="flex flex-col gap-1">
			<span className="text-caption text-muted-foreground/70">
				{label}
			</span>
			{capped ? (
				<p className="border border-border bg-background px-2.5 py-1.5 text-caption leading-relaxed text-muted-foreground">
					{capped}
				</p>
			) : (
				<pre className="chat-code max-h-56 overflow-auto rounded-lg border border-border bg-background px-2.5 py-1.5">
					<code className="font-mono text-caption leading-[1.55] text-foreground">
						<HighlightedCode code={text} language="json" />
					</code>
				</pre>
			)}
		</div>
	);
}

/** The daemon's stand-in for a payload past its cap, or nothing. */
function truncationNote(value: unknown): string | undefined {
	if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
	const record = value as { truncated?: unknown; bytes?: unknown; note?: unknown };
	if (record.truncated !== true) return undefined;
	const bytes = typeof record.bytes === "number" ? ` (${formatBytes(record.bytes)})` : "";
	return `This payload${bytes} was larger than ${PRODUCT_NAME} stores, so it was not kept.`;
}
