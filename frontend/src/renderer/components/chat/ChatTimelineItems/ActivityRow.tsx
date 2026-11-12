import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { AlertTriangle, Bot, Brain, ChevronRight, CircleAlert, FileDiff, Gauge, Keyboard, ListChecks, Loader2, Plug, ShieldCheck, ShieldQuestion, SquareTerminal } from "lucide-react";
import { cn } from "../../../lib/utils";
import { readArtifactRef, readRenderRef } from "../../../lib/render-frame";
import { ActivityTitle, SessionLabelLink } from "../ChatMarkdown";
import { RenderFrame } from "../RenderFrame";
import { ACTIVITY_SUMMARY_BUTTON_CLASS, commandCategory, exploredFileCount, isNonzeroCommandExit } from "../activity-command";
import { actionDetailLines, describeConversationActivity } from "../activity-action";
import { fileChangeFiles, type ActivityKind, type ConversationActivity } from "../../../types/conversation";
import { AutoReviewRow, RerouteRow } from "./AutoReviewRow";
import { CommandExploreBody, TerminalInput, CommandOutput } from "./CommandActivity";
import { ErrorActivityRow } from "./ErrorActivityRow";
import { FileChangeList, fileChangePatch, Patch } from "./FileChangeList";
import { McpStatusRow } from "./McpStatusRow";
import { McpToolRow } from "./McpToolRow";
import { ChatLocalImageGrid } from "../ChatLocalImageStrip";
import { toolImagePaths } from "../chat-tool-images";
import { ReasoningBlock } from "./ReasoningBlock";
import { ProjectSetupRow, AccountSwitchedRow, ReauthRow } from "./SetupRows";
import { fileChangeVerb, FileLocationLabel, fileBasename } from "./TurnChangedFiles";
import { Spinner } from "../../ui/spinner";

/** Fixed icon column, matching the prototype's row anatomy. */
const activityIcon: Record<ActivityKind, typeof SquareTerminal> = {
	command: SquareTerminal,
	file_change: FileDiff,
	plan: ListChecks,
	reasoning: Brain,
	approval: ShieldQuestion,
	usage: Gauge,
	error: AlertTriangle,
	system: CircleAlert,
	mcp_tool: Plug,
	auto_review: ShieldCheck,
	user_input: Keyboard,
	ao_action: Bot,
};

/**
 * One activity, routed to the renderer for its kind.
 *
 * The dispatch lives here rather than in the timeline because a run collapses
 * activities too, and both paths must agree about what an MCP call looks like. A
 * kind this build does not recognize still renders as a generic row — dropping it
 * would hide work the agent really did.
 */
export function ActivityRow({ activity }: { activity: ConversationActivity }) {
	const toolActivity =
		activity.activityKind === "command" ||
		activity.activityKind === "file_change" ||
		activity.activityKind === "mcp_tool" ||
		activity.activityKind === "auto_review" ||
		activity.activityKind === "ao_action";
	// Tallpine records pages as system rows; a page named by any other row is
	// not one this daemon serves.
	const renderRef = activity.activityKind === "system" ? readRenderRef(activity.detail) : undefined;
	const artifactRef = activity.activityKind === "system" ? readArtifactRef(activity.detail) : undefined;

	let content: ReactNode;
	if (activity.activityKind === "mcp_tool") content = <McpToolRow activity={activity} />;
	else if (activity.activityKind === "auto_review") content = <AutoReviewRow activity={activity} />;
	else if (activity.activityKind === "reasoning") content = <ReasoningBlock activity={activity} />;
	else if (activity.activityKind === "error") content = <ErrorActivityRow activity={activity} />;
	else if (activity.activityKind === "ao_action") content = <TallpineActionRow activity={activity} />;
	else if (activity.activityKind === "system" && activity.detail?.event === "mcp.status") content = <McpStatusRow activity={activity} />;
	else if (activity.detail?.event === "model.rerouted") content = <RerouteRow activity={activity} />;
	else if (activity.detail?.event === "auth.reauth_required") content = <ReauthRow activity={activity} />;
	else if (activity.detail?.event === "workspace.setup") content = <ProjectSetupRow activity={activity} />;
	else if (activity.detail?.event === "account.switched") content = <AccountSwitchedRow accountId={activity.detail.accountId} />;
	else if (renderRef) content = <RenderFrame render={renderRef} />;
	else if (artifactRef) content = <RenderFrame artifact={artifactRef} />;
	else content = <GenericActivityRow activity={activity} />;

	if (!toolActivity) return content;
	return (
		<>
			<ActivityTransition value={`${activity.revision}:${activity.summary}:${activity.status}`}>
				{content}
			</ActivityTransition>
			<ToolImageThumbnails activity={activity} />
		</>
	);
}

