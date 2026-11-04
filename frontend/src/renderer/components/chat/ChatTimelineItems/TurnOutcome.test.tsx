import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { APP_RESTART_ERROR, TurnOutcome } from "./TurnOutcome";

describe("TurnOutcome interrupted label", () => {
	it("blames the person only for a user interrupt", () => {
		render(<TurnOutcome state="interrupted" />);
		expect(screen.getByText("The agent was interrupted by you")).toBeInTheDocument();
	});

	it("names an app restart instead of the user", () => {
		render(<TurnOutcome state="interrupted" error={APP_RESTART_ERROR} />);
		expect(screen.getByText("The agent was interrupted by an app restart")).toBeInTheDocument();
		expect(screen.queryByText("The agent was interrupted by you")).toBeNull();
		expect(screen.queryByText(APP_RESTART_ERROR)).toBeNull();
	});
});
