import { sessionWantsAttention } from "../lib/session-presentation";
import {
	AGENT_OPTIONS,
	toKanbanColumn,
	toSessionActivity,
	toSessionStatus,
	type AgentId,
	type KanbanColumn,
	type SessionActivity,
	type SessionActivityState,
	type SessionStatus,
} from "@tallpine/product-ui";

import type { ReviewerHarnessId } from "../lib/reviewer-harnesses";
import type { ArtifactKind, SessionArtifact } from "./session-artifact";
import type { SessionBackgroundTask, SessionBranchState, SessionStaleBinary } from "./session-facts";

export { toKanbanColumn, toSessionActivity, toSessionStatus };

export type { ArtifactKind, SessionArtifact };
export type { KanbanColumn, SessionActivity, SessionActivityState, SessionStatus };

export type AgentProvider = AgentId | "fake";

/** A file changed in a worker workspace (drives the review rail). */
export type ChangedFile = {
	path: string;
	additions: number;
	deletions: number;
	staged?: boolean;
};

export type SessionKind = "worker" | "orchestrator";

/** Lifecycle state of a single pull request, mirrors the daemon's enum. */
export type PRState = "open" | "draft" | "merged" | "closed";

/**
 * One attributed pull request, mirroring the daemon's SessionPRFacts wire shape.
 * A session can own many (e.g. a stack), so {@link WorkspaceSession.prs} is a
 * list. The wire carries no source/target branch or parent pointer, so the UI
 * renders a flat list of PRs, not a stack tree.
 */
export type PullRequestFacts = {
	url: string;
	number: number;
	state: PRState;
	ci: string;
	review: string;
	mergeability: string;
	failingChecks?: Array<{
		name: string;
		status: "failed" | "cancelled";
		conclusion: string;
		url?: string;
	}>;
	reviewComments: boolean;
	updatedAt: string;
};

/** What kind of durable output a session has produced, when known. */
export type SessionOutputType = "none" | "pr" | "artifact" | "pr_artifact";

/** The daemon-committed controller currently responsible for the session. */
export type SessionMode = "chat" | "tui";

/** One stage of an asynchronous Chat start. */
export type SessionProvisionStep = {
	id: "fetch" | "worktree" | "setup" | "agent";
	status: "pending" | "running" | "done";
	startedAt?: string;
	endedAt?: string;
};

