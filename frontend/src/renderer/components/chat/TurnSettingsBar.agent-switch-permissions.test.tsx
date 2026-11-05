import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { TurnSettingsBar } from "./TurnSettingsBar";

vi.mock("./AgentSwitchPicker", async (original) => ({ ...await original<typeof import("./AgentSwitchPicker")>(), AgentModelPicker: () => null }));

describe("permissions for the agent picked for the next message", () => {
	it("goes back to the chat's own mode when leaving an agent that needed another", () => {
		render(
			<TurnSettingsBar
				harness="pi"
				models={[]}
				settings={{ approvalMode: "bypass-permissions" }}
				onChange={vi.fn()}
				agentSwitch={{ projectId: "p", pending: { harness: "claude-code" }, chatPermissionMode: "accept-edits", onPendingChange: vi.fn() }}
			/>,
		);
		expect(screen.getByRole("button", { name: "Approval policy for Claude Code" })).toHaveTextContent("Accept edits");
	});

});