/** Thumbnails of the images a finished tool call touched, under its row. */
function ToolImageThumbnails({ activity }: { activity: ConversationActivity }) {
	const paths = useMemo(() => toolImagePaths(activity), [activity]);
	return <ChatLocalImageGrid paths={paths} className="mb-1 mt-1 px-1" />;
}

export function ActivityTransition({
	value,
	children,
	inline = false,
}: {
	value: string;
	children: ReactNode;
	inline?: boolean;
}) {
	const reducedMotion = useReducedMotion();
	const previousValue = useRef(value);
	const [settling, setSettling] = useState(false);

	useEffect(() => {
		if (previousValue.current === value) return;
		previousValue.current = value;
		setSettling(true);
	}, [value]);

	const MotionElement = inline ? motion.span : motion.div;
	return (
		<MotionElement
			initial={reducedMotion ? false : { opacity: 0 }}
			animate={
				reducedMotion || !settling
					? { opacity: 1 }
					: { opacity: 0.72 }
			}
			transition={{ duration: reducedMotion ? 0 : 0.2, ease: [0.22, 1, 0.36, 1] }}
			onAnimationComplete={() => setSettling(false)}
		>
			{children}
		</MotionElement>
	);
}

function TallpineActionRow({ activity }: { activity: ConversationActivity }) {
	const { t } = useTranslation();
	const descriptor = describeConversationActivity(activity);
	const lines = actionDetailLines(activity);
	const [override, setOverride] = useState<boolean | null>(null);
	const reducedMotion = useReducedMotion();
	const label = descriptor?.label ?? activity.summary;
	const href = descriptor?.href;
	const hasBody = lines.length > 0 || Boolean(href);
	const open = override ?? false;
	return (
		<div className="flex min-w-0 max-w-full flex-col">
			<button
				type="button"
				onClick={() => setOverride(!open)}
				disabled={!hasBody}
				aria-expanded={hasBody ? open : undefined}
				aria-label={descriptor?.ariaLabel ?? label}
				className={cn(ACTIVITY_SUMMARY_BUTTON_CLASS, "activity-row-toggle", !hasBody && "cursor-default")}
			>
				<Bot aria-hidden="true" className="size-3 shrink-0 text-muted-foreground/70" />
				<span className="activity-row-label shrink-0 text-xs font-normal text-muted-foreground">{label}</span>
				{descriptor?.state === "failed" ? (
					<span className="text-xs text-destructive">{t("chat.action.status.failed")}</span>
				) : null}
				{descriptor?.state === "cancelled" ? (
					<span className="text-xs text-muted-foreground/70">{t("chat.action.status.cancelled")}</span>
				) : null}
				{descriptor?.state === "running" ? (
					<Spinner icon={Loader2} className="size-3 text-muted-foreground/60" />
				) : null}
				{hasBody ? (
					<ChevronRight
						aria-hidden="true"
						className={cn(
							"size-3 shrink-0 text-muted-foreground/40 transition-transform group-hover/run:text-muted-foreground",
							open && "rotate-90",
						)}
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
						className="overflow-hidden"
					>
						<div className="flex flex-col gap-1 px-2 py-1 text-xs text-muted-foreground">
							{lines.map((line, index) => <span key={index}>{line}</span>)}
							{href && descriptor?.linkLabelKey ? (
								<SessionLabelLink href={href}>{t(descriptor.linkLabelKey)}</SessionLabelLink>
							) : null}
						</div>
					</motion.div>
				) : null}
			</AnimatePresence>
		</div>
	);
}

/**
 * A collapsed activity row: icon, label, target, state. Expands to its payload.
 *
 * A `running` activity with no completion is a real terminal state here, not a
 * spinner that hangs forever — a provider can start a command and supersede it.
 */
