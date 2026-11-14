import { describe, expect, it } from "vitest";
import type { OwnerAsk } from "../../lib/ask-api";
import type { ProjectPlan } from "../../lib/plan-api";
import { planFixture } from "../../test/plan-fixtures";
import { waitingCount } from "./askModel";

const ask = (o: Partial<OwnerAsk>): OwnerAsk => ({ id: "a", projectId: "p1", ref: "Q1", text: "t", state: "open", anchored: false, options: [], recommended: "", createdAt: "", updatedAt: "", ...o });
const plan = (o: { lane?: string; starter?: string; status?: string }): ProjectPlan =>
	({ itemRef: "B1", itemTitle: "x", lane: o.lane, plan: planFixture({ status: (o.status ?? "awaiting_answers") as never, starterSessionId: o.starter }) });

describe("waitingCount", () => {
	it("counts a main chat's open asks and waiting plans, not lane work or closed asks", () => {
		const n = waitingCount(
			[ask({ id: "1" }), ask({ id: "2", lane: "B7" }), ask({ id: "3", state: "answered" }), ask({ id: "4", state: "withdrawn" })],
			[plan({}), plan({ lane: "B7", starter: "lane-1" }), plan({ status: "writing" })],
			{},
		);
		expect(n).toBe(2);
	});

	it("counts a lane's asks and the plans its orchestrator started", () => {
		const n = waitingCount(
			[ask({ id: "1" }), ask({ id: "2", lane: "B7" }), ask({ id: "3", lane: "B7", ref: "Q3" })],
			[plan({ lane: "B7", starter: "lane-1" }), plan({ lane: "B8", starter: "lane-2" }), plan({ lane: "B7", starter: "lane-1", status: "awaiting_approval" })],
			{ lane: "B7", sessionId: "lane-1" },
		);
		expect(n).toBe(4);
	});

	it("is 0 with nothing waiting", () => {
		expect(waitingCount([], [], {})).toBe(0);
		expect(waitingCount([], [plan({ status: "approved" })], {})).toBe(0);
	});
});
