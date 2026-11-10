/**
 * The plan endpoints, one table for both clients. Templates keep the literal
 * types so the desktop's openapi client still checks them against the spec.
 */
const API = "/api/v1/projects/{projectId}";

export const PLAN_PATHS = {
	list: `${API}/plans`,
	item: `${API}/backlog/{ref}/plan`,
	answers: `${API}/backlog/{ref}/plan/answers`,
	defaults: `${API}/backlog/{ref}/plan/defaults`,
	approve: `${API}/backlog/{ref}/plan/approve`,
	replan: `${API}/backlog/{ref}/plan/replan`,
	cancel: `${API}/backlog/{ref}/plan/cancel`,
	buildAnyway: `${API}/backlog/{ref}/plan/build-anyway`,
} as const;

/** Fills a template's {projectId}/{ref}, URL-encoding each value. */
export function planPath(template: string, projectId: string, ref?: string): string {
	return template.replace("{projectId}", encodeURIComponent(projectId)).replace("{ref}", encodeURIComponent(ref ?? ""));
}

/** The daemon answers 404 on the plan endpoints while its planner switch is off. */
export function isPlannerOff(status: number | undefined): boolean {
	return status === 404;
}