function GenericActivityRow({ activity }: { activity: ConversationActivity }) {
	// null means "nobody has decided", which is what lets a running command open
	// itself and then close again once it settles. Once the user clicks, their
	// choice sticks: auto-collapsing a log someone is reading is worse than leaving
	// a finished row open.
	const [override, setOverride] = useState<boolean | null>(null);
	const reducedMotion = useReducedMotion();
	const Icon = activityIcon[activity.activityKind] ?? SquareTerminal;
	const detail = activity.detail;
	const files = fileChangeFiles(activity);
	const isFileChange = activity.activityKind === "file_change";
	// A single edit with no patch has nothing to expand into — the header already
	// named the file. Multi-file edits expand to a list; a lone patch expands to
	// the diff itself.
	const hasFileBody =
		files.length > 1 || (files.length === 1 && Boolean(fileChangePatch(files[0])));
	const hasBody = Boolean(
		detail?.command ||
		(!isFileChange && (detail?.output || detail?.reason || detail?.text || detail?.terminalInput)) || hasFileBody,
	);
	const { label, path } = splitSummary(activity);
	// Commands and file edits share the explore-style summary line: muted label,
	// no icon column, always-visible chevron. Everything else keeps the denser
	// bordered activity row.
	const compactSummary =
		activity.activityKind === "command" || activity.activityKind === "file_change";
	const singleEdit = activity.activityKind === "file_change" && files.length === 1 ? files[0] : undefined;

	// Live output is only live if it is on screen, so a command that is still
	// running and already printing opens itself.
	const streamingOutput = activity.status === "running" && Boolean(detail?.output);
	const open = override ?? streamingOutput;

	return (
		<div
			className={cn(
				"min-w-0 max-w-full",
				compactSummary ? "flex flex-col" : "group/activity border-t border-border first:border-t-0",
			)}
		>
			<button
				type="button"
				onClick={() => setOverride(!open)}
				disabled={!hasBody}
				aria-expanded={hasBody ? open : undefined}
				className={cn(
					compactSummary
						? ACTIVITY_SUMMARY_BUTTON_CLASS
						: "flex min-h-[35px] w-full min-w-0 select-none items-center gap-[9px] px-[11px] py-2 text-left text-xs",
					"activity-row-toggle",
					hasBody && !compactSummary && "hover:text-foreground",
					!hasBody && "cursor-default",
				)}
			>
				{compactSummary ? null : (
					<Icon
						aria-hidden="true"
						className={cn(
							"w-[15px] shrink-0 text-center",
							activity.status === "failed" ? "text-destructive" : "text-muted-foreground/70",
						)}
						size={14}
					/>
				)}
				{singleEdit ? (
					<span className="flex min-w-0 items-center gap-1 text-xs font-normal">
						<span className="shrink-0 text-muted-foreground">
							{fileChangeVerb(singleEdit.status ?? "modified")}
						</span>
						<FileLocationLabel path={singleEdit.path} oldPath={singleEdit.oldPath} />
						{singleEdit.additions > 0 ? (
							<span className="shrink-0 font-mono text-micro tabular-nums text-success">
								+{singleEdit.additions}
						</span>
						) : null}
						{singleEdit.deletions > 0 ? (
							<span className="shrink-0 font-mono text-micro tabular-nums text-destructive">
								&minus;{singleEdit.deletions}
							</span>
						) : null}
					</span>
				) : (
					<strong
						className={cn(
							compactSummary
								? "activity-row-label shrink-0 text-xs font-normal text-muted-foreground group-hover/activity:text-foreground"
								: "min-w-0 truncate font-medium",
							!compactSummary &&
								(activity.status === "failed" ? "text-destructive" : "text-foreground"),
						)}
						title={compactSummary ? undefined : label}
					>
						<ActivityTitle text={label} />
					</strong>
				)}
				{path && !singleEdit ? (
					<span
						className="min-w-0 flex-1 truncate font-mono text-caption text-muted-foreground group-hover/activity:text-foreground"
						title={path}
					>
						{path}
					</span>
				) : compactSummary ? null : (
					<span className="flex-1" />
				)}
				<ActivityState
					activity={activity}
					open={open}
					hasBody={hasBody}
					inlineFileStats={Boolean(singleEdit)}
					showDisclosure={!compactSummary}
				/>
				{compactSummary && hasBody ? (
					<ChevronRight
						aria-hidden="true"
						className={cn(
							"size-3 shrink-0 text-muted-foreground/40 transition-transform group-hover/run:text-muted-foreground",
							open && "rotate-90",
						)}
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
						{compactSummary &&
						activity.activityKind === "command" &&
						(detail?.command || detail?.output || detail?.terminalInput) ? (
							<CommandExploreBody activity={activity} />
						) : (
							<div className="flex flex-col gap-1.5 px-1 pb-1 pt-0.5">
						{/* One file: open straight onto its patch. Listing the same
						    basename again under "Edited name" is noise. */}
						{files.length === 1 && fileChangePatch(files[0]) ? (
							<Patch patch={fileChangePatch(files[0])!} truncated={files[0].patchTruncated} />
						) : null}
						{files.length > 1 ? <FileChangeList files={files} /> : null}
						{!isFileChange && detail?.command ? (
							// Said explicitly rather than implied by the label: "Ran command"
							// alone never tells the reader what ran, and the collapsed row
							// deliberately keeps only the category.
							<pre className="scrollbar-none overflow-x-auto border border-border bg-background px-2.5 py-1.5 font-mono text-caption leading-relaxed text-foreground">
								{detail.command}
							</pre>
						) : null}
						{!isFileChange && (detail?.reason || detail?.text) ? (
							<p className="whitespace-pre-wrap px-1 text-caption leading-relaxed text-muted-foreground">
								{detail.reason ?? detail.text}
							</p>
						) : null}
						{!isFileChange && detail?.terminalInput ? (
							<TerminalInput
								text={detail.terminalInput}
								truncated={detail.terminalInputTruncated}
							/>
						) : null}
						{!isFileChange && detail?.output ? <CommandOutput activity={activity} /> : null}
							</div>
						)}
					</motion.div>
				) : null}
			</AnimatePresence>
		</div>
	);
}