export type WorkspaceSession = {
	workspaceCleanup?: "pending" | "removed" | "preserved_dirty" | "failed" | "not_applicable";
	id: string;
	/** Installation ID of the daemon that owns this session; absent for local. */
	hostId?: string;
	terminalHandleId?: string;
	/** Opaque controller generation; changes even when a restarted PTY reuses its handle. */
	terminalGeneration?: string;
	workspaceId: string;
	workspaceName: string;
	title: string;
	/** Raw issue/task identifier from the daemon. Intake ids are provider-prefixed. */
	issueId?: string;
	provider: AgentProvider;
	/** Reviewer selected for this session; absent means use the project default. */
	reviewerHarness?: ReviewerHarnessId;
	/** Per-session reviewer override, including hidden fields preserved across saves. */
	reviewerConfig?: {
		model?: string;
		mode?: string;
		permissions?: string;
	};
	/** Whether the daemon may automatically review this session after it becomes idle. */
	autoReviewEnabled?: boolean;
	kind?: SessionKind;
	/** Backlog ref of the lane an orchestrator owns (e.g. "B12"); unset for the default orchestrator. */
	lane?: string;
	/**
	 * Which controller is currently committed for this session. The session
	 * surface renders from THIS value, never from the current creation default.
	 * Only the daemon's durable interface-transition coordinator may change it.
	 */
	mode?: SessionMode;
	branch?: string;
	status: SessionStatus;
	/** Who a needs-input state waits on; derived by the daemon, absent when not waiting. */
	needsInputAudience?: "orchestrator" | "human";
	/** Stack-aware PR context derived by the daemon independently of runtime activity. */
	scmStatus?: SessionStatus;
	/**
	 * Board lane derived by the daemon from durable delivery facts (PR
	 * lifecycle, review runs, review ownership). `validating` and
	 * `needs_review` are the same review-feedback loop seen from either side:
	 * Tallpine turning it, or a person taking the next turn. The board groups by this
	 * and never re-derives a lane from {@link status}. For a daemon too old to
	 * send one, {@link toKanbanColumn} keeps the placement the status already
	 * implied rather than inventing a new one.
	 */
	kanbanColumn?: KanbanColumn;
	/**
	 * Phrase the daemon derived for what is happening inside
	 * {@link kanbanColumn} — "Reviewing", "Fixing CI failures", "Needs human
	 * review". It arrives renderable, so the UI prints it rather than mapping it.
	 * Absent from a daemon too old to send one, which keeps the label
	 * {@link status} already produced.
	 */
	displayStatus?: string;
	statusReadiness?: "checking" | "ready" | "unavailable";
	/**
	 * How far this session's start-up got. A Chat spawn answers as soon as the
	 * session is addressable, so a session can be open and typeable while its
	 * worktree and agent are still being created ("provisioning"), and a start
	 * that fails leaves the session in place ("failed") with
	 * {@link provisionError} explaining why. Absent means ready — which is also
	 * what every session created before asynchronous spawn reads as.
	 */
	provisionState?: "provisioning" | "ready" | "failed";
	/** Why a failed start stopped, in the daemon's words. */
	provisionError?: string;
	/**
	 * The checklist an asynchronous start works through, in order. A step still
	 * "running" on a failed session is the step that failed.
	 */
	provisionSteps?: SessionProvisionStep[];
	/** The daemon's observed branch facts: commits on top of the base and whether they reached the remote. */
	branchState?: SessionBranchState;
	/** Verdict of Tallpine's review of the current head (PR head or branch tip); absent while undecided. */
	reviewVerdict?: "approved" | "changes_requested";
	/** The session's work reached its base: PR merged or local branch landed. */
	landed?: boolean;
	/** Set while the task's generated branch name is on the way; keep `branch` private meanwhile. */
	branchNaming?: boolean;
	/** Set while this launch runs a harness release an update replaced; it moves to `version` when idle. */
	staleBinary?: SessionStaleBinary;
	/** Background tasks the agent is running (commands, monitors); each can be stopped by id. */
	backgroundTasks?: SessionBackgroundTask[];
	/** Set while an idle Chat agent sleeps: its processes are stopped and it resumes on its next message. */
	hibernatedAt?: string;
	/** Set while the provider account is out of quota: no turn starts until then; the agent resumes by itself. */
	pausedUntil?: string;
	/** Durable runtime fact from the daemon; independent of the derived SCM-aware status. */
	isTerminated?: boolean;
	chatProviderPreserved?: boolean;
	/** Set when the session's harness was removed from this build; the session is read-only. */
	unsupportedHarnessMessage?: string;
	/** User preference to tear down this session when its PR set completes through a merge. */
	terminateOnPrMerge?: boolean;
	/** Whether SCM review feedback is automatically injected into the worker. */
	autoInjectReview?: boolean;
	/** Whether CI failures are automatically injected into the worker. */
	autoInjectCI?: boolean;
	/** ISO timestamp from the daemon — used for relative time in the inspector. */
	createdAt?: string;
	/** ISO timestamp from the daemon. */
	updatedAt: string;
	/** ISO timestamp of the latest real user-authored message, when known. */
	lastUserMessageAt?: string;
	/** ISO timestamp of human direction or deliberate same-project orchestrator direction. */
	lastInteractionAt?: string;
	isPinned?: boolean;
	pinnedAt?: string;
	/** Raw agent lifecycle activity from the daemon. */
	activity?: SessionActivity;
	/**
	 * Live preview target set by the daemon (via `tallpine preview`) and streamed over
	 * CDC. When non-empty, the browser panel opens and navigates here.
	 */
	previewUrl?: string;
	/**
	 * Monotonic counter the daemon bumps on every `tallpine preview` call (even when
	 * previewUrl is unchanged), so the browser panel can re-navigate / refresh on
	 * a repeated preview of the same target.
	 */
	previewRevision?: number;
	/** The session's git diff against its base, when known. */
	changedFiles?: ChangedFile[];
	/** Pre-filled commit subject for the Git rail, when known. */
	commitMessage?: string;
	/**
	 * The session's attributed pull requests. One session can own many (a stack
	 * or independent PRs); empty when none are open yet. Status aggregation is
	 * done server-side, so {@link status} already reflects all of these.
	 */
	prs: PullRequestFacts[];
	/** What kind of durable output this session has produced, when known. */
	outputType?: SessionOutputType;
	/** The deliverable requested at spawn; "artifact" for sessions started from Create artifact. */
	requestedOutput?: "artifact";
	/**
	 * Whether the session's output includes artifact files. The daemon's session
	 * list reports only this; the files themselves come from the single-session
	 * read and are merged into {@link artifactFiles} by the workspace query.
	 */
	hasArtifacts?: boolean;
	/**
	 * Files present in this session's artifact directory, when {@link outputType}
	 * is `"artifact"`. Empty/absent for sessions whose output is a PR or nothing,
	 * and for terminated sessions until they are opened.
	 */
	artifactFiles?: SessionArtifact[];
};

