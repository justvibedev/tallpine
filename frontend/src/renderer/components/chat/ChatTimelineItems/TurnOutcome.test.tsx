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

describe("TurnOutcome transient network failure", () => {
	const error = "API Error: Can't reach the API server — check your internet or DNS (ENOTFOUND)";

	it("shows a quiet reconnecting line instead of the failure while the daemon retries", () => {
		render(
			<TurnOutcome
				state="failed"
				error={error}
				retry={{ onRetry: () => {} }}
				autoRetry={{ attempt: 2, maxAttempts: 5, nextAt: "2026-11-12T10:00:00Z" }}
			/>,
		);
		expect(screen.getByText("Reconnecting… (attempt 2 of 5)")).toBeInTheDocument();
		expect(screen.queryByText("The agent ran into a problem")).toBeNull();
		expect(screen.queryByRole("button", { name: "Retry this turn" })).toBeNull();
	});

	it("says it waits for the network while offline", () => {
		render(<TurnOutcome state="failed" error={error} autoRetry={{ attempt: 1, maxAttempts: 5, nextAt: "2026-11-12T10:00:00Z", offline: true }} />);
		expect(screen.getByText("Waiting for the network to reconnect…")).toBeInTheDocument();
		expect(screen.queryByText("The agent ran into a problem")).toBeNull();
	});

	it("keeps an already retried network failure quiet", () => {
		render(<TurnOutcome state="failed" error={error} retriedTransient />);
		expect(screen.getByText("Connection dropped; the turn was sent again")).toBeInTheDocument();
		expect(screen.queryByText("The agent ran into a problem")).toBeNull();
	});

	it("still surfaces a failure once automatic retries are used up", () => {
		render(<TurnOutcome state="failed" error={error} />);
		expect(screen.getByText("The agent ran into a problem")).toBeInTheDocument();
	});
});
