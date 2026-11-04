/**
 * What fills the context window, as the agent itself reports it (Claude Code's
 * /context): a stacked bar and one row per category with tokens and the share of
 * the window. Every figure is the agent's; Tallpine estimates nothing, so a category
 * the agent did not report is simply absent, and a row of zero is not drawn.
 *
 * Color carries no meaning here (DESIGN.md: color is rare and semantic): the bar
 * segments are steps of one neutral, in the same order as the rows, and the rows
 * carry the names and numbers.
 */

import { useTranslation } from "react-i18next";
import { formatTokenCount } from "../../lib/format-token-count";
import { cn } from "../../lib/utils";
import type { ContextBreakdownData } from "../../hooks/useContextBreakdown";

const tokens = (n: number) => formatTokenCount(n).replace(/ tok$/, "");

/** A share of the window: a sliver is never rounded to nothing. */
export function formatShare(part: number, whole: number): string {
	if (whole <= 0 || part <= 0) return "0%";
	const percent = (part / whole) * 100;
	if (percent < 0.1) return "<0.1%";
	return percent < 10 ? `${percent.toFixed(1)}%` : `${Math.round(percent)}%`;
}

// The agent's stable row ids, so a name is translated without matching English.
const CATEGORY_LABELS = {
	system_prompt: "contextMeter.breakdown.category.systemPrompt",
	system_tools: "contextMeter.breakdown.category.systemTools",
	mcp_tools: "contextMeter.breakdown.category.mcpTools",
	mcp_instructions: "contextMeter.breakdown.category.mcpInstructions",
	custom_agents: "contextMeter.breakdown.category.customAgents",
	memory_files: "contextMeter.breakdown.category.memoryFiles",
	skills: "contextMeter.breakdown.category.skills",
	messages: "contextMeter.breakdown.category.messages",
	autocompact_buffer: "contextMeter.breakdown.category.compactBuffer",
	compact_buffer: "contextMeter.breakdown.category.compactBuffer",
	free_space: "contextMeter.breakdown.category.freeSpace",
} as const;

const MESSAGE_PARTS = [
	["toolCalls", "contextMeter.breakdown.messages.toolCalls"],
	["toolResults", "contextMeter.breakdown.messages.toolResults"],
	["attachments", "contextMeter.breakdown.messages.attachments"],
	["assistant", "contextMeter.breakdown.messages.assistant"],
	["user", "contextMeter.breakdown.messages.user"],
] as const;

// Neutral steps for the used segments, strongest first.
const SEGMENT_STEPS = ["bg-foreground/80", "bg-foreground/65", "bg-foreground/50", "bg-foreground/38", "bg-foreground/28", "bg-foreground/20"];

const MAX_MEMORY_FILES = 5;

function Row({ label, value, share, className, indent }: { label: string; value: string; share?: string; className?: string; indent?: boolean }) {
	return (
		<div className={cn("flex items-baseline justify-between gap-3", indent && "pl-3 text-muted-foreground", className)}>
			<dt className="min-w-0 truncate">{label}</dt>
			<dd className="shrink-0 tabular-nums text-right">
				{value}
				{share ? <span className="ml-1.5 text-muted-foreground">{share}</span> : null}
			</dd>
		</div>
	);
}

/** The daemon's resolved threshold and capacity; the one "Compacts at" when present. */
export interface ResolvedCompaction {
	threshold: number;
	capacity: number;
}

