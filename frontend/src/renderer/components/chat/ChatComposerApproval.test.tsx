import { screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { render, renderComposer } from "./ChatComposer.test.setup";
import { ChatComposer } from "./ChatComposer";

describe("ChatComposer approval view", () => {
	it("replaces the editor with the pending approval and its error", () => {
		render(
			<ChatComposer
				onSend={vi.fn()}
				approval={<div>Approve this command?</div>}
				commandError="The approval could not be submitted"
			/>,
		);
		expect(screen.getByText("Approve this command?")).toBeInTheDocument();
		expect(screen.getByRole("alert")).toHaveTextContent("The approval could not be submitted");
		expect(screen.queryByLabelText("Message the agent")).toBeNull();
	});

	it("keeps the normal editor when nothing is awaiting approval", () => {
		renderComposer();
		expect(screen.getByLabelText("Message the agent")).toBeInTheDocument();
	});
});
