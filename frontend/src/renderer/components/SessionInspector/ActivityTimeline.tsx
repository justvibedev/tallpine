import { AppLink } from "../AppLink";
import { useQuery } from "@tanstack/react-query";
import { InspectorActivityTimelineView, type InspectorTimelineEvent } from "@tallpine/product-ui";
import { ArrowUpRight } from "lucide-react";
import {
	interfaceTransitionIsActive,
	useSessionInterfaceTransitionStatus,
} from "../../hooks/useSessionInterfaceTransition";
import { formatTimeCompact } from "../../lib/format-time";
import { type SessionPRSummary } from "../../hooks/useSessionScmSummary";
import { sessionWorkspaceHistoryQueryOptions } from "../../hooks/workspace-files/workspace-list-queries";
import { prBrowserUrl } from "../../lib/pr-display";
import type { WorkspaceSession } from "../../types/workspace";
import { openPRs } from "../../types/workspace";
import { getAgentActivityView, getSessionTimelinePillView } from "../../lib/session-presentation";
import { appI18n } from "../../i18n";

export type SortableTimelineEvent = InspectorTimelineEvent & { sortTime: number };

// ponytail: newest 5 commits only; a "show all" control when long sessions need it.
export const TIMELINE_COMMIT_LIMIT = 5;

export function ActivityTimeline({ hostId, prs, session }: { hostId?: string; prs: SessionPRSummary[]; session: WorkspaceSession }) {
	// Keyed on the daemon's commit count, not the shared history query that every
	// file edit invalidates, so only a new commit refetches.
	const commitCount = session.branchState?.commits ?? 0;
	const history = useQuery({
		queryKey: ["session-activity-commits", hostId ?? "", session.id, commitCount],
		queryFn: sessionWorkspaceHistoryQueryOptions(session.id, undefined, hostId).queryFn,
		staleTime: Infinity,
		placeholderData: (previous) => previous,
		enabled: session.kind !== "orchestrator",
	});
	const events: SortableTimelineEvent[] = [];
	const pushEvent = (event: InspectorTimelineEvent, timestamp?: string | null) => {
		events.push({ ...event, sortTime: timelineSortTime(timestamp) });
	};
	const createdAt = session.createdAt ?? session.updatedAt;

	pushEvent(
		{
			tone: "neutral",
			content: <>{appI18n.t("inspector.timeline.createdWorkspace")}</>,
			timestamp: formatTimeCompact(createdAt),
		},
		createdAt,
	);

	for (const commit of history.data?.commits.slice(0, TIMELINE_COMMIT_LIMIT) ?? []) {
		pushEvent(
			{
				tone: "neutral",
				content: (
					<>
						{appI18n.t("inspector.timeline.committed")} <span className="text-passive">{commit.subject}</span>
					</>
				),
				timestamp: formatTimeCompact(commit.timestamp),
			},
			commit.timestamp,
		);
	}

	for (const pr of prs.filter((pr) => pr.state === "draft")) {
		pushEvent(
			{
				tone: "neutral",
				content: <PRTimelineLink pr={pr} verb={appI18n.t("inspector.timeline.draft")} />,
				timestamp: prStateTime(pr),
			},
			pr.stateChangedAt,
		);
	}

	for (const pr of prs.filter((pr) => pr.state !== "draft")) {
		pushEvent(
			{
				tone: "neutral",
				content: <PRTimelineLink pr={pr} verb={appI18n.t("inspector.timeline.opened")} />,
				timestamp: prCreatedTime(pr),
			},
			pr.createdAt,
		);
	}

	for (const pr of prs.filter((pr) => pr.state === "merged")) {
		pushEvent(
			{
				tone: "good",
				content: <PRTimelineLink pr={pr} verb={appI18n.t("inspector.timeline.merged")} />,
				timestamp: prStateTime(pr),
			},
			pr.stateChangedAt,
		);
	}

	if (session.status === "merged") {
		const mergedAt = latestMergedTimestamp(prs);
		pushEvent(
			{
				tone: "good",
				content: <>{appI18n.t("inspector.timeline.done")}</>,
				timestamp: mergedAt ? formatTimeCompact(mergedAt) : null,
			},
			mergedAt,
		);
	}

	// An account move stops the old agent before the new one starts. That stop
	// is not the session ending, so the move stands in for "Exited" meanwhile.
	// The session's own host: a remote session's id means nothing to the local daemon.
	const { transition } = useSessionInterfaceTransitionStatus(session.id, hostId);
	const accountMoving = Boolean(transition?.targetAccountId) && interfaceTransitionIsActive(transition);
	const activityView = accountMoving
		? { ...getAgentActivityView({ state: "active", lastActivityAt: transition?.updatedAt ?? "" }), label: appI18n.t("settings.agentAccounts.switchingAccount") }
		: getAgentActivityView(session.activity);
	const activityAt = session.activity?.lastActivityAt ?? session.updatedAt ?? session.createdAt;
	pushEvent(
		{
			tone: "now",
			content: (
				<span className="inline-flex flex-wrap items-center gap-1.5">
					<span className="inline-flex align-middle">
						{accountMoving ? <TimelinePill {...activityView} /> : <InspectorActivityPill activity={session.activity} />}
					</span>
					{session.status === "no_signal" ? (
						<span className="inline-flex align-middle">
							<TimelinePill {...getSessionTimelinePillView("no_signal")} />
						</span>
					) : null}
					{scmTimelineStates(session).map((state) => (
						<span key={state} className="inline-flex align-middle">
							<InspectorScmPill state={state} />
						</span>
					))}
				</span>
			),
			timestamp: activityAt ? formatTimeCompact(activityAt) : null,
			markerTone: activityView.tone,
			markerBreathe: activityView.breathe,
		} satisfies InspectorTimelineEvent,
		activityAt,
	);

	return <InspectorActivityTimelineView events={[...events].sort((a, b) => b.sortTime - a.sortTime)} />;
}

