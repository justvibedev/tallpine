/** Mapping of daemon session views to workspace sessions. */
import { toProvisionSteps } from "../../lib/provision-steps";
import type { components } from "../../../api/schema";
import { toReviewerHarnessId } from "../../lib/reviewer-harnesses";
import { appI18n } from "../../i18n";
import {
	type PRState,
	type PullRequestFacts,
	type SessionArtifact,
	toAgentProvider,
	toKanbanColumn,
	toSessionActivity,
	toSessionStatus,
	type WorkspaceSession,
	type WorkspaceSummary,
	STANDALONE_WORKSPACE_ID,
} from "../../types/workspace";

export function standaloneWorkspaceName(): string {
	return appI18n.t("standalone.workspaceName");
}

export function placeStandaloneWorkspaceLast(workspaces: WorkspaceSummary[]): WorkspaceSummary[] {
	const standalone = workspaces.find((workspace) => workspace.id === STANDALONE_WORKSPACE_ID);
	if (!standalone) return workspaces;
	return [...workspaces.filter((workspace) => workspace.id !== STANDALONE_WORKSPACE_ID), standalone];
}

export function toPullRequestFacts(pr: components["schemas"]["SessionPRFacts"]): PullRequestFacts {
	return {
		url: pr.url,
		number: pr.number,
		state: pr.state as PRState,
		ci: pr.ci,
		review: pr.review,
		mergeability: pr.mergeability,
		reviewComments: pr.reviewComments,
		updatedAt: pr.updatedAt,
	};
}

export function toSessionArtifact(artifact: components["schemas"]["SessionArtifact"]): SessionArtifact {
	return {
		kind: artifact.kind,
		name: artifact.name,
		path: artifact.path,
		previewUrl: artifact.previewUrl,
		rawUrl: artifact.rawUrl,
		inlineUrl: artifact.inlineUrl,
		size: artifact.size,
		updatedAt: artifact.updatedAt,
	};
}

export function toWorkspaceSession(
	session: components["schemas"]["SessionView"],
	project: Pick<WorkspaceSummary, "id" | "name">,
): WorkspaceSession {
	const statusReadiness = session.statusReadiness ?? "ready";
	const status = statusReadiness === "ready" ? toSessionStatus(session.status, session.isTerminated) : "unknown";
	const scmStatus = session.scmStatus ? toSessionStatus(session.scmStatus) : undefined;
	const kanbanColumn = toKanbanColumn(session.kanbanColumn, status);
	const activity = statusReadiness === "ready" ? toSessionActivity(session.activity) : undefined;
	if (statusReadiness === "ready" && status === "unknown") reportUnknownSessionField("status", session.status);
	if (statusReadiness === "ready" && (!activity || activity.state === "unknown")) {
		reportUnknownSessionField("activity", session.activity?.state);
	}
	return {
		id: session.id,
		terminalHandleId: session.terminalHandleId,
		terminalGeneration: session.terminalGeneration,
		workspaceId: project.id,
		workspaceName: project.name,
		title: session.displayName ?? session.issueId ?? session.id,
		issueId: session.issueId,
		provider: toAgentProvider(session.harness),
		reviewerHarness: toReviewerHarnessId(session.reviewerHarness),
		reviewerConfig: session.reviewerConfig
			? {
					model: session.reviewerConfig.model ?? undefined,
					mode: session.reviewerConfig.mode ?? undefined,
					permissions: session.reviewerConfig.permissions ?? undefined,
				}
			: undefined,
		autoReviewEnabled: session.autoReviewEnabled ?? false,
		kind: session.kind === "orchestrator" ? "orchestrator" : session.kind === "worker" ? "worker" : undefined,
		lane: session.lane || undefined,
		mode: session.mode === "chat" ? "chat" : "tui",
		branch: session.branch || undefined,
		status,
		needsInputAudience: session.needsInputAudience,
		scmStatus,
		kanbanColumn,
		displayStatus: session.displayStatus || undefined,
		statusReadiness,
		provisionState: session.provisionState,
		provisionError: session.provisionError || undefined,
		provisionSteps: toProvisionSteps(session.provisionSteps),
		branchState: session.branchState,
		reviewVerdict: session.reviewVerdict || undefined,
		landed: session.landed || undefined,
		branchNaming: session.branchNaming,
		staleBinary: session.staleBinary ? { version: session.staleBinary.version, since: session.staleBinary.since, switchFailedAt: session.staleBinary.switchFailedAt ?? undefined } : undefined,
		backgroundTasks: session.backgroundTasks?.length ? session.backgroundTasks.map((task) => ({ id: task.id, name: task.name, kind: task.kind, model: task.model, startedAt: task.startedAt ?? undefined })) : undefined,
		hibernatedAt: session.hibernatedAt ?? undefined,
		pausedUntil: session.pausedUntil ?? undefined,
		isTerminated: session.isTerminated,
		workspaceCleanup: session.workspaceCleanup,
		chatProviderPreserved: session.chatProviderPreserved,
		unsupportedHarnessMessage: session.unsupportedHarnessMessage || undefined,
		terminateOnPrMerge: session.terminateOnPrMerge ?? false,
		autoInjectReview: session.autoInjectReview ?? true,
		autoInjectCI: session.autoInjectCI ?? true,
		createdAt: session.createdAt,
		updatedAt: session.updatedAt,
		lastUserMessageAt: session.lastUserMessageAt ?? undefined,
		lastInteractionAt: session.lastInteractionAt ?? undefined,
		activity,
		previewUrl: session.previewUrl,
		previewRevision: session.previewRevision,
		isPinned: session.isPinned ?? false,
		pinnedAt: session.pinnedAt ?? undefined,
		prs: (session.prs ?? []).map(toPullRequestFacts),
		outputType: session.outputType,
		...(session.requestedOutput ? { requestedOutput: session.requestedOutput } : {}),
		hasArtifacts: session.hasArtifacts ?? (session.artifactFiles?.length ? true : undefined),
		artifactFiles: session.artifactFiles?.map(toSessionArtifact),
	};
}

