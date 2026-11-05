import { interfaceTransitionState, render, workerSession } from "./SessionView.test.setup";
import { resetSessionViewState } from "./SessionView.test.reset";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { SessionView } from "./SessionView";

describe("SessionView interface handoff reason", () => {
	beforeEach(() => {
		resetSessionViewState();
	});

	it("explains an unsupported chat-to-terminal handoff in that direction", async () => {
		interfaceTransitionState.status = { supported: false, targetMode: "tui", reasonCode: "INTERFACE_HANDOFF_UNSUPPORTED" };
		const session = workerSession("sess-1");
		session.mode = "chat";

		render(<SessionView sessionId="sess-1" />);

		const button = screen.getByRole("button", { name: "Switch to terminal UI" });
		expect(button).toBeDisabled();
		await userEvent.hover(button.parentElement as HTMLElement);
		const tooltip = await screen.findByRole("tooltip");
		expect(tooltip).toHaveTextContent("This agent can't continue a chat in the terminal.");
		expect(tooltip).not.toHaveTextContent("to chat");
	});
});