export function PRTimelineLink({ pr, verb }: { pr: SessionPRSummary; verb: string }) {
	return (
		<AppLink
			aria-label={`${verb} PR #${pr.number}`}
			className="inline-flex min-w-0 items-center gap-1 rounded-xs text-foreground underline-offset-2 transition-colors hover:text-accent hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent/50"
			href={prBrowserUrl(pr)}
			rel="noopener noreferrer"
			target="_blank"
		>
			<span>{verb} </span>
			<b>PR #{pr.number}</b>
			<ArrowUpRight aria-hidden="true" className="size-icon-2xs shrink-0" strokeWidth={2} />
		</AppLink>
	);
}

export function prStateTime(pr: SessionPRSummary): string | null {
	return pr.stateChangedAt ? formatTimeCompact(pr.stateChangedAt) : null;
}

export function prCreatedTime(pr: SessionPRSummary): string | null {
	return pr.createdAt ? formatTimeCompact(pr.createdAt) : null;
}

export function latestMergedTimestamp(prs: SessionPRSummary[]): string | null {
	let latest: { timestamp: string; milliseconds: number } | undefined;
	for (const pr of prs) {
		if (pr.state !== "merged" || !pr.stateChangedAt) continue;
		const milliseconds = Date.parse(pr.stateChangedAt);
		if (!Number.isFinite(milliseconds)) continue;
		if (!latest || milliseconds > latest.milliseconds) {
			latest = { timestamp: pr.stateChangedAt, milliseconds };
		}
	}
	return latest?.timestamp ?? null;
}

export function timelineSortTime(timestamp: string | null | undefined): number {
	if (!timestamp) return Number.NEGATIVE_INFINITY;
	const milliseconds = Date.parse(timestamp);
	return Number.isFinite(milliseconds) ? milliseconds : Number.NEGATIVE_INFINITY;
}

export type ScmTimelineState = "ci_failed" | "changes_requested" | "commented" | "conflict";

export function conflictPill() {
	return { label: appI18n.t("inspector.conflict"), tone: "var(--color-danger)", breathe: false };
}

export function InspectorActivityPill({ activity }: { activity?: WorkspaceSession["activity"] }) {
	return <TimelinePill {...getAgentActivityView(activity)} />;
}

export function InspectorScmPill({ state }: { state: ScmTimelineState }) {
	if (state === "conflict") return <TimelinePill {...conflictPill()} />;
	return <TimelinePill {...getSessionTimelinePillView(state)} />;
}

export function TimelinePill({ label, tone }: { label: string; tone: string; breathe: boolean }) {
	return (
		<span className="inline-flex shrink-0 whitespace-nowrap text-xs font-semibold" style={{ color: tone }}>
			{label}
		</span>
	);
}

export function scmTimelineStates(session: WorkspaceSession): ScmTimelineState[] {
	const states: ScmTimelineState[] = [];
	const seen = new Set<ScmTimelineState>();
	const open = new Set(openPRs(session));
	const add = (state: ScmTimelineState) => {
		if (seen.has(state)) return;
		seen.add(state);
		states.push(state);
	};

	if (session.status === "ci_failed") add("ci_failed");
	if (session.status === "changes_requested") add("changes_requested");
	if (session.status === "commented") add("commented");
	for (const pr of session.prs) {
		if (open.has(pr) && pr.ci === "failing") add("ci_failed");
		if (pr.review === "changes_requested") add("changes_requested");
		// Read the raw fact directly rather than session.status: status collapses
		// to "working" while the agent is active, but unresolved comments from a
		// non-blocking review must stay visible regardless of agent activity.
		// Gated on the PR still being open so a stale comment on a merged/closed
		// PR cannot keep the pill around.
		if (open.has(pr) && pr.reviewComments) add("commented");
		if (pr.mergeability === "conflicting") add("conflict");
	}

	return states;
}