// Tracker providers whose ids the intake daemon stamps sessions with, in
// "<provider>:<native>" form. Adding a provider (Linear, Jira, ...) later is
// just another prefix in this list — no caller of canonicalTrackerIssueId
// needs to change.
const TRACKER_PROVIDER_PREFIXES = ["github:"] as const;

/**
 * The provider-prefixed issue id if `issueId` came from tracker intake, or
 * undefined for manually created sessions (whose issueId, if any, is a plain
 * task title with no provider prefix).
 */
export function canonicalTrackerIssueId(issueId?: string): string | undefined {
	if (!issueId) return undefined;
	return TRACKER_PROVIDER_PREFIXES.some((prefix) => issueId.startsWith(prefix)) ? issueId : undefined;
}

export type ProjectKind = "single_repo" | "workspace" | "scratch";

/** UI-only grouping for sessions that have no daemon project row. */
export const STANDALONE_WORKSPACE_ID = "__standalone__" as const;
export const STANDALONE_PROJECT_KIND = "standalone" as const;

const projectKinds = new Set<ProjectKind>(["single_repo", "workspace", "scratch"]);

export function toProjectKind(kind?: string): ProjectKind | undefined {
	return projectKinds.has(kind as ProjectKind) ? (kind as ProjectKind) : undefined;
}

export type WorkspaceRepoSummary = {
	name: string;
	relativePath: string;
	repo: string;
};

// Open PRs (actionable) sort above merged/closed; ties break by number.
const prStateRank: Record<PRState, number> = { open: 0, draft: 1, merged: 2, closed: 3 };

/** A session's PRs ordered actionable-first (open, draft, merged, closed). */
export function sortedPRs(session: WorkspaceSession): PullRequestFacts[] {
	return [...session.prs].sort((a, b) => prStateRank[a.state] - prStateRank[b.state] || a.number - b.number);
}

/** PRs still in flight (open or draft). */
export function openPRs(session: WorkspaceSession): PullRequestFacts[] {
	return session.prs.filter((pr) => pr.state === "open" || pr.state === "draft");
}

export function mergedPRCount(session: WorkspaceSession): number {
	return session.prs.filter((pr) => pr.state === "merged").length;
}

/** The highest-priority PR for compact one-line surfaces (board card, sidebar). */
export function primaryPR(session: WorkspaceSession): PullRequestFacts | undefined {
	return sortedPRs(session)[0];
}

/** Artifact files to show in the Summary panel, for sessions whose output includes artifacts. */
export function sessionArtifacts(session: WorkspaceSession): SessionArtifact[] {
	if (session.outputType !== "artifact" && session.outputType !== "pr_artifact") return [];
	return session.artifactFiles ?? [];
}

export function isOrchestratorSession(session: Pick<WorkspaceSession, "id" | "kind">): boolean {
	return session.kind === "orchestrator" || session.id.endsWith("-orchestrator");
}

/**
 * The project's LIVE orchestrator, if any. Terminated orchestrator rows stay in
 * the session list (the daemon returns all sessions, ordered by spawn number),
 * so an earlier dead orchestrator must not shadow a live one — its zellij
 * session is deleted and attaching to it dead-ends in an instant
 * "[process exited]". No live orchestrator → undefined, so the topbar offers
 * Spawn instead of navigating to a dead session.
 */
export function findProjectOrchestrator(
	workspaces: WorkspaceSummary[],
	projectId: string,
): WorkspaceSession | undefined {
	const workspace = workspaces.find((w) => w.id === projectId);
	return newestActiveOrchestrator(workspace?.sessions ?? []);
}

/** The project's default orchestrator: lane orchestrators are legitimate extras and never stand in for it. */
export function newestActiveOrchestrator(sessions: WorkspaceSession[]): WorkspaceSession | undefined {
	const active = sessions.filter((session) => isOrchestratorSession(session) && sessionIsActive(session) && !session.lane);
	return active.reduce<WorkspaceSession | undefined>(
		(newest, session) => (!newest || sessionNewer(session, newest) ? session : newest),
		undefined,
	);
}

function sessionNewer(a: WorkspaceSession, b: WorkspaceSession): boolean {
	const aCreated = timestamp(a.createdAt);
	const bCreated = timestamp(b.createdAt);
	if (aCreated !== bCreated) return aCreated > bCreated;
	const aUpdated = timestamp(a.updatedAt);
	const bUpdated = timestamp(b.updatedAt);
	if (aUpdated !== bUpdated) return aUpdated > bUpdated;
	return a.id > b.id;
}

