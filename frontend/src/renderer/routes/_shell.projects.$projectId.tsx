import { createFileRoute, redirect } from "@tanstack/react-router";
import { SessionsBoard } from "../components/SessionsBoard";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { projectLandingOrchestratorId } from "../lib/project-landing";
import type { WorkspaceSummary } from "../types/workspace";

export type ProjectBoardSearch = { view?: "board" };

export const Route = createFileRoute("/_shell/projects/$projectId")({
	validateSearch: (search: Record<string, unknown>): ProjectBoardSearch =>
		search.view === "board" ? { view: "board" } : {},
	// The one place every "open this project" entry point (sidebar row, palette,
	// home, topbar switcher) converges. `view=board` is the explicit board link.
	// The shell loader has already filled the workspace cache, so this reads it
	// instead of fetching; an empty cache keeps the board.
	beforeLoad: ({ params, search, context }) => {
		if (search.view === "board") return;
		const workspaces = context.queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKey);
		const sessionId = projectLandingOrchestratorId(workspaces, params.projectId);
		if (!sessionId) return;
		throw redirect({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: params.projectId, sessionId },
			replace: true,
		});
	},
	component: ProjectBoardRoute,
});

function ProjectBoardRoute() {
	const { projectId } = Route.useParams();
	return <SessionsBoard projectId={projectId} />;
}
