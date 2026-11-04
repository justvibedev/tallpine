import { describe, expect, it } from "vitest";
import type { DashboardSession } from "./api";
import { orchestratorProjectSections } from "./orchestratorView";

const worker = (ciStatus: "passing" | "failing"): DashboardSession =>
	({
		id: "w1",
		projectId: "p",
		status: ciStatus === "failing" ? "ci_failed" : "mergeable",
		pr: {
			number: 1,
			url: "https://example.test/pull/1",
			state: "open",
			ciStatus,
			reviewDecision: "approved",
			mergeability: { mergeable: true },
		},
	}) as unknown as DashboardSession;

describe("orchestrator ready-PR count", () => {
	const detail = (ci: "passing" | "failing") =>
		orchestratorProjectSections(
			[{ id: "p", name: "p" }],
			[worker(ci)],
			[{ id: "o", projectId: "p", projectName: "p", status: "working" } as never],
		)
			.flatMap((s) => s.data)
			.map((r) => r.detail)
			.join("|");

	it("counts a green mergeable PR as ready but not an approved one with failing CI", () => {
		expect(detail("passing")).toContain("is ready");
		expect(detail("failing")).not.toContain("is ready");
	});
});