/**
 * Split a summary into a bold action and a muted target, which is how the row
 * scans: what happened, then what it happened to. A command becomes its binary
 * plus its arguments; anything without a natural split keeps its whole label.
 */
function splitSummary(activity: ConversationActivity): { label: string; path?: string } {
	if (activity.activityKind === "command") {
		const rawCommand = activity.detail?.command ?? activity.summary;
		const category = commandCategory(rawCommand);
		if (category === "read" || category === "search") {
			const count = exploredFileCount(rawCommand);
			if (category === "search") return { label: "Search" };
			return { label: count && count > 1 ? "Read files" : "Read file" };
		}
		return { label: category === "vcs" ? "Checked repository" : "Ran command" };
	}
	const files = fileChangeFiles(activity);
	if (activity.activityKind === "file_change" && files.length === 1) {
		// Basename sits in the chip next to the status verb; the full path is the hover.
		return {
			label: `${fileChangeVerb(files[0]!.status ?? "modified")} ${fileBasename(files[0]!.path)}`,
		};
	}
	return { label: activity.summary };
}

function ActivityState({
	activity,
	open,
	hasBody,
	inlineFileStats = false,
	showDisclosure = true,
}: {
	activity: ConversationActivity;
	open: boolean;
	hasBody: boolean;
	inlineFileStats?: boolean;
	showDisclosure?: boolean;
}) {
	const { status, detail } = activity;
	const files = fileChangeFiles(activity);

	if (status === "running") {
		return (
			<Spinner icon={Loader2}
				aria-label="running"
				
			 className="size-3 shrink-0 self-center text-muted-foreground/60" />
		);
	}
	if (files.length && !inlineFileStats) {
		const additions = files.reduce((sum, file) => sum + file.additions, 0);
		const deletions = files.reduce((sum, file) => sum + file.deletions, 0);
		return (
			<span className="shrink-0 font-mono text-micro tabular-nums text-muted-foreground/70">
				<span className="text-success">+{additions}</span>{" "}
				<span className="text-destructive">&minus;{deletions}</span>
			</span>
		);
	}
	if (status === "failed") {
		return (
			<span
				className={cn(
					"shrink-0 font-mono text-micro tabular-nums",
					isNonzeroCommandExit(activity) ? "text-muted-foreground/70" : "text-destructive",
				)}
			>
				{detail?.exitCode !== undefined ? `exit ${detail.exitCode}` : "failed"}
			</span>
		);
	}
	if (status === "recovered") {
		return (
			<span className="shrink-0 font-mono text-micro text-muted-foreground/70">
				outcome unknown
			</span>
		);
	}
	if (status === "cancelled") {
		return (
			<span className="shrink-0 font-mono text-micro text-muted-foreground/70">
				stopped
			</span>
		);
	}
	// Everything else settled fine, which is the boring majority. A chevron on
	// hover is the whole affordance; a duration or timestamp on every row builds a
	// column of numbers nobody reads.
	if (hasBody && showDisclosure) {
		return (
			<ChevronRight
				aria-hidden="true"
				className={cn(
					"size-3 shrink-0 self-center text-muted-foreground/50 transition-all",
					open ? "rotate-90 opacity-100" : "opacity-0 group-hover/activity:opacity-100",
				)}
			/>
		);
	}
	return null;
}
