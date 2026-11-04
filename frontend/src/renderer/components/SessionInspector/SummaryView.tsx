import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { memo } from "react";
import { SessionInspectorSummaryView } from "@tallpine/product-ui";
import { clientForSessionHost } from "../../lib/host-clients";
import { ResumeAgentControl } from "../ResumeAgentControl";
import { SessionBranchSummary } from "../SessionBranchSummary";
import { SessionAccountSummary } from "../SessionAccountSummary";
import { SessionWorkerInstructions } from "../SessionWorkerInstructions";
import { useSessionScmSummary } from "../../hooks/useSessionScmSummary";
import { prsSpanRepositories, sessionPRDisplaySummaries } from "../../lib/pr-display";
import type { WorkspaceSession } from "../../types/workspace";
import { sessionArtifacts, STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { isExternalRepository, LinkedPRCard } from "./LinkedPRCard";
import { SessionControls } from "./SessionControls";
import { PRSummaryCard } from "./PRSummaryCard";
import { ArtifactSummaryCard } from "./ArtifactSummaryCard";
import { ActivityTimeline } from "./ActivityTimeline";

export const SummaryView = memo(function SummaryView({
	canOpenReviews,
	onOpenArtifact,
	hostId,
	onOpenFiles,
	onOpenReviews,
	session,
}: {
	canOpenReviews: boolean;
	onOpenArtifact?: (target: { feedback?: boolean; path: string }) => void;
	hostId?: string;
	onOpenFiles: () => void;
	onOpenReviews: () => void;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const query = useSessionScmSummary(session.id, true, hostId);
	const linkedPRs = query.data?.linkedPrs ?? [];
	const projectQuery = useQuery({
		queryKey: hostId ? ["project", hostId, session.workspaceId] : ["project", session.workspaceId],
		enabled: linkedPRs.length > 0,
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/projects/{id}", {
				params: { path: { id: session.workspaceId } },
			});
			if (error) throw error;
			return data?.status === "ok" && "repo" in data.project ? data.project : undefined;
		},
	});
	const prSummaries = sessionPRDisplaySummaries(session, query.data?.prs);
	const prCount = prSummaries.length + linkedPRs.length;
	const hasPRs = prCount > 0;
	const artifacts = sessionArtifacts(session);
	const hasArtifacts = artifacts.length > 0;
	const opensPR = hasPRs || session.outputType === "pr" || session.outputType === "pr_artifact";
	const prSectionTitle = prCount > 1 ? t("inspector.pullRequests", { count: prCount }) : opensPR ? t("inspector.pullRequest") : t("inspector.branch");
	const artifactSectionTitle = artifacts.length > 1
		? t("inspector.artifacts", { count: artifacts.length })
		: t("inspector.artifact");
	const openPRNumber = prSummaries.find((pr) => pr.state === "open" || pr.state === "draft")?.number;
	// CI, review, and merge policies act on a PR. Standalone sessions keep the row for Archive.
	const showSessionControls = hasPRs || session.workspaceId === STANDALONE_WORKSPACE_ID;
	return (
		<SessionInspectorSummaryView
			activity={
				<>
					<ActivityTimeline hostId={hostId} prs={prSummaries} session={session} />
					{hostId ? null : <SessionAccountSummary session={session} />}
					<SessionWorkerInstructions hostId={hostId} session={session} />
					<ResumeAgentControl
						className="w-full"
						containerClassName="mt-3 border-t border-(--color-border-settings-input) pt-3"
						hostId={hostId}
						session={session}
					/>
				</>
			}
			activityTitle={t("inspector.activity")}
			artifactCards={
				hasArtifacts ? (
					artifacts.map((artifact) => (
						<ArtifactSummaryCard
							artifact={artifact}
							key={artifact.path}
							onOpenArtifact={onOpenArtifact}
							session={session}
						/>
					))
				) : undefined
			}
			artifactTitle={hasArtifacts ? artifactSectionTitle : undefined}
			branch={
				<SessionBranchSummary
					hostId={hostId}
					onOpenFiles={onOpenFiles}
					openPRNumber={openPRNumber}
					pullRequests={hasPRs ? (
						<>
							{prSummaries.map((pr) => (
								<PRSummaryCard
									canOpenReviews={canOpenReviews}
									key={pr.url || pr.htmlUrl || pr.number}
									showRepository={prsSpanRepositories(prSummaries)}
									onOpenReviews={onOpenReviews}
									pr={pr}
									hostId={hostId}
									sessionId={session.id}
								/>
							))}
							{linkedPRs.map((pr) => <LinkedPRCard external={isExternalRepository(pr, projectQuery.data)} key={pr.url} pr={pr} />)}
						</>
					) : null}
					session={session}
					title={prSectionTitle}
				/>
			}
			completion={showSessionControls ? <SessionControls hostId={hostId} session={session} /> : undefined}
		/>
	);
});
