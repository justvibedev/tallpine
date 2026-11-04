/**
 * Tallpine's automation messages, each drawn as what it is instead of one generic
 * "automation" card.
 *
 * - What the user did through Tallpine (file feedback, a diff selection, a git button,
 *   browser annotations) is the user's own message: their words under a chip
 *   naming what they pointed at.
 * - What GitHub reported on the session's PR (failing CI, a review comment, a
 *   changes-requested review, merge conflicts) is a row in the PR's glyph,
 *   tinted by what it means, with the detail beneath.
 * - Tallpine's own notices are one quiet line.
 *
 * Each is read back from the fixed text Tallpine writes for it, in that text's order,
 * so what a reviewer or a CI log wrote inside it cannot pose as Tallpine's structure.
 * Text that matches none of them, or a message an agent sent, keeps the card.
 */
// ponytail: reads Tallpine's own message templates, so changing one means changing its reader here, and a direct API send without a sender can still pass as the user's action. Persisting the daemon's AuthoredByUser and the nudge kind on the message would end both.

import {
	ArrowLeftRight,
	ArrowUpFromLine,
	ClipboardCheck,
	CircleX,
	FileText,
	GitCommitHorizontal,
	GitPullRequest,
	GitPullRequestDraft,
	MousePointer2,
	RefreshCw,
	ChevronRight,
	type LucideIcon,
} from "lucide-react";
import { useState, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import { readAutomation, type Automation, type FailedCheck, type FileFeedback, type GitAction, type AutomationNotice, type PullRequestRef } from "@tallpine/shared/chat/automation/read";
import { forgeURL } from "@tallpine/shared/chat/automation/pr-nudges";
import type { ParsedBrowserAnnotationMessage } from "@tallpine/shared/chat/automation/browser-feedback";
import type { MessageKey } from "../../i18n/messages";
import { cn } from "../../lib/utils";
import type { ConversationMessage } from "../../types/conversation";
import { ChatMarkdown, FileReference, SessionLabelLink } from "./ChatMarkdown";
import { formatTime, humanBubbleClass, MessageTimestamp, QueuedNote } from "./ChatTimelineItems";
import { cachedBy, Folded } from "./WorkerUpdates";

export type { Automation, AutomationNotice, FailedCheck, FileFeedback, GitAction, PullRequestRef, ReviewComment } from "@tallpine/shared/chat/automation/read";
export { readAutomation } from "@tallpine/shared/chat/automation/read";

// A message an agent sent is the agent's, whatever its text says.
const automationOf = cachedBy((item) =>
	item.kind === "message" && !item.senderSessionId && !/^\s*\[from /.test(item.text) ? readAutomation(item.text) : null,
);

/* -------------------------------------------------------------------------- */
/* drawing                                                                     */
/* -------------------------------------------------------------------------- */

type UserAction = Extract<Automation, { kind: "fileFeedback" | "diffSelection" | "gitAction" | "browserFeedback" }>;
type PullRequestAutomation = Extract<Automation, { pr: PullRequestRef }>;

/** An automation message drawn as what it is, or `fallback` when Tallpine cannot tell. */
export function AutomationMessage({ message, queued, fallback }: { message: ConversationMessage; queued?: boolean; fallback: ReactNode }) {
	const automation = automationOf(message);
	if (!automation) return <>{fallback}</>;
	if (automation.kind === "notice") return <NoticeLine notice={automation.notice} />;
	if ("pr" in automation) return <PullRequestEvent automation={automation} createdAt={message.createdAt} />;
	return (
		<YourAction queued={queued} createdAt={message.createdAt}>
			<UserActionBody action={automation} />
		</YourAction>
	);
}

/** Something the user did through Tallpine, drawn as the user's own message: queued until sent, with its time on hover. */
function YourAction({ queued, createdAt, children }: { queued?: boolean; createdAt: string; children: ReactNode }) {
	return (
		<div className="group/message flex flex-col items-end gap-1" data-testid="your-action">
			{/* Themes draw sent and queued differently; light theme tells them apart in CSS. */}
			<div data-queued={queued ? "" : undefined} className={cn(humanBubbleClass(queued), "min-w-0")}>
				{children}
			</div>
			<div className="mt-1 flex h-7 items-center opacity-0 transition-opacity duration-150 ease-out group-hover/message:opacity-100 motion-reduce:transition-none">
				<MessageTimestamp createdAt={createdAt} />
			</div>
			{queued ? <QueuedNote /> : null}
		</div>
	);
}

function UserActionBody({ action }: { action: UserAction }) {
	switch (action.kind) {
		case "fileFeedback":
			return <FileFeedbackBody comments={action.comments} />;
		case "diffSelection":
			return (
				<>
					<FileChip path={action.path} line={action.start} end={action.end} />
					{action.instruction ? <p className="mt-1.5 whitespace-pre-wrap">{action.instruction}</p> : null}
				</>
			);
		case "gitAction":
			return <GitActionBody action={action.action} base={action.base} />;
		case "browserFeedback":
			return <BrowserFeedbackBody annotations={action.annotations} />;
	}
}

const chip = "inline-flex min-w-0 max-w-full items-center gap-1.5 rounded-md bg-foreground/[0.06] px-1.5 py-0.5 text-xs text-muted-foreground transition-colors enabled:hover:text-foreground";

function FileChip({ path, line, end }: { path: string; line?: number; end?: number }) {
	const name = path.slice(path.lastIndexOf("/") + 1);
	const at = line ? (end && end !== line ? `:${line}–${end}` : `:${line}`) : "";
	return (
		<FileReference path={path} line={line} className={chip}>
			<FileText aria-hidden="true" className="size-3 shrink-0" />
			<span className="truncate font-mono">{name}{at}</span>
		</FileReference>
	);
}

function FileFeedbackBody({ comments }: { comments: readonly FileFeedback[] }) {
	const { t } = useTranslation();
	return (
		<div className="flex min-w-0 flex-col gap-3">
			{comments.map((comment, index) => (
				<div key={index} className="min-w-0">
					<FileChip path={comment.path} line={comment.line} />
					{comment.code || comment.quote ? (
						<p className="mt-1 truncate font-mono text-xs text-muted-foreground" title={comment.code ?? comment.quote}>
							{comment.code ?? comment.quote}
						</p>
					) : comment.wholeFile ? (
						<p className="mt-1 text-xs text-muted-foreground">{t("chat.automation.entireFile")}</p>
					) : null}
					{comment.feedback ? <p className="mt-1.5 whitespace-pre-wrap">{comment.feedback}</p> : null}
				</div>
			))}
		</div>
	);
}

const GIT_ACTIONS: Record<GitAction, { icon: LucideIcon; label: MessageKey }> = {
	createPR: { icon: GitPullRequest, label: "inspector.git.createPR" },
	createDraftPR: { icon: GitPullRequestDraft, label: "inspector.git.createDraftPR" },
	commit: { icon: GitCommitHorizontal, label: "inspector.git.commit" },
	commitPush: { icon: GitCommitHorizontal, label: "inspector.git.commitAndPush" },
	push: { icon: ArrowUpFromLine, label: "inspector.git.push" },
};

function GitActionBody({ action, base }: { action: GitAction; base?: string }) {
	const { t } = useTranslation();
	const { icon: Icon, label } = GIT_ACTIONS[action];
	return (
		<span className="inline-flex min-w-0 items-center gap-2">
			<Icon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
			<span>{t(label)}</span>
			{base ? (
				<>
					<ChevronRight aria-hidden="true" className="size-3 shrink-0 text-muted-foreground" />
					<span className="truncate font-mono text-xs text-muted-foreground">{base}</span>
				</>
			) : null}
		</span>
	);
}

function BrowserFeedbackBody({ annotations }: { annotations: ParsedBrowserAnnotationMessage }) {
	const { t } = useTranslation();
	return (
		<>
			<span className={chip} title={annotations.pageUrl || annotations.pageTitle}>
				<MousePointer2 aria-hidden="true" className="size-3 shrink-0" />
				<span className="truncate">{annotations.pageTitle}</span>
			</span>
			<ol className="mt-1.5 flex flex-col gap-1">
				{annotations.items.map((item) => (
					<li key={item.number} className="flex min-w-0 items-baseline gap-2">
						<span className="shrink-0 text-xs tabular-nums text-muted-foreground">{item.number}</span>
						<span className="min-w-0">
							<span>{item.comment || t("chat.automation.visualChanges", { count: item.changes.length })}</span>
							{item.target ? <span className="ml-1.5 text-xs text-muted-foreground">{item.target}</span> : null}
							{item.text ? <span className="ml-1.5 text-xs text-muted-foreground">{item.text}</span> : null}
						</span>
					</li>
				))}
			</ol>
			{annotations.screenshotCount > 0 ? (
				<p className="mt-1.5 text-xs text-muted-foreground">{t("chat.automation.screenshots", { count: annotations.screenshotCount })}</p>
			) : null}
		</>
	);
}

const NOTICES: Record<AutomationNotice, { icon: LucideIcon; label: MessageKey }> = {
	orchestratorReplaced: { icon: RefreshCw, label: "chat.automation.orchestratorReplaced" },
	contextCarried: { icon: ArrowLeftRight, label: "chat.automation.contextCarried" },
	reviewTask: { icon: ClipboardCheck, label: "chat.automation.reviewTask" },
};

function NoticeLine({ notice }: { notice: AutomationNotice }) {
	const { t } = useTranslation();
	const { icon: Icon, label } = NOTICES[notice];
	return (
		<p className="flex min-h-7 min-w-0 items-center gap-2 text-xs text-muted-foreground" data-testid="automation-notice">
			<Icon aria-hidden="true" className="size-3.5 shrink-0" />
			<span className="min-w-0">{t(label)}</span>
		</p>
	);
}

// The forges Tallpine reads PRs from. A URL in the message's text is a link only to
// these, so a message that merely looks like Tallpine's cannot send the user elsewhere.
/** `children` linked to `url` when it is on a known forge; plain text otherwise. */
function ForgeLink({ url, className, children }: { url?: string; className?: string; children?: ReactNode }) {
	const href = forgeURL(url);
	if (!href) return <span className={className}>{children}</span>;
	return (
		<SessionLabelLink href={href} className={cn(className, "decoration-foreground/40 underline-offset-2 hover:underline")}>
			{children}
		</SessionLabelLink>
	);
}

/** What GitHub reported on the session's PR, in the PR glyph tinted by what it means. */
function PullRequestEvent({ automation, createdAt }: { automation: PullRequestAutomation; createdAt: string }) {
	const { t, i18n } = useTranslation();
	const { pr } = automation;
	const components = {
		strong: <span className="font-medium text-foreground" />,
		pr: <ForgeLink url={pr.url} className="font-medium text-foreground" />,
	};
	// A narrow key keeps Trans's typing tractable; the full MessageKey union is too complex for it (TS2590).
	const lead = (key: `chat.automation.${"ciFailing" | "reviewComment" | "changesRequested" | "mergeConflict"}`, author?: string) => (
		<Trans t={t} i18nKey={key} values={{ number: pr.number, author }} components={components} />
	);
	let tone = "text-status-in-review";
	let heading: ReactNode;
	let detail: ReactNode = null;
	switch (automation.kind) {
		case "ciFailing":
			tone = "text-status-exited";
			heading = lead("chat.automation.ciFailing");
			detail = (
				<ul className="flex flex-col gap-1">
					{automation.checks.map((check, index) => <FailedCheckLine key={index} check={check} />)}
				</ul>
			);
			break;
		case "reviewComments": {
			const authors = [...new Set(automation.comments.map((comment) => `@${comment.author}`))];
			heading = lead("chat.automation.reviewComment", new Intl.ListFormat(i18n.language, { type: "conjunction" }).format(authors));
			detail = automation.comments.map((comment, index) => (
				<div key={index} className="min-w-0">
					{comment.path || authors.length > 1 ? (
						<div className="mb-1 flex min-w-0 items-center gap-2">
							{/* Several reviewers: each comment says whose it is. */}
							{authors.length > 1 ? <span className="shrink-0 text-xs font-medium text-foreground">{`@${comment.author}`}</span> : null}
							{comment.path ? <FileChip path={comment.path} line={comment.line} /> : null}
						</div>
					) : null}
					<Folded watch={comment.body}>
						<ChatMarkdown text={comment.body} safeOrigin />
					</Folded>
				</div>
			));
			break;
		}
		case "changesRequested":
			heading = lead("chat.automation.changesRequested", automation.author ? `@${automation.author}` : t("chat.automation.tallpineReviewer"));
			detail = automation.body ? (
				<Folded watch={automation.body}>
					<ChatMarkdown text={automation.body} safeOrigin />
				</Folded>
			) : null;
			break;
		case "mergeConflict":
			// The PR cannot merge until the agent rebases: a failure, like failing CI.
			tone = "text-status-exited";
			heading = lead("chat.automation.mergeConflict");
			break;
	}
	return (
		<div className="relative min-w-0 pl-6" data-testid="pull-request-event">
			<GitPullRequest aria-hidden="true" className={cn("absolute left-0 top-[6px] size-4", tone)} />
			<div className="flex min-h-7 min-w-0 items-center gap-2 text-xs text-muted-foreground">
				<span className="min-w-0 truncate">
					{heading}
					{pr.title ? <span className="ml-1.5">{pr.title}</span> : null}
				</span>
				<span className="ml-auto shrink-0 tabular-nums">{formatTime(createdAt)}</span>
			</div>
			{detail ? <div className="flex min-w-0 flex-col gap-3 pb-1">{detail}</div> : null}
		</div>
	);
}

function FailedCheckLine({ check }: { check: FailedCheck }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	return (
		<li className="min-w-0 text-xs">
			<div className="flex min-w-0 items-center gap-2">
				<CircleX aria-hidden="true" className="size-3 shrink-0 text-status-exited" />
				<ForgeLink url={check.url} className="min-w-0 truncate font-medium text-foreground">
					{check.name}
				</ForgeLink>
				{check.log ? (
					<button
						type="button"
						aria-expanded={open}
						onClick={() => setOpen((current) => !current)}
						className="ml-1 shrink-0 rounded font-medium text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
					>
						{t(open ? "chat.automation.hideLog" : "chat.automation.showLog")}
					</button>
				) : null}
			</div>
			{open && check.log ? (
				<pre className="mt-1.5 max-h-64 overflow-auto rounded-md bg-surface p-2 font-mono text-caption leading-relaxed text-muted-foreground">{check.log}</pre>
			) : null}
		</li>
	);
}
