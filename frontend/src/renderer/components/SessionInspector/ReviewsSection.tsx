import type { components } from "../../../api/schema";
import { WORKER_DEFAULT_REVIEWERS } from "../../lib/reviewer-harnesses";
import type { WorkspaceSession } from "../../types/workspace";
import { ProjectConfig, OpenReviewerTerminal } from "./types";
import { LocalReviewsSection } from "./LocalReviewsSection";

/** Reviewer harness the daemon accepts, typed from the generated schema. */
export type ReviewerHarness = NonNullable<components["schemas"]["TriggerReviewRequest"]["harness"]>;
export type AgentCatalog = components["schemas"]["AgentReadinessResponse"];

/** The reviewer a session follows: the project's own, then the app-wide default, then the worker's. */
export function resolveDefaultReviewerHarness(config: ProjectConfig | undefined, workerHarness: WorkspaceSession["provider"], appHarness?: string): ReviewerHarness {
	const configuredHarness = config?.reviewers?.[0]?.harness;
	if (configuredHarness) return configuredHarness as ReviewerHarness;
	if (appHarness) return appHarness as ReviewerHarness;
	return WORKER_DEFAULT_REVIEWERS[workerHarness] ?? "claude-code";
}

export function ReviewsSection({
	session,
	hostId,
	onOpenReviewFile,
	onOpenReviewerTerminal,
	onOpenReviewerChat,
	onWorkerMessageSent,
}: {
	session: WorkspaceSession;
	hostId?: string;
	onOpenReviewFile?: (target: { line?: number; path: string }) => void;
	onOpenReviewerTerminal?: OpenReviewerTerminal;
	onOpenReviewerChat?: (reviewId: string) => void;
	onWorkerMessageSent?: () => void;
}) {
	return (
		<LocalReviewsSection
			hostId={hostId}
			onOpenReviewFile={onOpenReviewFile}
			onOpenReviewerTerminal={onOpenReviewerTerminal}
			onOpenReviewerChat={onOpenReviewerChat}
			onWorkerMessageSent={onWorkerMessageSent}
			session={session}
		/>
	);
}
