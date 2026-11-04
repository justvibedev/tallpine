import { describe, expect, it } from "vitest";
import { PLAN_PATHS, isPlannerOff, planPath } from "./plan";

describe("plan paths", () => {
	it("fills and encodes project and ref", () => {
		expect(planPath(PLAN_PATHS.answers, "p/1", "B 2")).toBe("/api/v1/projects/p%2F1/backlog/B%202/plan/answers");
		expect(planPath(PLAN_PATHS.list, "p1")).toBe("/api/v1/projects/p1/plans");
		expect(planPath(PLAN_PATHS.item, "p1", "B1")).toBe("/api/v1/projects/p1/backlog/B1/plan");
	});

	it("keeps every endpoint the clients call", () => {
		expect(Object.values(PLAN_PATHS).sort()).toEqual([
			"/api/v1/projects/{projectId}/backlog/{ref}/plan",
			"/api/v1/projects/{projectId}/backlog/{ref}/plan/answers",
			"/api/v1/projects/{projectId}/backlog/{ref}/plan/approve",
			"/api/v1/projects/{projectId}/backlog/{ref}/plan/build-anyway",
			"/api/v1/projects/{projectId}/backlog/{ref}/plan/defaults",
			"/api/v1/projects/{projectId}/backlog/{ref}/plan/replan",
			"/api/v1/projects/{projectId}/plans",
		]);
	});

	it("treats only 404 as the planner switch being off", () => {
		expect(isPlannerOff(404)).toBe(true);
		expect(isPlannerOff(500)).toBe(false);
		expect(isPlannerOff(200)).toBe(false);
		expect(isPlannerOff(undefined)).toBe(false);
	});
});
