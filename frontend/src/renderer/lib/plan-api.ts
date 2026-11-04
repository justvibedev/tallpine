import type { components } from "../../api/schema";
import { PLAN_PATHS, isPlannerOff } from "@tallpine/shared/api/plan";
import { apiClient, apiErrorMessage } from "./api-client";

export type PlanView = components["schemas"]["PlanView"];
export type ProjectPlan = components["schemas"]["ProjectPlan"];
export type PlanAnswersRequest = components["schemas"]["PlanAnswersRequest"];

export const projectPlansKey = (projectId: string) => ["backlog", projectId, "plans"] as const;

function fail(error: unknown): never {
	throw new Error(apiErrorMessage(error));
}

/** Plans of a project that still wait on someone. Empty while the planner switch is off (404). */
export async function listProjectPlans(projectId: string): Promise<ProjectPlan[]> {
	const { data, error, response } = await apiClient.GET(PLAN_PATHS.list, { params: { path: { projectId } } });
	if (isPlannerOff(response?.status)) return [];
	if (error) fail(error);
	return data?.plans ?? [];
}

type Params = { params: { path: { projectId: string; ref: string } } };
const at = (projectId: string, ref: string): Params => ({ params: { path: { projectId, ref } } });

export async function answerPlan(projectId: string, ref: string, body: PlanAnswersRequest): Promise<PlanView> {
	const { data, error } = await apiClient.POST(PLAN_PATHS.answers, { ...at(projectId, ref), body });
	if (error || !data) fail(error);
	return data.plan;
}

export async function acceptPlanDefaults(projectId: string, ref: string): Promise<PlanView> {
	const { data, error } = await apiClient.POST(PLAN_PATHS.defaults, { ...at(projectId, ref), body: {} });
	if (error || !data) fail(error);
	return data.plan;
}

export async function approvePlan(projectId: string, ref: string): Promise<PlanView> {
	const { data, error } = await apiClient.POST(PLAN_PATHS.approve, { ...at(projectId, ref), body: {} });
	if (error || !data) fail(error);
	return data.plan;
}

export async function startPlan(projectId: string, ref: string): Promise<PlanView> {
	const { data, error } = await apiClient.POST(PLAN_PATHS.item, at(projectId, ref));
	if (error || !data) fail(error);
	return data.plan;
}

export async function replanItem(projectId: string, ref: string): Promise<PlanView> {
	const { data, error } = await apiClient.POST(PLAN_PATHS.replan, at(projectId, ref));
	if (error || !data) fail(error);
	return data.plan;
}

export async function buildPlanAnyway(projectId: string, ref: string): Promise<PlanView> {
	const { data, error } = await apiClient.POST(PLAN_PATHS.buildAnyway, { ...at(projectId, ref), body: {} });
	if (error || !data) fail(error);
	return data.plan;
}

export const itemPlanKey = (projectId: string, ref: string) => ["backlog", projectId, "plan", ref] as const;

/** The item's full plan record, with the stale flag. Null when there is none or the planner is off. */
export async function getItemPlan(projectId: string, ref: string): Promise<PlanView | null> {
	const { data, error, response } = await apiClient.GET(PLAN_PATHS.item, { params: { path: { projectId, ref } } });
	if (isPlannerOff(response?.status)) return null;
	if (error || !data) fail(error);
	return data.plan;
}

export const plannerEnabledKey = (projectId: string) => ["backlog", projectId, "planner-enabled"] as const;

/** Whether this daemon's planner switch is on: the plans list answers 404 while it is off. */
export async function isPlannerEnabled(projectId: string): Promise<boolean> {
	const { response } = await apiClient.GET(PLAN_PATHS.list, { params: { path: { projectId } } });
	return response?.ok === true;
}
