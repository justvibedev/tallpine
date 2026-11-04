import { describe, expect, it } from "vitest";
import { boardSessionNeedsAttention } from "@tallpine/product-ui";
import type { WorkspaceSession } from "../types/workspace";
import { toBoardSessionPresentation } from "./SessionsBoardAdapters";

const base = {
	id: "s1",
	workspaceId: "w",
	workspaceName: "w",
	title: "t",
	provider: "claude-code",
	branch: "b",
	status: "needs_input",
	updatedAt: "2026-01-01T00:00:00Z",
	prs: [],
} as WorkspaceSession;

describe("toBoardSessionPresentation attention", () => {
	it("gives no chip to a session waiting on the orchestrator", () => {
		const card = toBoardSessionPresentation({ ...base, needsInputAudience: "orchestrator" });
		expect(boardSessionNeedsAttention(card)).toBe(false);
	});

	it("keeps the chip for a human-owned needs_input", () => {
		const card = toBoardSessionPresentation({ ...base, needsInputAudience: "human" });
		expect(boardSessionNeedsAttention(card)).toBe(true);
	});
});