function sessionRecentlyMessagedNewer(a: WorkspaceSession, b: WorkspaceSession): boolean {
	const aMessaged = sessionLastMessageTimestamp(a);
	const bMessaged = sessionLastMessageTimestamp(b);
	if (aMessaged !== bMessaged) return aMessaged > bMessaged;
	const aUpdated = timestamp(a.updatedAt);
	const bUpdated = timestamp(b.updatedAt);
	if (aUpdated !== bUpdated) return aUpdated > bUpdated;
	return a.id > b.id;
}

/** The sidebar's direction-age label and sort share the same timestamp. */
function sessionLastMessageTimestamp(session: WorkspaceSession): number {
	return (
		validTimestamp(session.lastInteractionAt) ??
		validTimestamp(session.lastUserMessageAt) ??
		validTimestamp(session.createdAt) ??
		0
	);
}

/**
 * The workspaces as the sidebar needs them. Every agent hook rewrites a session's
 * `updatedAt` and `activity.lastActivityAt`, yet the sidebar shows neither, so
 * with them a busy agent re-rendered its whole project tree on each hook. Here
 * `lastActivityAt` is blanked, and `updatedAt` is kept only where it can decide an
 * order: on sessions whose sort key (last message time, or creation time) ties
 * another session's, which is when the sidebar's sorts read it. Callers share the
 * result structurally with the previous one so unchanged projects keep identity.
 */
export function withoutSidebarChurn(workspaces: WorkspaceSummary[]): WorkspaceSummary[] {
	const messaged = new Map<number, number>();
	const created = new Map<number, number>();
	for (const workspace of workspaces) {
		for (const session of workspace.sessions) {
			const lastMessage = sessionLastMessageTimestamp(session);
			const createdAt = timestamp(session.createdAt);
			messaged.set(lastMessage, (messaged.get(lastMessage) ?? 0) + 1);
			created.set(createdAt, (created.get(createdAt) ?? 0) + 1);
		}
	}
	const tied = (session: WorkspaceSession) =>
		(messaged.get(sessionLastMessageTimestamp(session)) ?? 0) > 1 || (created.get(timestamp(session.createdAt)) ?? 0) > 1;
	return workspaces.map((workspace) => ({
		...workspace,
		sessions: workspace.sessions.map((session) => ({
			...session,
			updatedAt: tied(session) ? session.updatedAt : "",
			activity: session.activity && { ...session.activity, lastActivityAt: "" },
		})),
	}));
}

function timestamp(value?: string): number {
	return validTimestamp(value) ?? 0;
}

function validTimestamp(value?: string): number | undefined {
	if (!value) return undefined;
	const parsed = Date.parse(value);
	return Number.isNaN(parsed) ? undefined : parsed;
}

export function workerSessions(sessions: WorkspaceSession[]): WorkspaceSession[] {
	return sessions.filter((s) => !isOrchestratorSession(s));
}

/** Worker sessions ordered by the user's latest message (else creation), newest first. */
export function sortedWorkerSessions(sessions: WorkspaceSession[]): WorkspaceSession[] {
	return workerSessions(sessions).sort((a, b) =>
		sessionRecentlyMessagedNewer(b, a) ? 1 : sessionRecentlyMessagedNewer(a, b) ? -1 : 0,
	);
}

export function sessionIsActive(session: WorkspaceSession): boolean {
	return session.isTerminated !== true && session.status !== "terminated";
}

/**
 * Whether the agent PROCESS is gone while the session ROW is still alive —
 * Ctrl+C, `/exit`, a usage limit. Recovered with `resume-agent`, not `restore`.
 * Not `sessionIsActive`, which reports row liveness and calls this state alive.
 */
export function sessionAgentExited(session: WorkspaceSession | undefined): boolean {
	return Boolean(session && session.activity?.state === "exited" && sessionIsActive(session));
}

/** Whether a session can accept a Cue from its topbar. The daemon makes the
 * final decision, including whether a command Cue's worktree still exists. */
export function sessionCueTargetAvailable(session: WorkspaceSession | undefined): boolean {
	return Boolean(session && sessionIsActive(session) && session.activity?.state !== "exited" && session.activity?.state !== "blocked");
}

export function sessionNeedsAttention(session: WorkspaceSession): boolean {
	return sessionWantsAttention(session);
}

export { attentionZone, attentionZoneLabel, attentionZoneOrder } from "../lib/session-presentation";
export type { AttentionZone } from "../lib/session-presentation";

