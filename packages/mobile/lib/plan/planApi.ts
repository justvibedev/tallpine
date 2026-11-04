import { PLAN_PATHS, isPlannerOff, planPath } from "@tallpine/shared/api/plan";
import { ApiError, req } from "../api";
import type { ServerConfig } from "../config";
import type { PlanAnswersRequest, PlanView, ProjectPlan } from "./planCardModel";

/** Plans of a project that wait on someone. Empty while the daemon's planner switch is off (404). */
export async function listProjectPlans(cfg: ServerConfig, projectId: string): Promise<ProjectPlan[]> {
	try {
		const res = await req(cfg, planPath(PLAN_PATHS.list, projectId));
		const data = await res.json();
		return Array.isArray(data?.plans) ? data.plans : [];
	} catch (e) {
		if (e instanceof ApiError && isPlannerOff(e.status)) return [];
		throw e;
	}
}

/** The item's full plan record. Null when there is none or the planner is off. */
export async function getItemPlan(cfg: ServerConfig, projectId: string, ref: string): Promise<PlanView | null> {
	try {
		const res = await req(cfg, planPath(PLAN_PATHS.item, projectId, ref));
		return (await res.json())?.plan ?? null;
	} catch (e) {
		if (e instanceof ApiError && isPlannerOff(e.status)) return null;
		throw e;
	}
}

async function post(cfg: ServerConfig, path: string, body: unknown): Promise<PlanView> {
	const res = await req(cfg, path, { method: "POST", body: JSON.stringify(body) });
	return (await res.json()).plan;
}

export const answerPlan = (cfg: ServerConfig, projectId: string, ref: string, body: PlanAnswersRequest) => post(cfg, planPath(PLAN_PATHS.answers, projectId, ref), body);
export const acceptPlanDefaults = (cfg: ServerConfig, projectId: string, ref: string) => post(cfg, planPath(PLAN_PATHS.defaults, projectId, ref), {});
export const approvePlan = (cfg: ServerConfig, projectId: string, ref: string) => post(cfg, planPath(PLAN_PATHS.approve, projectId, ref), {});
