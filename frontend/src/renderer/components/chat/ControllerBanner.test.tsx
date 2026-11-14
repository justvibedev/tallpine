import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { WorkspaceSession } from "../../types/workspace";
import { ControllerBanner } from "./ControllerBanner";
import { finishedSessionLabel } from "./ChatWorkspaceContent";

describe("ControllerBanner finished sessions", () => {
	it("shows a calm finished state with no resume for a deliberate kill", () => {
		render(<ControllerBanner controller={{ state: "stopped" }} onResume={() => {}} finishedLabel="Finished: PR #7 merged" />);
		expect(screen.getByText("Finished: PR #7 merged")).toBeTruthy();
		expect(screen.queryByText("The agent controller stopped")).toBeNull();
		expect(screen.queryByText("Resume agent")).toBeNull();
	});
	it("keeps the red banner for an unexpected exit", () => {
		render(<ControllerBanner controller={{ state: "stopped" }} onResume={() => {}} />);
		expect(screen.getByText("The agent controller stopped")).toBeTruthy();
		expect(screen.getByText("Resume agent")).toBeTruthy();
	});
	it("labels only terminated sessions", () => {
		const base = { isTerminated: true, prs: [{ number: 7, state: "merged" }] } as unknown as WorkspaceSession;
		expect(finishedSessionLabel(base)).toBe("Finished: PR #7 merged");
		expect(finishedSessionLabel({ ...base, prs: [] })).toBe("Session finished");
		expect(finishedSessionLabel({ ...base, isTerminated: false })).toBeUndefined();
	});
});