export type WorkspaceSummary = {
	id: string;
	/** Installation ID of the daemon that owns this project; absent for local. */
	hostId?: string;
	name: string;
	/**
	 * Discriminator for where the project lives. Local projects carry the
	 * daemon's ProjectKind (or undefined for older daemons).
	 */
	kind?: ProjectKind | typeof STANDALONE_PROJECT_KIND;
	/** Local checkout path. */
	path: string;
	folderMissing?: boolean;
	workspaceRepos?: WorkspaceRepoSummary[];
	type?: "main" | "worktree";
	orchestratorAgent?: AgentProvider;
	accentColor?: string;
	diff?: {
		additions: number;
		deletions: number;
	};
	/**
	 * Terminated worker sessions the daemon holds for this project. The workspace
	 * query lists live sessions only; archived ones are paged on demand
	 * (useArchivedSessions), and this sizes that archive without downloading it.
	 */
	archivedCount?: number;
	sessions: WorkspaceSession[];
};

export function hasConfiguredOrchestratorAgent(
	workspace: Pick<WorkspaceSummary, "orchestratorAgent"> | undefined,
): boolean {
	return Boolean(workspace?.orchestratorAgent);
}

export function orchestratorNeedsRestart(workspace: WorkspaceSummary, orchestrator?: WorkspaceSession): boolean {
	if (!orchestrator || !workspace.orchestratorAgent) return false;
	return orchestrator.provider !== workspace.orchestratorAgent;
}

export type OrchestratorHealth =
	| { state: "ok" }
	| { state: "restarting"; message: string }
	| { state: "restart_needed"; message: string }
	| { state: "missing"; message: string }
	| { state: "duplicates"; message: string };

export function orchestratorHealth(workspace: WorkspaceSummary, restarting = false): OrchestratorHealth {
	if (restarting) {
		return {
			state: "restarting",
			message: "Restarting orchestrator. New tasks wait until the replacement is ready.",
		};
	}
	// Only orchestrators sharing a lane (or all lane-less) compete; one per lane is normal.
	const perLane = new Map<string, number>();
	for (const session of workspace.sessions) {
		if (!isOrchestratorSession(session) || !sessionIsActive(session)) continue;
		perLane.set(session.lane ?? "", (perLane.get(session.lane ?? "") ?? 0) + 1);
	}
	if ([...perLane.values()].some((count) => count > 1)) {
		return {
			state: "duplicates",
			message:
				"Multiple orchestrators are active. The newest one is used; stale ones will be cleaned up on daemon reconcile.",
		};
	}
	const orchestrator = newestActiveOrchestrator(workspace.sessions);
	if (!orchestrator) {
		return { state: "missing", message: "No orchestrator is running for this project." };
	}
	if (orchestratorNeedsRestart(workspace, orchestrator)) {
		return {
			state: "restart_needed",
			message: `Configured orchestrator agent is ${workspace.orchestratorAgent}; running agent is ${orchestrator.provider}.`,
		};
	}
	return { state: "ok" };
}

export function toAgentProvider(provider?: string): AgentProvider {
	if (provider === "fake") return provider;
	return AGENT_OPTIONS.find((candidate) => candidate === provider) ?? "codex";
}

export type NextSessionNavigation =
	| { target: "session"; sessionId: string }
	| { target: "project" };

/**
 * Resolves where to navigate after an active session is killed.
 * Prioritizes:
 * 1. Adjacent remaining worker session (previous if available, else first remaining).
 * 2. Active non-terminated orchestrator if no worker sessions remain.
 * 3. Project board if no alive sessions remain.
 */
export function resolveNextNavigationAfterSessionKill(
	workspace: WorkspaceSummary | undefined,
	killedSessionId: string,
	sessionsInDisplayOrder?: WorkspaceSession[],
): NextSessionNavigation {
	if (!workspace) return { target: "project" };

	const workerList = (sessionsInDisplayOrder ?? sortedWorkerSessions(workspace.sessions)).filter(
		(s) => s.isTerminated !== true,
	);
	const currentIndex = workerList.findIndex((s) => s.id === killedSessionId);
	const remaining = workerList.filter((s) => s.id !== killedSessionId);

	if (remaining.length > 0) {
		const nextIndex = currentIndex > 0 ? currentIndex - 1 : 0;
		const nextSession = remaining[nextIndex] ?? remaining[0];
		return { target: "session", sessionId: nextSession.id };
	}

	const orchestrator = newestActiveOrchestrator(workspace.sessions);
	if (orchestrator && orchestrator.id !== killedSessionId && orchestrator.isTerminated !== true) {
		return { target: "session", sessionId: orchestrator.id };
	}

	return { target: "project" };
}
