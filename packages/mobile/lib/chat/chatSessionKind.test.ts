import { describe, expect, it } from "vitest";
import type { DashboardSession, OrchestratorLink } from "../api";
import { isOrchestratorChat } from "./chatSessionKind";

describe("isOrchestratorChat", () => {
	it("treats a board-listed orchestrator link as an orchestrator", () => {
		const link: OrchestratorLink = { id: "app-orch-1", projectId: "app", projectName: "App", mode: "chat" };
		expect(isOrchestratorChat(link)).toBe(true);
	});

	it("reads the kind of a looked-up session", () => {
		const base = { id: "s1", projectId: "app", mode: "chat" } as DashboardSession;
		expect(isOrchestratorChat({ ...base, kind: "orchestrator" })).toBe(true);
		expect(isOrchestratorChat({ ...base, kind: "worker" })).toBe(false);
		expect(isOrchestratorChat(base)).toBe(false);
	});
});