export function ContextBreakdown({ data, resolved }: { data: ContextBreakdownData; resolved?: ResolvedCompaction }) {
	const { t } = useTranslation();
	const max = data.maxTokens ?? 0;
	const categories = data.categories ?? [];
	if (max <= 0 || categories.length === 0) return null;
	const used = categories.filter((c) => c.kind === "used" && c.tokens > 0);
	const buffer = categories.filter((c) => c.kind === "buffer" && c.tokens > 0);
	const free = categories.filter((c) => c.kind === "free" && c.tokens > 0);
	const deferred = categories.filter((c) => c.kind === "deferred" && c.tokens > 0);
	const labelOf = (c: { id?: string; name: string }) => {
		const key = c.id ? CATEGORY_LABELS[c.id as keyof typeof CATEGORY_LABELS] : undefined;
		return key ? t(key) : c.name;
	};
	const width = (n: number) => `${Math.min(100, (n / max) * 100)}%`;
	const threshold = data.autoCompactThreshold && data.autoCompactThreshold > 0 ? data.autoCompactThreshold : undefined;
	// The agent counts the messages row and its parts separately (a live session
	// reported parts summing to 4x the row). A split that contradicts its own row
	// is not shown: neither figure is ours to correct.
	const messagesRow = used.find((c) => c.id === "messages")?.tokens ?? 0;
	const partsTotal = data.messages ? Object.values(data.messages).reduce((sum, n) => sum + (n ?? 0), 0) : 0;
	const parts = data.messages && partsTotal > 0 && partsTotal <= messagesRow * 1.1 ? data.messages : undefined;
	const memoryFiles = [...(data.memoryFiles ?? [])].sort((a, b) => b.tokens - a.tokens).slice(0, MAX_MEMORY_FILES);

	return (
		<section className="mt-3 flex flex-col gap-2 border-t border-border pt-3" data-context-breakdown="" aria-label={t("contextMeter.breakdown.title")}>
			<p className="text-sm font-medium">{t("contextMeter.breakdown.title")}</p>
			<div
				role="img"
				aria-label={t("contextMeter.breakdown.barLabel", { used: tokens(data.totalTokens ?? 0), window: tokens(max) })}
				className="relative flex h-2.5 w-full gap-px overflow-hidden rounded-full bg-border"
			>
				{used.map((c, i) => (
					<span key={`${c.id ?? c.name}-${i}`} className={cn("h-full shrink-0", SEGMENT_STEPS[Math.min(i, SEGMENT_STEPS.length - 1)])} style={{ width: width(c.tokens) }} />
				))}
				{buffer.map((c) => (
					// The reserve is held back for compaction: drawn at the far end as hatching.
					<span key={c.name} className="ml-auto h-full shrink-0 bg-[repeating-linear-gradient(135deg,var(--color-border-strong)_0_2px,transparent_2px_5px)]" style={{ width: width(c.tokens) }} />
				))}
				{threshold ? <span aria-hidden="true" className="absolute inset-y-0 w-px bg-foreground" style={{ left: width(threshold) }} /> : null}
			</div>
			<dl className="flex flex-col gap-1">
				{used.map((c, i) => (
					<div key={`${c.id ?? c.name}-${i}`} className="flex flex-col gap-1">
						<Row label={labelOf(c)} value={tokens(c.tokens)} share={formatShare(c.tokens, max)} />
						{c.id === "messages" && parts
							? MESSAGE_PARTS.filter(([key]) => (parts[key] ?? 0) > 0).map(([key, label]) => (
								<Row key={key} indent label={t(label)} value={tokens(parts[key] ?? 0)} share={formatShare(parts[key] ?? 0, max)} />
							))
							: null}
					</div>
				))}
				{buffer.map((c) => <Row key={c.name} label={labelOf(c)} value={tokens(c.tokens)} share={formatShare(c.tokens, max)} className="text-muted-foreground" />)}
				{free.map((c) => <Row key={c.name} label={labelOf(c)} value={tokens(c.tokens)} share={formatShare(c.tokens, max)} className="text-muted-foreground" />)}
				{resolved ? <Row label={t("contextMeter.breakdown.compactsAt")} value={tokens(resolved.threshold)} share={formatShare(resolved.threshold, resolved.capacity)} /> : null}
				{threshold && threshold !== resolved?.threshold ? (
					<Row label={t(resolved ? "contextMeter.breakdown.agentCompactsAt" : "contextMeter.breakdown.compactsAt")} value={tokens(threshold)} share={formatShare(threshold, resolved?.capacity ?? max)} />
				) : null}
			</dl>
			{memoryFiles.length > 0 ? (
				<div className="flex flex-col gap-1">
					<p className="text-muted-foreground">{t("contextMeter.breakdown.instructionFiles")}</p>
					<dl className="flex flex-col gap-1">
						{memoryFiles.map((file) => (
							<Row key={file.path} label={file.path.split(/[\\/]/).slice(-2).join("/")} value={tokens(file.tokens)} share={formatShare(file.tokens, max)} />
						))}
					</dl>
				</div>
			) : null}
			{deferred.length > 0 ? (
				<div className="flex flex-col gap-1 text-muted-foreground">
					<p>{t("contextMeter.breakdown.notInWindow")}</p>
					<dl className="flex flex-col gap-1">
						{deferred.map((c) => <Row key={c.name} label={labelOf(c)} value={tokens(c.tokens)} />)}
					</dl>
				</div>
			) : null}
		</section>
	);
}