export const reportedUnknownSessionFields = new Set<string>();

export function reportUnknownSessionField(field: "status" | "activity", value?: string): void {
	const reason = value ? "unrecognized" : "missing";
	const key = `${field}:${reason}`;
	if (reportedUnknownSessionFields.has(key)) return;
	reportedUnknownSessionFields.add(key);
}

export function toLocalWorkspaceSession(
	session: components["schemas"]["SessionView"],
	workspaceId: string,
	workspaceName: string,
): WorkspaceSession {
	const status = toSessionStatus(session.status, session.isTerminated);
	const scmStatus = session.scmStatus ? toSessionStatus(session.scmStatus) : undefined;
	const kanbanColumn = toKanbanColumn(session.kanbanColumn, status);
	const activity = toSessionActivity(session.activity);
	if (status === "unknown") reportUnknownSessionField("status", session.status);
	if (!activity || activity.state === "unknown") reportUnknownSessionField("activity", session.activity?.state);
	return {
		id: session.id,
		terminalHandleId: session.terminalHandleId,
		terminalGeneration: session.terminalGeneration,
		workspaceId,
		workspaceName,
		title: session.displayName ?? session.issueId ?? session.id,
		issueId: session.issueId,
		provider: toAgentProvider(session.harness),
		reviewerHarness: toReviewerHarnessId(session.reviewerHarness),
		reviewerConfig: session.reviewerConfig
			? {
					model: session.reviewerConfig.model ?? undefined,
					mode: session.reviewerConfig.mode ?? undefined,
					permissions: session.reviewerConfig.permissions ?? undefined,
				}
			: undefined,
		autoReviewEnabled: session.autoReviewEnabled ?? false,
		kind: session.kind === "orchestrator" ? "orchestrator" : session.kind === "worker" ? "worker" : undefined,
		lane: session.lane || undefined,
		// Carried through verbatim: the session surface must render from
		// the mode this session was created with, not from the current default.
		mode: session.mode === "chat" ? "chat" : "tui",
		branch: session.branch || undefined,
		status,
		needsInputAudience: session.needsInputAudience,
		scmStatus,
		kanbanColumn,
		displayStatus: session.displayStatus || undefined,
		provisionState: session.provisionState,
		provisionError: session.provisionError || undefined,
		provisionSteps: toProvisionSteps(session.provisionSteps),
		branchState: session.branchState,
		reviewVerdict: session.reviewVerdict || undefined,
		landed: session.landed || undefined,
		branchNaming: session.branchNaming,
		staleBinary: session.staleBinary ? { version: session.staleBinary.version, since: session.staleBinary.since, switchFailedAt: session.staleBinary.switchFailedAt ?? undefined } : undefined,
		isTerminated: session.isTerminated,
		workspaceCleanup: session.workspaceCleanup,
		terminateOnPrMerge: session.terminateOnPrMerge ?? false,
		autoInjectReview: session.autoInjectReview ?? true,
		autoInjectCI: session.autoInjectCI ?? true,
		createdAt: session.createdAt,
		updatedAt: session.updatedAt,
		lastUserMessageAt: session.lastUserMessageAt ?? undefined,
		lastInteractionAt: session.lastInteractionAt ?? undefined,
		activity,
		previewUrl: session.previewUrl,
		previewRevision: session.previewRevision,
		isPinned: session.isPinned ?? false,
		pinnedAt: session.pinnedAt ?? undefined,
		prs: (session.prs ?? []).map(toPullRequestFacts),
		outputType: session.outputType,
		...(session.requestedOutput ? { requestedOutput: session.requestedOutput } : {}),
		hasArtifacts: session.hasArtifacts ?? (session.artifactFiles?.length ? true : undefined),
		artifactFiles: session.artifactFiles?.map(toSessionArtifact),
	};
}
