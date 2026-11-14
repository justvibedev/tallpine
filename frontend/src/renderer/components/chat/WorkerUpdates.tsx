/**
 * The orchestrator chat's view of its workers.
 *
 * A worker's message or report is drawn as one quiet row — who, state, a headline,
 * time — that opens to the full text, instead of a full-width card. When the
 * orchestrator's answer asks the user nothing, it hangs under the rows as one line
 * that opens to the work behind it, so the chat reads as the user's conversation
 * rather than a log of worker traffic. A turn that spawned workers lists them
 * with where each one is now.
 */

import { cachedBy as sharedCachedBy, latestWorkerUpdateKeys, piggybackedUpdates, workerUpdatesOf } from "@tallpine/shared/chat/automation/worker-updates";
import { isTaskTitleUpdate, renamedTitle } from "@tallpine/shared/chat/automation/title-update";
import { parseViaPrefix } from "@tallpine/shared/chat/automation/sender-prefix";
import { splitArtifacts, splitHeadline, type WorkerUpdate } from "@tallpine/shared/chat/automation/worker-reports";
import { Bot, ChevronDown, ChevronRight, CornerDownRight, FileText, Tag, Undo2 } from "lucide-react";
import { createContext, useContext, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { needsInputView } from "./needsInputAudienceView";
import { CatalogTrans } from "../../i18n";
import { cn } from "../../lib/utils";
import { isSteer, type ConversationItem, type ConversationMessage } from "../../types/conversation";
import type { WorkspaceSession } from "../../types/workspace";
import { AgentAvatar } from "../AgentAvatar";
import { ArtifactPathCode, ChatMarkdown, SessionLabelLink, useChatLinkWorkspaces } from "./ChatMarkdown";
import { formatTime, humanVisibleText, QueuedNote } from "./ChatTimelineItems";
import { CopyButton } from "./CopyButton";

export type { WorkerUpdate };

// The prompt Tallpine used to send the orchestrator to name a new task, and the command it answered with.

/** Items never change in place, so each is parsed once however often the timeline renders. */
export const cachedBy: <T>(read: (item: ConversationItem) => T) => (item: ConversationItem) => T = sharedCachedBy;

const STATES: Record<string, { label: string; attention?: boolean }> = {
	needs_input: { label: "needs you", attention: true },
	stuck: { label: "stuck", attention: true },
	done: { label: "done" },
};

/** The outside tool a message or steer came through and what it said, when no Tallpine session sent it. */
// ponytail: reads the tool's text prefix, so anyone can type one; a sender name on the send API would replace it.
export const relayedVia = cachedBy((item): { name: string; body: string } | null => {
	// A message's piggybacked reports draw as their own rows, as under a human message.
	const text = item.kind === "message"
		? item.role === "user" && !item.senderSessionId ? humanVisibleText(item.text) : undefined
		: isSteer(item) && !item.detail?.senderSessionId?.trim() ? item.detail?.text ?? item.summary : undefined;
	if (text === undefined) return null;
	return parseViaPrefix(text);
});

export { isTaskTitleUpdate, latestWorkerUpdateKeys, piggybackedUpdates, renamedTitle, workerUpdatesOf };

/** The title the orchestrator gave a new task, read from its `tallpine session rename`. */
/** A turn a worker or Tallpine opened rather than the user: worker traffic, or a task-naming prompt. */
export function isWorkerLead(item: ConversationItem | undefined): boolean {
	return item !== undefined && (workerUpdatesOf(item).length > 0 || isTaskTitleUpdate(item));
}

/** True when a reply asks the user something. A `?` in code or a link, or one running into more text (`a?.b`), is not a question. */
// The orchestrator's "Needs you:" marker (prompt rule in session_manager/prompt.go) is the structured signal; punctuation is only the fallback.
const NEEDS_YOU_MARKER = /^\s*(?:\*\*|__)?\s*needs you:/i;

export const hasNeedsYouMarker = (text: string): boolean => NEEDS_YOU_MARKER.test(text);

export function asksUser(text: string): boolean {
	if (hasNeedsYouMarker(text)) return true;
	const prose = text.replace(/```[\s\S]*?(?:```|$)|`[^`\n]*`|\]\([^)]*\)|\bhttps?:\/\/\S+/g, " ");
	return /[?？](?=[\s"'”’)\]*_]|$)/.test(prose);
}

export function finalReply(items: readonly ConversationItem[]): ConversationMessage | undefined {
	for (let index = items.length - 1; index >= 0; index -= 1) {
		const item = items[index];
		if (item?.kind === "message" && item.role === "assistant" && item.text.trim() !== "") return item;
	}
	return undefined;
}

/** A worker-led turn whose final orchestrator reply carries the "Needs you:" marker. */
export function replyNeedsOwner(items: readonly ConversationItem[]): boolean {
	const reply = finalReply(items);
	return reply !== undefined && isWorkerLead(items[0]) && hasNeedsYouMarker(reply.text);
}

/**
 * The orchestrator's answer to a turn a worker opened, when it asks the user
 * nothing. That answer is drawn as one line under the worker's rows.
 */
export function quietWorkerReply(items: readonly ConversationItem[]): ConversationMessage | undefined {
	const lead = items[0];
	if (!isWorkerLead(lead)) return undefined;
	// Another project's orchestrator is a peer: the receiver's answer is its own message, not a footnote.
	if (lead && workerUpdatesOf(lead).some((update) => update.fromOrchestrator)) return undefined;
	const reply = finalReply(items);
	return reply && !asksUser(reply.text) ? reply : undefined;
}

export interface WorkerUpdateIndex {
	/** Keys of each worker's newest update; an older "needs you" has been overtaken. */
	latestKeys: ReadonlySet<string>;
	/** Each worker's newest update, by session. */
	latest: ReadonlyMap<string, WorkerUpdate>;
}

export const WorkerUpdateIndexContext = createContext<WorkerUpdateIndex | null>(null);

export function indexWorkerUpdates(items: readonly ConversationItem[]): WorkerUpdateIndex {
	const latest = new Map<string, WorkerUpdate>();
	for (const item of items) {
		for (const update of [...piggybackedUpdates(item), ...workerUpdatesOf(item)]) {
			latest.set(update.sessionId ?? update.key, update);
		}
	}
	return { latest, latestKeys: new Set([...latest.values()].map((update) => update.key)) };
}

/**
 * The index for a timeline, keeping the same object until some worker's newest
 * update changes, so rows in settled turns stay idle while a reply streams.
 */
export function useWorkerUpdateIndex(items: readonly ConversationItem[] | undefined): WorkerUpdateIndex | null {
	const previous = useRef<WorkerUpdateIndex | null>(null);
	return useMemo(() => {
		const next = items ? indexWorkerUpdates(items) : null;
		const before = previous.current;
		const unchanged = before && next && before.latestKeys.size === next.latestKeys.size
			&& [...next.latestKeys].every((key) => before.latestKeys.has(key));
		previous.current = unchanged ? before : next;
		return previous.current;
	}, [items]);
}

function isCurrent(index: WorkerUpdateIndex | null, update: WorkerUpdate): boolean {
	return !index || index.latestKeys.has(update.key);
}

export const sessionHref = (update: Pick<WorkerUpdate, "sessionId" | "projectId">) =>
	update.sessionId && update.projectId
		? `tallpine://sessions/${encodeURIComponent(update.projectId)}/${encodeURIComponent(update.sessionId)}`
		: undefined;

/** The worker as this chat's links see it: the local daemon's, a remote host's, or Cloud's. */
export function useWorkerSession(sessionId: string | undefined, projectId?: string): WorkspaceSession | undefined {
	const workspaces = useChatLinkWorkspaces();
	if (!sessionId) return undefined;
	const scope = projectId ? workspaces.filter((workspace) => workspace.id === projectId) : workspaces;
	for (const workspace of scope) {
		const session = workspace.sessions.find((candidate) => candidate.id === sessionId);
		if (session) return session;
	}
	return undefined;
}

function WorkerUpdateRow({ update }: { update: WorkerUpdate }) {
	const [open, setOpen] = useState(false);
	const session = useWorkerSession(update.sessionId, update.projectId);
	const index = useContext(WorkerUpdateIndexContext);
	const { text, artifacts } = splitArtifacts(update.body);
	const { headline, detail } = splitHeadline(text);
	const { t } = useTranslation();
	const baseState = update.state ? STATES[update.state] : undefined;
	const waitView = update.state === "needs_input" ? needsInputView(session, t) : undefined;
	const state = baseState && waitView ? { ...baseState, label: waitView.label, attention: waitView.attention } : baseState;
	const attention = state?.attention && isCurrent(index, update);
	// An older message names its sender only by session id; the session supplies its project and title.
	const href = sessionHref({ sessionId: update.sessionId, projectId: update.projectId ?? session?.workspaceId });
	const name = <span className="block truncate">{update.name === update.sessionId ? session?.title || update.name : update.name}</span>;
	return (
		<div className="min-w-0 text-xs" data-testid="worker-update">
			<div className="group -mx-1.5 flex h-7 w-[calc(100%+0.75rem)] min-w-0 items-center gap-2 rounded-md px-1.5 text-muted-foreground transition-colors hover:bg-interactive-hover">
				{session?.provider ? (
					<AgentAvatar provider={session.provider} decorative className="size-4 shrink-0" />
				) : (
					<Bot aria-hidden="true" className="size-3.5 shrink-0" />
				)}
				{/* The sender opens its session, as the "[from …]" label on a worker message does; the rest of the row expands. */}
				{href ? (
					<SessionLabelLink
						href={href}
						className="max-w-[40%] shrink-0 font-medium text-foreground decoration-foreground/40 underline-offset-2 hover:underline"
					>
						{name}
					</SessionLabelLink>
				) : (
					<span className="max-w-[40%] shrink-0 font-medium text-foreground">{name}</span>
				)}
				<button
					type="button"
					aria-expanded={open}
					onClick={() => setOpen((current) => !current)}
					className="flex h-full min-w-0 flex-1 items-center gap-2 rounded text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
				>
					{state ? (
						<span className={cn("shrink-0", attention && "font-medium text-status-needs-you")}>{state.label}</span>
					) : null}
					<span className="min-w-0 flex-1 truncate transition-colors group-hover:text-foreground">{headline}</span>
					<span className="shrink-0 tabular-nums">{formatTime(update.createdAt)}</span>
					<ChevronRight
						aria-hidden="true"
						className={cn("size-3 shrink-0 transition-transform motion-reduce:transition-none", open && "rotate-90")}
					/>
				</button>
			</div>
			{artifacts.map((path) => (
				<p key={path} className="ml-6 flex h-6 min-w-0 items-center gap-1.5 text-muted-foreground">
					<FileText aria-hidden="true" className="size-3.5 shrink-0" />
					{/* A listed artifact links to its preview; one the session no longer lists stays a name. */}
					<span className="min-w-0 truncate font-mono" title={path}>
						<ArtifactPathCode text={artifactReference(path, update.sessionId)}>{path.split("/").at(-1)}</ArtifactPathCode>
					</span>
				</p>
			))}
			{open ? (
				<div className="mb-2 ml-6 mt-1 min-w-0">
					<ChatMarkdown text={detail} muted safeOrigin className="whitespace-pre-wrap text-xs leading-relaxed" />
				</div>
			) : null}
		</div>
	);
}

/** A report may name an artifact relative to its session's artifact folder; scope that to the session. */
function artifactReference(path: string, sessionId: string | undefined): string {
	const absolute = path.startsWith("/") || /^[A-Za-z]:[\\/]/.test(path);
	return absolute || !sessionId ? path : `${sessionId}/${path}`;
}

export function WorkerUpdateRows({ updates }: { updates: readonly WorkerUpdate[] }) {
	return (
		<div className="flex min-w-0 flex-col">
			{updates.map((update) => (
				<WorkerUpdateRow key={update.key} update={update} />
			))}
		</div>
	);
}

/**
 * In a worker's chat, a message another agent sent it (its orchestrator, or a
 * sibling worker), drawn the way the orchestrator chat draws a worker: avatar,
 * linked name and time, with what it sent open beneath at the row's indent.
 * What an outside tool relayed through Tallpine's API (`[via Grok Bot] …`) draws the
 * same way under the tool's name, in any chat. Any other message draws as `children`.
 */
export function FromAgent({ item, queued, children }: { item: ConversationItem; queued?: boolean; children: ReactNode }) {
	const { t } = useTranslation();
	const updates = workerUpdatesOf(item);
	const update = updates.length === 1 && !updates[0]!.state ? updates[0] : undefined;
	const session = useWorkerSession(update?.sessionId, update?.projectId);
	const via = relayedVia(item);
	if (via) {
		return (
			<SenderRow
				avatar={<Bot aria-hidden="true" className="absolute left-0 top-[3px] size-4 text-muted-foreground" />}
				createdAt={item.createdAt}
				body={via.body}
				queued={queued}
			>
				<span className={SENDER_NAME}>{via.name}</span>
			</SenderRow>
		);
	}
	if (!update || !session) return <>{children}</>;
	const name = session.kind === "orchestrator" ? t("shell.orchestrator") : session.title || update.name;
	const href = sessionHref({ sessionId: update.sessionId, projectId: update.projectId ?? session.workspaceId });
	return (
		<SenderRow avatar={<AgentAvatar provider={session.provider} decorative className="absolute left-0 top-[3px] size-4" />} createdAt={update.createdAt} body={update.body}>
			{href ? (
				<SessionLabelLink href={href} className={cn(SENDER_NAME, "decoration-foreground/40 underline-offset-2 hover:underline")}>
					{name}
				</SessionLabelLink>
			) : null}
		</SenderRow>
	);
}

const SENDER_NAME = "float-left mr-2 text-[length:var(--chat-readable-font-size)] font-medium leading-[1.58] text-foreground";

function SenderRow({ avatar, createdAt, body, queued, children }: { avatar: ReactNode; createdAt: string; body: string; queued?: boolean; children: ReactNode }) {
	return (
		<div className="relative min-w-0 pl-6" data-testid="agent-message">
			{avatar}
			<Folded watch={body}>
				{/* The name and time float into the message's first line; the rest wraps under the name. */}
				<span className="float-right ml-3 text-xs leading-[calc(var(--chat-readable-font-size)*1.58)] tabular-nums text-muted-foreground">
					{formatTime(createdAt)}
				</span>
				{children}
				<ChatMarkdown text={body} safeOrigin />
			</Folded>
			{queued ? <div className="mt-1"><QueuedNote /></div> : null}
		</div>
	);
}

/**
 * Content folded after five lines, with "Show more" whenever the fold hides
 * something. Re-measured as the chat resizes, so hidden text always comes with a
 * way to open it.
 */
export function Folded({ watch, children }: { watch: unknown; children: ReactNode }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const [folds, setFolds] = useState(false);
	const body = useRef<HTMLDivElement>(null);
	useLayoutEffect(() => {
		const element = body.current;
		if (!element || open) return;
		const measure = () => setFolds(element.scrollHeight > element.clientHeight + 1);
		measure();
		const observer = new ResizeObserver(measure);
		observer.observe(element);
		return () => observer.disconnect();
	}, [open, watch]);
	return (
		<>
			<div ref={body} className={cn("min-w-0", !open && "line-clamp-5")}>
				<div>{children}</div>
			</div>
			{folds || open ? (
				<button
					type="button"
					aria-expanded={open}
					onClick={() => setOpen((current) => !current)}
					className="mt-1 inline-flex items-center gap-1 rounded text-xs font-medium text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
				>
					{t(open ? "shell.showLess" : "shell.showMore")}
					<ChevronDown aria-hidden="true" className={cn("size-3 transition-transform motion-reduce:transition-none", open && "rotate-180")} />
				</button>
			) : null}
		</>
	);
}

/** A task-naming prompt and its rename, drawn as the one fact they produced. */
export function TaskNamedLine({ title }: { title: string }) {
	const { t } = useTranslation();
	return (
		<p className="flex h-7 min-w-0 items-center gap-2 text-xs text-muted-foreground">
			<Tag aria-hidden="true" className="size-3.5 shrink-0" />
			<span className="min-w-0 truncate">
				<CatalogTrans t={t} i18nKey="chat.taskNamed" values={{ title }} components={{ task: <span className="font-medium text-foreground" /> }} />
			</span>
		</p>
	);
}

/**
 * The orchestrator's answer under the rows that prompted it. It keeps what the
 * reply would have — copy, rollback, the work behind it — but on one quiet line.
 */
export function OrchestratorReply({
	message,
	work,
	workOpen,
	onWorkOpenChange,
	onRollback,
	rollbackDisabled,
}: {
	message: ConversationMessage;
	work?: ReactNode;
	workOpen: boolean;
	onWorkOpenChange: (open: boolean) => void;
	onRollback?: () => void;
	rollbackDisabled?: boolean;
}) {
	const { t } = useTranslation();
	const control = "flex size-6 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground";
	return (
		<div
			className="group/message -mt-1.5 ml-6 min-w-0"
			data-testid="orchestrator-reply"
			data-chat-message-id={message.id}
			data-chat-message-revision={message.revision}
			data-chat-message-role={message.role}
		>
			<div className="flex min-w-0 items-start gap-2">
				<CornerDownRight aria-hidden="true" className="mt-[3px] size-3 shrink-0 text-muted-foreground" />
				{/* The timeline sizes chat markdown through these variables; a reply line sits at the rows' size. */}
				<div
					data-chat-message-body=""
					className="min-w-0 flex-1 [--chat-readable-code-font-size:var(--font-size-caption)] [--chat-readable-font-size:var(--font-size-sm)]"
				>
					<ChatMarkdown text={message.text} />
				</div>
				<div className="-my-1 flex shrink-0 items-center">
					{/* Revealed on hover or keyboard focus, so a list of quiet replies does not carry a toolbar each. */}
					<div className="flex items-center opacity-0 transition-opacity group-hover/message:opacity-100 group-focus-within/message:opacity-100 motion-reduce:transition-none">
						<CopyButton text={message.text} label="Copy message as markdown" compact className={cn(control, "px-0 py-0")} />
						{onRollback ? (
							<button
								type="button"
								onClick={onRollback}
								disabled={rollbackDisabled}
								aria-label={t("chat.rollBackToHere")}
								title={t("chat.rollBackToHere")}
								className={cn(control, "disabled:pointer-events-none disabled:opacity-40")}
							>
								<Undo2 aria-hidden="true" className="size-3" />
							</button>
						) : null}
					</div>
					{work ? (
						<button
							type="button"
							aria-expanded={workOpen}
							aria-label={t("chat.showReplyWork")}
							onClick={() => onWorkOpenChange(!workOpen)}
							className={control}
						>
							<ChevronRight aria-hidden="true" className={cn("size-3 transition-transform motion-reduce:transition-none", workOpen && "rotate-90")} />
						</button>
					) : null}
				</div>
			</div>
			{workOpen && work ? <div className="mt-2 flex min-w-0 flex-col gap-2.5">{work}</div> : null}
		</div>
	);
}
